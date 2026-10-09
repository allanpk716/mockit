package lifecycle

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// freePort 拿一个当前空闲的 TCP 端口再立刻释放,用于构造"没有服务在听"的场景。
// 存在极小的端口被复用窗口,对 localhost 单测可接受。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占空闲端口失败: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// writeLockJSON 写一个 server.lock JSON 夹具(测试造状态用;
// 生产路径是 AcquireInstanceLock,不走这里)。
func writeLockJSON(t *testing.T, dataDir string, l *Lock) {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("序列化 lock 失败: %v", err)
	}
	if err := os.WriteFile(LockPath(dataDir), b, 0o644); err != nil {
		t.Fatalf("写 lock 夹具失败: %v", err)
	}
}

// deadLock 写一个指向无服务端口的 lock 文件。
func deadLock(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: freePort(t), Token: "tok"})
	return dir
}

func TestLockPath(t *testing.T) {
	if got, want := LockPath("C:/data"), "C:/data/server.lock"; got != want {
		t.Errorf("LockPath = %q, want %q", got, want)
	}
}

func TestStartLockPath(t *testing.T) {
	if got, want := StartLockPath("C:/data"), "C:/data/start.lock"; got != want {
		t.Errorf("StartLockPath = %q, want %q", got, want)
	}
}

func TestLockJSONFieldNames(t *testing.T) {
	b, err := json.Marshal(Lock{Port: 1, PID: 2, Version: "v", Token: "t", BaseHost: "h", StartedAt: 3})
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	for _, k := range []string{"port", "pid", "version", "token", "base_host", "started_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Lock 缺少 JSON 键 %q(跨泳道契约)", k)
		}
	}
}

func TestReadLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Lock{
		Port:      9000,
		PID:       4242,
		Version:   "0.1.0",
		Token:     "secret-token",
		BaseHost:  "nuc10.example",
		StartedAt: 1728000000,
	}
	writeLockJSON(t, dir, in)
	out, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock 失败: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip 不一致: in = %+v, out = %+v", in, out)
	}
}

func TestReadLockMissingErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadLock(dir)
	if err == nil {
		t.Fatal("lock 文件缺失应报错")
	}
	if !strings.Contains(err.Error(), "不存在") || !strings.Contains(err.Error(), "server.lock") {
		t.Errorf("错误信息应明确指出 lock 文件不存在, got: %v", err)
	}
}

func TestReadLockBadJSONErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(LockPath(dir), []byte("{oops"), 0o644); err != nil {
		t.Fatalf("写坏 lock 失败: %v", err)
	}
	_, err := ReadLock(dir)
	if err == nil {
		t.Fatal("坏 JSON 的 lock 文件应报错")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("错误信息应指出 JSON 无效, got: %v", err)
	}
}

func TestPingAlive(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"9.9.9"}`)
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: port, Token: "tok"})
	res, err := Ping(dir)
	if err != nil {
		t.Fatalf("Ping 报错: %v", err)
	}
	if !res.Alive {
		t.Error("活实例应返回 Alive = true")
	}
	if res.Version != "9.9.9" {
		t.Errorf("Version = %q, want 9.9.9", res.Version)
	}
	if gotPath != "/ping" || gotMethod != http.MethodGet {
		t.Errorf("应 GET /ping, got %s %s", gotMethod, gotPath)
	}
}

func TestPingAlivePlainTextFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "0.2.0\n")
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: srv.Listener.Addr().(*net.TCPAddr).Port})
	res, err := Ping(dir)
	if err != nil {
		t.Fatalf("Ping 报错: %v", err)
	}
	if !res.Alive || res.Version != "0.2.0" {
		t.Errorf("纯文本版本应兜底解析: %+v, err = %v", res, err)
	}
}

func TestPingDeadPortNotAlive(t *testing.T) {
	dir := deadLock(t)
	res, err := Ping(dir)
	if err != nil {
		t.Fatalf("Ping 不通不应报错(Alive=false 表达), got: %v", err)
	}
	if res.Alive {
		t.Errorf("无服务端口应 Alive = false, got %+v", res)
	}
}

func TestPingNon200NotAlive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: srv.Listener.Addr().(*net.TCPAddr).Port})
	res, err := Ping(dir)
	if err != nil {
		t.Fatalf("Ping 非 200 不应报错, got: %v", err)
	}
	if res.Alive {
		t.Errorf("非 200 应 Alive = false, got %+v", res)
	}
}

func TestPingNoLockErrors(t *testing.T) {
	dir := t.TempDir()
	res, err := Ping(dir)
	if err == nil {
		t.Fatal("lock 缺失时 Ping 应报错")
	}
	if res != nil {
		t.Errorf("lock 缺失时结果应为 nil, got %+v", res)
	}
}

func TestShutdownCarriesToken(t *testing.T) {
	var gotMethod, gotToken, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotToken, gotPath = r.Method, r.Header.Get("X-Mockit-Token"), r.URL.Path
		if gotToken != "secret-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: srv.Listener.Addr().(*net.TCPAddr).Port, Token: "secret-token"})
	if err := Shutdown(dir); err != nil {
		t.Fatalf("Shutdown 应成功, got: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("shutdown 应为 POST, got %s", gotMethod)
	}
	if gotPath != "/shutdown" {
		t.Errorf("shutdown 路径 = %q, want /shutdown", gotPath)
	}
	if gotToken != "secret-token" {
		t.Errorf("shutdown 应携带 lock token, got %q", gotToken)
	}
}

func TestShutdownTokenMismatchRejected(t *testing.T) {
	// 服务端校验 token:发给它的 token 来自 lock;这里用拒绝分支验证
	// Shutdown 对非 200 响应报错(token 正确性由服务端保证)。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mockit-Token") == "secret-token" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: srv.Listener.Addr().(*net.TCPAddr).Port, Token: "wrong"})
	if err := Shutdown(dir); err == nil {
		t.Error("非 200 响应 Shutdown 应报错")
	}
}

func TestShutdownNoLockErrors(t *testing.T) {
	dir := t.TempDir()
	if err := Shutdown(dir); err == nil {
		t.Error("lock 缺失时 Shutdown 应报错")
	}
}

func TestShutdownDeadPortErrors(t *testing.T) {
	dir := deadLock(t)
	err := Shutdown(dir)
	if err == nil {
		t.Fatal("无服务端口 Shutdown 应报错")
	}
	// 红线:错误文本不得携带内部 URL(Go http 客户端错误原文形如
	// Post "http://127.0.0.1:port/shutdown": dial tcp ...,会泄漏给 MCP 工具层)。
	if strings.Contains(err.Error(), "http") || strings.Contains(err.Error(), "://") {
		t.Fatalf("Shutdown 错误文本泄漏 URL:\n%v", err)
	}
}
