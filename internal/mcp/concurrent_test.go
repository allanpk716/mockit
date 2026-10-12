package mcp

// 并发与取消(规格 #4 票 08)的协议往返测试:
//   - 慢工具调用进行中,ping 仍能及时得到响应;
//   - notifications/cancelled 中断在飞 HTTP 请求,被取消的请求不再收到
//     回复,随后的调用不受影响;
//   - 多个并发工具调用的响应各自是完整的一行 JSON,不交错、不丢失。
//
// 同步一律用 channel + deadline,不用固定 sleep。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// lineChanWriter 把每次写出按行拆分送入 channel;测试据此在响应到达的
// 时刻观察 stdout,而非事后扫缓冲区。
type lineChanWriter struct {
	ch  chan string
	mu  sync.Mutex
	buf []byte
}

func newLineChanWriter() *lineChanWriter {
	return &lineChanWriter{ch: make(chan string, 64)}
}

func (w *lineChanWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.ch <- line
	}
	return len(p), nil
}

// protoSession 驱动一次活的协议会话:stdin 是 io.Pipe(可逐行投喂),
// loop 跑在独立 goroutine,stdout 行进 channel;all 登记收到过的全部响应。
type protoSession struct {
	t    *testing.T
	s    *server
	inW  *io.PipeWriter
	outW *lineChanWriter
	done chan int
	all  []respEnvelope
}

func startSession(t *testing.T, backendURL string) *protoSession {
	t.Helper()
	outW := newLineChanWriter()
	s, _, _ := newTestSrv(t, backendURL)
	s.out = outW
	inR, inW := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- s.loop(inR) }()
	return &protoSession{t: t, s: s, inW: inW, outW: outW, done: done}
}

// send 投喂一行协议输入。
func (p *protoSession) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.inW, line+"\n"); err != nil {
		p.t.Fatalf("写 stdin 失败: %v", err)
	}
}

// recv 按 deadline 收一行响应并登记进 all;超时即失败(及时性本身是被测行为)。
func (p *protoSession) recv(deadline time.Duration, what string) respEnvelope {
	p.t.Helper()
	select {
	case ln := <-p.outW.ch:
		var env respEnvelope
		if err := json.Unmarshal([]byte(ln), &env); err != nil {
			p.t.Fatalf("%s: stdout 行不是合法 JSON(疑似并发写出交错): %v\n行: %s", what, err, ln)
		}
		p.all = append(p.all, env)
		return env
	case <-time.After(deadline):
		p.t.Fatalf("%s: %v 内未收到响应", what, deadline)
		return respEnvelope{}
	}
}

// waitSig 按 deadline 等一个信号。
func waitSig(t *testing.T, ch <-chan struct{}, deadline time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(deadline):
		t.Fatalf("%s: %v 内未发生", what, deadline)
	}
}

// close 关 stdin,等 loop 收尾在飞调用后退出并返回退出码;loop 退出后
// 残余的响应行(若有)一并登记进 all——此时所有在飞调用已结束,登记是完备的。
func (p *protoSession) close() int {
	p.t.Helper()
	if err := p.inW.Close(); err != nil {
		p.t.Fatalf("关 stdin 失败: %v", err)
	}
	select {
	case code := <-p.done:
		for {
			select {
			case ln := <-p.outW.ch:
				var env respEnvelope
				if err := json.Unmarshal([]byte(ln), &env); err != nil {
					p.t.Fatalf("loop 退出后残余行不是合法 JSON: %v\n行: %s", err, ln)
				}
				p.all = append(p.all, env)
				continue
			default:
			}
			break
		}
		return code
	case <-time.After(10 * time.Second):
		p.t.Fatal("loop 未在期限内退出(stdin 关闭后应等在飞调用收尾并退出)")
		return -1
	}
}

// toolText 取 tools/call 结果里 content[0].text。
func toolText(t *testing.T, env respEnvelope) string {
	t.Helper()
	content, _ := env.Result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call 结果缺 content: %v", env.Result)
	}
	c0, _ := content[0].(map[string]any)
	text, _ := c0["text"].(string)
	return text
}

// 验收:一个被假 serve 拖慢的工具调用进行中,ping 能及时得到响应。
// 顺序实现会在工具调用结束前读不到 ping 行,本测试超时失败。
func TestPingResponsiveDuringSlowToolCall(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/submissions/ab12cd" {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release // 拖住工具调用的 HTTP 请求,直到测试放行
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ab12cd","title":"登录页","status":"pending","decision":"","chosen_variant":null,"comment":"","variants":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	// LIFO:先 doRelease 再 Close——失败路径若未放行假 serve,直接 Close 会与
	// handler 的 <-release 互相等死。
	defer backend.Close()
	defer doRelease()

	p := startSession(t, backend.URL)
	p.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mockit_get_review","arguments":{"id":"ab12cd"}}}`)
	waitSig(t, entered, 5*time.Second, "假 serve 收到工具调用的 HTTP 请求")

	// 慢调用挂着期间发 ping:必须及时拿到响应。
	p.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if env := p.recv(5*time.Second, "慢工具调用期间的 ping 响应"); strings.TrimSpace(string(env.ID)) != "2" || env.Error != nil {
		t.Fatalf("ping 响应不符: id=%s err=%+v", env.ID, env.Error)
	}

	doRelease()
	env := p.recv(5*time.Second, "工具调用响应")
	if strings.TrimSpace(string(env.ID)) != "1" || env.Error != nil {
		t.Fatalf("工具调用响应不符: id=%s err=%+v", env.ID, env.Error)
	}
	if code := p.close(); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}
}

