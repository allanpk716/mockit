package mcp

// 工具层测试:三工具对 fake httptest serve 的行为、b64 提交断言、
// 参数校验、lock 缺失/ping 不通两路错误文案(F1:任何工具输出不得含 URL)。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mockit/internal/lifecycle"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSubmitInlineHTMLPostsB64(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ab12cd","status":"pending","variant_count":1}`)
	}))
	defer backend.Close()

	s, _, _ := newTestSrv(t, backend.URL)
	html := "<h1>你好</h1>"
	text, isErr := s.callTool("mockit_submit", mustJSON(t, map[string]any{
		"title": "登录页",
		"note":  "备注文案",
		"variants": []map[string]any{
			{"label": "方案A", "html": html},
		},
	}))
	if isErr {
		t.Fatalf("提交不应失败: %s", text)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/submissions" {
		t.Fatalf("请求 = %s %s, want POST /api/submissions", gotMethod, gotPath)
	}
	if gotBody["title"] != "登录页" || gotBody["note"] != "备注文案" {
		t.Fatalf("payload title/note 不符: %v", gotBody)
	}
	variants, _ := gotBody["variants"].([]any)
	v0, _ := variants[0].(map[string]any)
	if v0["label"] != "方案A" || v0["kind"] != "html" {
		t.Fatalf("variant0 = %v", v0)
	}
	dec, err := base64.StdEncoding.DecodeString(v0["content_b64"].(string))
	if err != nil {
		t.Fatalf("content_b64 非法: %v", err)
	}
	if string(dec) != html {
		t.Fatalf("b64 解码 = %q, want %q", dec, html)
	}
	for _, want := range []string{"ab12cd", "pending", "候选数=1", "暂无链接"} {
		if !strings.Contains(text, want) {
			t.Fatalf("成功文本缺 %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "http") || strings.Contains(text, "127.0.0.1") {
		t.Fatalf("成功文本不得含任何 URL(F1):\n%s", text)
	}
}

func TestSubmitPathVariantsKindMapping(t *testing.T) {
	var gotBody map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = io.WriteString(w, `{"id":"zz99zz","status":"pending","variant_count":3}`)
	}))
	defer backend.Close()

	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "a.html")
	htmPath := filepath.Join(dir, "b.htm")
	zipPath := filepath.Join(dir, "c.zip")
	htmlBody, htmBody, zipBody := "<p>page-a</p>", "<i>page-b</i>", []byte("PK\x03\x04 fake-zip-bytes")
	for _, f := range []struct {
		path string
		data []byte
	}{{htmlPath, []byte(htmlBody)}, {htmPath, []byte(htmBody)}, {zipPath, zipBody}} {
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s, _, _ := newTestSrv(t, backend.URL)
	text, isErr := s.callTool("mockit_submit", mustJSON(t, map[string]any{
		"title": "多候选",
		"variants": []map[string]any{
			{"label": "A", "path": htmlPath},
			{"label": "B", "path": htmPath},
			{"label": "C", "path": zipPath},
		},
	}))
	if isErr {
		t.Fatalf("提交不应失败: %s", text)
	}
	variants, _ := gotBody["variants"].([]any)
	if len(variants) != 3 {
		t.Fatalf("variant 数 = %d, want 3", len(variants))
	}
	wantKinds := []string{"html", "html", "zip"}
	wantData := []string{htmlBody, htmBody, string(zipBody)}
	for i, v := range variants {
		m, _ := v.(map[string]any)
		if m["kind"] != wantKinds[i] {
			t.Fatalf("variant%d kind = %v, want %s", i, m["kind"], wantKinds[i])
		}
		dec, err := base64.StdEncoding.DecodeString(m["content_b64"].(string))
		if err != nil || string(dec) != wantData[i] {
			t.Fatalf("variant%d b64 解码不符: err=%v", i, err)
		}
	}
	if !strings.Contains(text, "候选数=3") || !strings.Contains(text, "暂无链接") {
		t.Fatalf("成功文本缺候选数或暂无链接说明:\n%s", text)
	}
}

