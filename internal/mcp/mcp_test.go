package mcp

// 协议层测试:行分隔 JSON-RPC 的 round-trip、通知静默、坏行跳过、未知方法。

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

// newTestSrv 造一个注入了假后端/假 lock 的 server。backendURL 非空时作为
// API 基址注入(baseOverride);readLock/ping 保持“触达即报错”的哨兵,
// 用于断言测试没有意外走到 serve 定位。
func newTestSrv(t *testing.T, backendURL string) (*server, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	s := &server{
		cfg:    &config.Config{DataDir: t.TempDir()},
		out:    out,
		errOut: errOut,
		readLock: func(string) (*lifecycle.Lock, error) {
			return nil, errors.New("测试不应触达 lock")
		},
		ping:  func(int) error { return errors.New("测试不应触达 ping") },
		httpc: &http.Client{Timeout: 5 * time.Second},
	}
	if backendURL != "" {
		s.baseOverride = backendURL
	}
	return s, out, errOut
}

type respEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Result map[string]any  `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// outLines 把 stdout 缓冲按行解析成响应信封;每行都必须是合法 JSON(协议只走 stdout)。
func outLines(t *testing.T, out *bytes.Buffer) []respEnvelope {
	t.Helper()
	var envs []respEnvelope
	for _, ln := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var env respEnvelope
		if err := json.Unmarshal([]byte(ln), &env); err != nil {
			t.Fatalf("stdout 行不是合法 JSON 响应: %v\n行: %s", err, ln)
		}
		envs = append(envs, env)
	}
	return envs
}

func wantNoErrorResp(t *testing.T, env respEnvelope) map[string]any {
	t.Helper()
	if env.Error != nil {
		t.Fatalf("意外的 JSON-RPC error: code=%d msg=%s", env.Error.Code, env.Error.Message)
	}
	return env.Result
}

func TestProtocolRoundTripInitializeToolsListToolsCall(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/submissions/ab12cd" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ab12cd","title":"登录页","status":"reviewed","decision":"choose","chosen_variant":2,"comment":"第二个布局好","variants":[{"seq":1,"label":"方案A","kind":"html"},{"seq":2,"label":"方案B","kind":"html"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer backend.Close()

	s, out, _ := newTestSrv(t, backend.URL)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"client","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mockit_get_review","arguments":{"id":"ab12cd"}}}`,
		``,
	}, "\n")

	if code := s.loop(strings.NewReader(input)); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}

	envs := outLines(t, out)
	if len(envs) != 3 {
		t.Fatalf("响应行数 = %d, want 3(notifications/initialized 不回应):\n%s", len(envs), out.String())
	}

	// initialize:回显请求的 protocolVersion;serverInfo 与 capabilities.tools。
	initRes := wantNoErrorResp(t, envs[0])
	if initRes["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want 回显 2025-06-18", initRes["protocolVersion"])
	}
	info, _ := initRes["serverInfo"].(map[string]any)
	if info == nil || info["name"] != "mockit" || info["version"] != Version {
		t.Fatalf("serverInfo = %v, want {mockit, %s}", initRes["serverInfo"], Version)
	}
	caps, _ := initRes["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("capabilities 缺 tools: %v", initRes["capabilities"])
	}

	// tools/list:三个工具,各带 inputSchema。
	listRes := wantNoErrorResp(t, envs[1])
	tools, _ := listRes["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools 数 = %d, want 3", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		name, _ := m["name"].(string)
		names[name] = true
		if _, ok := m["inputSchema"]; !ok {
			t.Fatalf("工具 %v 缺 inputSchema", m["name"])
		}
	}
	for _, want := range []string{"mockit_submit", "mockit_get_review", "mockit_list"} {
		if !names[want] {
			t.Fatalf("缺工具 %s, 实得 %v", want, names)
		}
	}

	// tools/call:结果为 text content,含状态/裁决/选中候选/批注,不含 URL(F6)。
	callRes := wantNoErrorResp(t, envs[2])
	if _, has := callRes["isError"]; has {
		t.Fatalf("tools/call 不应 isError: %v", callRes)
	}
	content, _ := callRes["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("content 为空: %v", callRes)
	}
	c0, _ := content[0].(map[string]any)
	if c0["type"] != "text" {
		t.Fatalf("content[0].type = %v, want text", c0["type"])
	}
	text := c0["text"].(string)
	for _, want := range []string{"ab12cd", "已审", "choose", "方案B", "2", "第二个布局好"} {
		if !strings.Contains(text, want) {
			t.Fatalf("结果文本缺 %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "http") {
		t.Fatalf("结果文本含 URL,违反 F1/F6:\n%s", text)
	}
}

func TestInitializeDefaultProtocolVersion(t *testing.T) {
	s, out, _ := newTestSrv(t, "")
	if code := s.loop(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")); code != 0 {
		t.Fatalf("退出码 = %d, want 0", code)
	}
	envs := outLines(t, out)
	if len(envs) != 1 {
		t.Fatalf("响应行数 = %d, want 1", len(envs))
	}
	r := wantNoErrorResp(t, envs[0])
	if r["protocolVersion"] != "2024-11-05" {
		t.Fatalf("缺省 protocolVersion = %v, want 2024-11-05", r["protocolVersion"])
	}
}

func TestPingReturnsEmptyObject(t *testing.T) {
	s, out, _ := newTestSrv(t, "")
	s.loop(strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n"))
	envs := outLines(t, out)
	if len(envs) != 1 {
		t.Fatalf("响应行数 = %d, want 1", len(envs))
	}
	r := wantNoErrorResp(t, envs[0])
	if len(r) != 0 {
		t.Fatalf("ping 结果 = %v, want {}", r)
	}
}

func TestBadJSONSkippedAndNotificationSilent(t *testing.T) {
	s, out, errOut := newTestSrv(t, "")
	input := strings.Join([]string{
		`{"jsonrpc":"2.0", "id":7, "method":`, // 半截坏行
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"totally/unknown"}`,
		`{"jsonrpc":"2.0","id":9,"method":"ping"}`,
		``,
	}, "\n")
	if code := s.loop(strings.NewReader(input)); code != 0 {
		t.Fatalf("坏 JSON 行导致非零退出: %d", code)
	}
	envs := outLines(t, out)
	if len(envs) != 1 {
		t.Fatalf("响应行数 = %d, want 1(坏行跳过,通知静默):\n%s", len(envs), out.String())
	}
	if strings.TrimSpace(string(envs[0].ID)) != "9" {
		t.Fatalf("唯一响应应为 id=9 的 ping, 得 id=%s", envs[0].ID)
	}
	if !strings.Contains(errOut.String(), "坏 JSON") {
		t.Fatalf("stderr 未记录坏行: %q", errOut.String())
	}
}

func TestUnknownMethodError(t *testing.T) {
	s, out, _ := newTestSrv(t, "")
	s.loop(strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"mockit/nope"}` + "\n"))
	envs := outLines(t, out)
	if len(envs) != 1 || envs[0].Error == nil {
		t.Fatalf("未知方法应回 JSON-RPC error, 得 %+v", envs)
	}
	if envs[0].Error.Code != -32601 {
		t.Fatalf("错误码 = %d, want -32601", envs[0].Error.Code)
	}
}