// 验收:发送 notifications/cancelled 后,对应的 HTTP 请求被中断、该请求
// 不再收到回复,随后的调用正常;对未知 id 的取消按规范静默忽略。
func TestCancelledToolCallInterruptsHTTPAndGetsNoReply(t *testing.T) {
	entered := make(chan struct{}, 1)
	interrupted := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/submissions/slow00":
			select {
			case entered <- struct{}{}:
			default:
			}
			// 挂住直到客户端断开:服务端请求 context 结束即“HTTP 请求被中断”的观察点。
			<-r.Context().Done()
			select {
			case interrupted <- struct{}{}:
			default:
			}
		case r.URL.Path == "/api/submissions/ok11ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ok11ok","title":"正常提交","status":"pending","decision":"","chosen_variant":null,"comment":"","variants":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	p := startSession(t, backend.URL)
	p.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mockit_get_review","arguments":{"id":"slow00"}}}`)
	waitSig(t, entered, 5*time.Second, "假 serve 收到工具调用的 HTTP 请求")

	// 取消 id=1:在飞 HTTP 请求应被中断(假 serve 侧观察到请求 context 结束)。
	p.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"用户打断"}}`)
	waitSig(t, interrupted, 5*time.Second, "取消通知中断在飞 HTTP 请求")

	// 对未知 id 的取消:按 MCP 规范静默忽略,不影响后续。
	p.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}`)

	p.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if env := p.recv(5*time.Second, "取消后的 ping 响应"); strings.TrimSpace(string(env.ID)) != "2" || env.Error != nil {
		t.Fatalf("ping 响应不符: id=%s err=%+v", env.ID, env.Error)
	}

	p.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mockit_get_review","arguments":{"id":"ok11ok"}}}`)
	env := p.recv(5*time.Second, "取消后的新工具调用响应")
	if strings.TrimSpace(string(env.ID)) != "3" || env.Error != nil {
		t.Fatalf("新工具调用响应不符: id=%s err=%+v", env.ID, env.Error)
	}
	if text := toolText(t, env); !strings.Contains(text, "ok11ok") || !strings.Contains(text, "待审") {
		t.Fatalf("新工具调用文本不符:\n%s", text)
	}

	// loop 退出意味着所有在飞调用已收尾;此时全部响应里不得有 id=1 的任何回复。
	if code := p.close(); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}
	got := map[string]int{}
	for _, env := range p.all {
		got[idKey(env.ID)]++
	}
	if got["1"] != 0 {
		t.Fatalf("已取消的请求(id=1)不应收到任何回复, 实收 %d 条", got["1"])
	}
	if len(p.all) != 2 || got["2"] != 1 || got["3"] != 1 {
		t.Fatalf("应有且仅有 id=2、id=3 两条响应, 实得 %v", got)
	}
}

// 验收:并发写出不交错——多个并发工具调用的响应各自是完整的一行 JSON,
// id 一一对应、不丢不重。假 serve 用栅栏等全部请求到齐后同时放行,制造
// 并发完成;逐行解析即检测交错(半行/拼行都过不了解析)。
func TestConcurrentToolCallWritesDoNotInterleave(t *testing.T) {
	const n = 8
	var barrier sync.WaitGroup
	barrier.Add(n)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		barrier.Done()
		open := make(chan struct{})
		go func() { barrier.Wait(); close(open) }()
		select {
		case <-open: // n 个请求全部到齐,同时放行
		case <-r.Context().Done(): // 顺序实现的兜底:客户端超时断开,handler 不挂死
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer backend.Close()

	s, out, _ := newTestSrv(t, backend.URL)
	var in strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&in, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"mockit_list","arguments":{}}}`+"\n", i)
	}
	if code := s.loop(strings.NewReader(in.String())); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}

	envs := outLines(t, out) // 内部逐行解析,交错即失败
	if len(envs) != n {
		t.Fatalf("响应行数 = %d, want %d\n%s", len(envs), n, out.String())
	}
	got := map[string]int{}
	for _, env := range envs {
		if env.Error != nil {
			t.Fatalf("id=%s 意外 JSON-RPC error: %+v", env.ID, env.Error)
		}
		if _, has := env.Result["isError"]; has {
			t.Fatalf("id=%s 不应 isError: %v", env.ID, env.Result)
		}
		if text := toolText(t, env); text != "暂无提交" {
			t.Fatalf("id=%s 文本 = %q, want 暂无提交", env.ID, text)
		}
		got[idKey(env.ID)]++
	}
	for i := 1; i <= n; i++ {
		if got[strconv.Itoa(i)] != 1 {
			t.Fatalf("id=%d 的响应 = %d 条, want 恰 1 条(全部: %v)", i, got[strconv.Itoa(i)], got)
		}
	}
}