func TestSubmitOversizeRejected(t *testing.T) {
	s, _, _ := newTestSrv(t, "") // 不应触达网络与 lock
	big := strings.Repeat("a", maxFileBytes+1)
	text, isErr := s.callTool("mockit_submit", mustJSON(t, map[string]any{
		"title":    "超大内联",
		"variants": []map[string]any{{"label": "A", "html": big}},
	}))
	if !isErr || !strings.Contains(text, "20MB") {
		t.Fatalf("内联超限应报 20MB: isErr=%v text=%s", isErr, text)
	}

	dir := t.TempDir()
	bigPath := filepath.Join(dir, "big.html")
	if err := os.WriteFile(bigPath, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	text, isErr = s.callTool("mockit_submit", mustJSON(t, map[string]any{
		"title":    "超大文件",
		"variants": []map[string]any{{"label": "A", "path": bigPath}},
	}))
	if !isErr || !strings.Contains(text, "20MB") {
		t.Fatalf("path 超限应报 20MB: isErr=%v text=%s", isErr, text)
	}
}

func TestSubmitParamValidation(t *testing.T) {
	s, _, _ := newTestSrv(t, "")
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "d.txt")
	if err := os.WriteFile(txtPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"缺title", map[string]any{"variants": []map[string]any{{"label": "A", "html": "<b/>"}}}, "title"},
		{"缺variants", map[string]any{"title": "t"}, "候选"},
		{"html与path二选一", map[string]any{"title": "t", "variants": []map[string]any{{"label": "A", "html": "<b/>", "path": txtPath}}}, "二选一"},
		{"两者皆无", map[string]any{"title": "t", "variants": []map[string]any{{"label": "A"}}}, "之一"},
		{"坏扩展名", map[string]any{"title": "t", "variants": []map[string]any{{"label": "A", "path": txtPath}}}, "不支持的文件类型"},
	}
	for _, c := range cases {
		text, isErr := s.callTool("mockit_submit", mustJSON(t, c.args))
		if !isErr {
			t.Fatalf("%s: 应报错, 得成功文本: %s", c.name, text)
		}
		if !strings.Contains(text, c.want) {
			t.Fatalf("%s: 文本缺 %q: %s", c.name, c.want, text)
		}
	}
}

