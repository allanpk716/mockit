package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

// freeDriftBase 返回 p 与 p+1 当前都空闲的 p(验证后即释放,竞争窗口极小)。
func freeDriftBase(t *testing.T) int {
	t.Helper()
	for i := 0; i < 100; i++ {
		l1, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		p := l1.Addr().(*net.TCPAddr).Port
		l1.Close()
		l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+1))
		if err != nil {
			continue
		}
		l2.Close()
		return p
	}
	t.Fatal("找不到可用的相邻端口对")
	return 0
}

// bindRun 绑定从随机起点起的 n 个连续端口,全部成功才返回(全占用例)。
func bindRun(t *testing.T, n int) (int, []net.Listener) {
	t.Helper()
	for attempt := 0; attempt < 30; attempt++ {
		l0, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		base := l0.Addr().(*net.TCPAddr).Port
		lns := []net.Listener{l0}
		ok := true
		for i := 1; i < n; i++ {
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+i))
			if err != nil {
				for _, l := range lns {
					l.Close()
				}
				ok = false
				break
			}
			lns = append(lns, ln)
		}
		if ok {
			return base, lns
		}
	}
	t.Fatal("找不到连续空闲端口")
	return 0, nil
}

// waitLock 轮询等待 lock 文件写成功。
func waitLock(t *testing.T, dataDir string, timeout time.Duration) *lifecycle.Lock {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		lk, err := lifecycle.ReadLock(dataDir)
		if err == nil {
			return lk
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock 未在 %v 内写入: %v", timeout, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// localClient 本机短超时 HTTP client。
func localClient() *http.Client { return &http.Client{Timeout: 5 * time.Second} }

func TestServePortDriftLockShutdown(t *testing.T) {
	base := freeDriftBase(t)
	block, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base))
	if err != nil {
		t.Fatalf("占住首选端口失败: %v", err)
	}
	defer block.Close()

	// 探测注入为零命中:本机真实网卡不进单测(票 09);零命中 → base_host 留空。
	oldAddrs := interfaceAddrs
	interfaceAddrs = func() ([]net.IP, error) { return nil, nil }
	t.Cleanup(func() { interfaceAddrs = oldAddrs })

	dataDir := t.TempDir()
	cfg := &config.Config{
		Port: base, Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}

	codeCh := make(chan int, 1)
	go func() { codeCh <- Serve(cfg) }()

	lk := waitLock(t, dataDir, 10*time.Second)
	if lk.Port != base+1 {
		t.Fatalf("lock.port=%d,应漂移到 %d", lk.Port, base+1)
	}
	if lk.Version != "0.1.0" {
		t.Fatalf("lock.version=%q", lk.Version)
	}
	if lk.Token == "" {
		t.Fatal("lock.token 不应为空")
	}
	if lk.BaseHost != "" {
		t.Fatalf("lock.base_host 应留空(零命中且未配置 external_url),得 %q", lk.BaseHost)
	}
	if lk.PID != os.Getpid() {
		t.Fatalf("lock.pid=%d,应 %d", lk.PID, os.Getpid())
	}
	if lk.StartedAt == 0 {
		t.Fatal("lock.started_at 应非零")
	}

	// 实际端口上 ping 通
	resp, err := localClient().Get(fmt.Sprintf("http://127.0.0.1:%d/ping", lk.Port))
	if err != nil {
		t.Fatalf("ping 实际端口失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !containsVersion(body, "0.1.0") {
		t.Fatalf("ping 错: %d %q", resp.StatusCode, body)
	}

	// shutdown 错 token → 403 且仍存活
	wrong := shutdownReq(t, lk.Port, "wrong-token")
	if wrong != http.StatusForbidden {
		t.Fatalf("错 token 应 403,得 %d", wrong)
	}
	if resp, err := localClient().Get(fmt.Sprintf("http://127.0.0.1:%d/ping", lk.Port)); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("错 token 后应仍存活: %v", err)
	} else {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	// shutdown 对 token → 200,Serve 以退出码 0 收场
	if code := shutdownReq(t, lk.Port, lk.Token); code != http.StatusOK {
		t.Fatalf("对 token 应 200,得 %d", code)
	}
	select {
	case c := <-codeCh:
		if c != 0 {
			t.Fatalf("Serve 退出码 %d,应 0", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve 未在 shutdown 后退出")
	}

	// 日志文件已建
	if _, err := os.Stat(filepath.Join(dataDir, "logs", "serve.log")); err != nil {
		t.Fatalf("serve.log 未创建: %v", err)
	}
}

func TestServeAllPortsBusy(t *testing.T) {
	base, lns := bindRun(t, 10)
	defer func() {
		for _, l := range lns {
			l.Close()
		}
	}()

	dataDir := t.TempDir()
	cfg := &config.Config{
		Port: base, Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}

	codeCh := make(chan int, 1)
	go func() { codeCh <- Serve(cfg) }()
	select {
	case c := <-codeCh:
		if c != 1 {
			t.Fatalf("全占应退出码 1,得 %d", c)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve 未在全占时退出")
	}

	// 绑定失败不得写 lock
	if _, err := lifecycle.ReadLock(dataDir); err == nil {
		t.Fatal("绑定失败不应写 lock")
	}
}

// shutdownReq 发 shutdown 请求,返回状态码。
func shutdownReq(t *testing.T, port int, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/shutdown", port), nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set("X-Mockit-Token", token)
	resp, err := localClient().Do(req)
	if err != nil {
		t.Fatalf("shutdown 请求: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// containsVersion 兼容 {"version":"x"} 与纯文本两种 ping 响应形态。
func containsVersion(body []byte, want string) bool {
	s := string(body)
	if len(s) >= 2 && s[0] == '{' {
		var pj map[string]string
		if err := json.Unmarshal(body, &pj); err == nil {
			return pj["version"] == want
		}
	}
	return strings.TrimSpace(s) == want
}