func TestGetReviewAndListAgainstFakeServe(t *testing.T) {
	var gotQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/submissions/ab12cd":
			_, _ = io.WriteString(w, `{"id":"ab12cd","title":"登录页","status":"pending","decision":"","chosen_variant":null,"comment":"","variants":[{"seq":1,"label":"A","kind":"html"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/submissions":
			gotQuery = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"items":[{"id":"a1","title":"甲","status":"pending"},{"id":"b2","title":"乙","status":"reviewed"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	s, _, _ := newTestSrv(t, backend.URL)

	text, isErr := s.callTool("mockit_get_review", mustJSON(t, map[string]any{"id": "ab12cd"}))
	if isErr {
		t.Fatalf("get_review 不应失败: %s", text)
	}
	for _, want := range []string{"ab12cd", "登录页", "pending(待审)", "未裁决"} {
		if !strings.Contains(text, want) {
			t.Fatalf("get_review 文本缺 %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "http") {
		t.Fatalf("get_review 文本含 URL,违反 F6:\n%s", text)
	}

	text, isErr = s.callTool("mockit_list", mustJSON(t, map[string]any{"status": "pending", "limit": 5}))
	if isErr {
		t.Fatalf("list 不应失败: %s", text)
	}
	if !strings.Contains(gotQuery, "status=pending") || !strings.Contains(gotQuery, "limit=5") {
		t.Fatalf("list 查询参数 = %q", gotQuery)
	}
	for _, want := range []string{"a1", "甲", "b2", "乙"} {
		if !strings.Contains(text, want) {
			t.Fatalf("list 文本缺 %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "http") {
		t.Fatalf("list 文本含 URL,违反 F6:\n%s", text)
	}
}

func TestListEmptyAndNoFilters(t *testing.T) {
	var gotQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer backend.Close()

	s, _, _ := newTestSrv(t, backend.URL)
	text, isErr := s.callTool("mockit_list", json.RawMessage("{}"))
	if isErr {
		t.Fatalf("list 不应失败: %s", text)
	}
	if gotQuery != "" {
		t.Fatalf("无过滤参数时 query = %q, want 空", gotQuery)
	}
	if text != "暂无提交" {
		t.Fatalf("空列表文本 = %q, want 暂无提交", text)
	}
}

func TestUnknownTool(t *testing.T) {
	s, _, _ := newTestSrv(t, "")
	text, isErr := s.callTool("mockit_nope", json.RawMessage("{}"))
	if !isErr || !strings.Contains(text, "未知工具") {
		t.Fatalf("未知工具应报错: isErr=%v text=%s", isErr, text)
	}
}

// lockPathFixture 直接写 JSON 夹具文件(不依赖票 01 的 lifecycle 实现),
// 返回注入 readLock 用的解析函数(测试侧自行反序列化,只用 Lock 类型)。
func lockPathFixture(t *testing.T, port int) (string, func(string) (*lifecycle.Lock, error)) {
	t.Helper()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "server.lock")
	lockJSON := fmt.Sprintf(`{"port":%d,"pid":1,"version":"0.1.0","token":"tok","base_host":"","started_at":1}`, port)
	if err := os.WriteFile(lockPath, []byte(lockJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	parse := func(string) (*lifecycle.Lock, error) {
		b, err := os.ReadFile(lockPath)
		if err != nil {
			return nil, err
		}
		var l lifecycle.Lock
		if err := json.Unmarshal(b, &l); err != nil {
			return nil, err
		}
		return &l, nil
	}
	return lockPath, parse
}

func TestServeNotRunningWhenLockMissing(t *testing.T) {
	s, _, _ := newTestSrv(t, "")
	s.readLock = lifecycle.ReadLock // 真实路径:DataDir 是无 lock 文件的临时目录
	s.ping = nil
	text, isErr := s.callTool("mockit_list", json.RawMessage("{}"))
	if !isErr {
		t.Fatalf("lock 缺失应报错: %s", text)
	}
	if !strings.Contains(text, "serve 未运行,请先手工运行 mockit serve") {
		t.Fatalf("lock 缺失文案不符:\n%s", text)
	}
}

func TestServeNotRunningWhenPingFails(t *testing.T) {
	// 占一个端口再放掉 → 大概率无人监听的死端口。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	_, parse := lockPathFixture(t, deadPort)
	s, _, _ := newTestSrv(t, "")
	s.readLock = parse
	s.ping = pingServe // 默认真实探活
	text, isErr := s.callTool("mockit_list", json.RawMessage("{}"))
	if !isErr {
		t.Fatalf("ping 不通应报错: %s", text)
	}
	if !strings.Contains(text, "serve 未运行,请先手工运行 mockit serve") {
		t.Fatalf("ping 不通文案不符:\n%s", text)
	}
	if !strings.Contains(text, "ping") {
		t.Fatalf("ping 不通文案应注明 ping 环节:\n%s", text)
	}
}

// 全真路径:lock 夹具(端口=假 serve)→ 默认 ping 探活通过 → 工具正常拿数据。
func TestResolveViaLockAndPingHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"version":"0.1.0"}`)
	})
	mux.HandleFunc("/api/submissions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"items":[]}`)
	})
	backend := httptest.NewServer(mux)
	defer backend.Close()
	port := backend.URL[strings.LastIndex(backend.URL, ":")+1:]

	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		t.Fatal(err)
	}
	_, parse := lockPathFixture(t, p)
	s, _, _ := newTestSrv(t, "")
	s.readLock = parse
	s.ping = pingServe
	text, isErr := s.callTool("mockit_list", json.RawMessage("{}"))
	if isErr {
		t.Fatalf("lock+ping 全通不应失败: %s", text)
	}
	if text != "暂无提交" {
		t.Fatalf("文本 = %q, want 暂无提交", text)
	}
}
