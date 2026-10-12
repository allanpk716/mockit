package mcp

// 票 #16 工具层测试:mockit_upgrade 工具(check_only / 执行升级 / 参数
// 校验 / tools/list 收录)与新版提醒(后台检查、提醒只附一次、
// update_check=false 零请求、检查失败静默、同进程不并发重复检查)。
// 全部走假 serve + 假 release(httptest),路径全在 t.TempDir(),
// 不触真实 ~/.claude 与已安装的 mockit 二进制。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mockit/internal/upgrade"
)

// fakeRelease 是假 release 服务器:/latest 302 到 /tag/<latest> 并统计
// 命中次数;/download/<tag>/<name> 下发 addTag 登记的内容,SHA256SUMS.txt
// 按登记自动生成。
type fakeRelease struct {
	srv    *httptest.Server
	latest string
	mu     sync.Mutex
	files  map[string]map[string][]byte
	hits   *int64
}

func newFakeRelease(t *testing.T, latest string) *fakeRelease {
	t.Helper()
	var hits int64
	f := &fakeRelease{
		latest: latest,
		files:  map[string]map[string][]byte{},
		hits:   &hits,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			atomic.AddInt64(f.hits, 1)
			http.Redirect(w, r, "/tag/"+f.latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/tag/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/download/"):
			rest := strings.TrimPrefix(r.URL.Path, "/download/")
			i := strings.Index(rest, "/")
			if i < 0 {
				http.NotFound(w, r)
				return
			}
			tag, name := rest[:i], rest[i+1:]
			f.mu.Lock()
			defer f.mu.Unlock()
			if name == "SHA256SUMS.txt" {
				w.Write([]byte(f.sumsLocked(tag)))
				return
			}
			b, ok := f.files[tag][name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// platformAsset 返回当前平台的资产名(与 release 资产命名契约一致)。
func platformAsset() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("mockit-%s-%s.exe", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("mockit-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// addTag 登记一个 tag 的当前平台二进制资产。
func (f *fakeRelease) addTag(tag string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files[tag] == nil {
		f.files[tag] = map[string][]byte{}
	}
	f.files[tag][platformAsset()] = content
}

// sumsLocked 生成 sha256sum 格式校验和(须持 f.mu 调用)。
func (f *fakeRelease) sumsLocked(tag string) string {
	var b strings.Builder
	for name, content := range f.files[tag] {
		h := sha256.Sum256(content)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	return b.String()
}

// upgradeFixture 组装 mockit_upgrade 的注入依赖:假 release、旧二进制、
// 技能目录、数据目录、配好 mockit 的 Claude 配置,全在临时目录;
// calls 记录新二进制被怎么调用。
type upgradeFixture struct {
	rel    *fakeRelease
	exe    string
	skills string
	data   string
	claude string
	calls  []string
}

func newUpgradeFixture(t *testing.T, latest, current string) (*upgradeFixture, *upgradeDeps) {
	t.Helper()
	dir := t.TempDir()
	exeName := "mockit"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	fx := &upgradeFixture{
		rel:    newFakeRelease(t, latest),
		exe:    filepath.Join(dir, "bin", exeName),
		skills: filepath.Join(dir, "skills"),
		data:   filepath.Join(dir, "data"),
		claude: filepath.Join(dir, "claude.json"),
	}
	if err := os.MkdirAll(filepath.Dir(fx.exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.claude, []byte(`{"mcpServers":{"mockit":{"command":"mockit","args":["mcp"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.rel.addTag("v0.3.0", []byte("new binary 0.3.0"))
	dep := &upgradeDeps{
		checker: &upgrade.Checker{
			BaseURL: fx.rel.srv.URL, DataDir: fx.data, Current: current,
			Client: &http.Client{Timeout: 5 * time.Second},
		},
		upgrader: &upgrade.Upgrader{
			BaseURL: fx.rel.srv.URL, ExePath: fx.exe, SkillsRoot: fx.skills,
			DataDir: fx.data, ClaudeConfigPath: fx.claude, Current: current,
			RunBinary: func(exe string, args ...string) error {
				fx.calls = append(fx.calls, strings.Join(append([]string{exe}, args...), " "))
				return nil
			},
			Client: &http.Client{Timeout: 5 * time.Second},
		},
	}
	return fx, dep
}

// tools/list 应收录 mockit_upgrade,描述写明仅在用户明确要求时调用。
func TestToolsListIncludesMockitUpgrade(t *testing.T) {
	s, out, _ := newTestSrv(t, "")
	if code := s.loop(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}
	envs := outLines(t, out)
	res := wantNoErrorResp(t, envs[len(envs)-1])
	tools, _ := res["tools"].([]any)
	found := false
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		if m["name"] != "mockit_upgrade" {
			continue
		}
		found = true
		desc, _ := m["description"].(string)
		if !strings.Contains(desc, "仅在用户明确要求") {
			t.Fatalf("描述应写明仅在用户明确要求时调用:\n%s", desc)
		}
		schema, _ := m["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["version"]; !ok {
			t.Fatalf("schema 缺 version 参数: %v", schema)
		}
		if _, ok := props["check_only"]; !ok {
			t.Fatalf("schema 缺 check_only 参数: %v", schema)
		}
	}
	if !found {
		t.Fatalf("tools/list 未收录 mockit_upgrade: %v", tools)
	}
}

// check_only:只查当前/最新版本,不安装——二进制不动、不发资产请求。
func TestUpgradeToolCheckOnly(t *testing.T) {
	fx, dep := newUpgradeFixture(t, "v0.3.0", "0.2.0")
	s, _, _ := newTestSrv(t, "")
	s.updeps = dep

	text, isErr := s.callTool(context.Background(), "mockit_upgrade", mustJSON(t, map[string]any{"check_only": true}))
	if isErr {
		t.Fatalf("check_only 不应失败: %s", text)
	}
	for _, want := range []string{"当前版本: 0.2.0", "最新版本: 0.3.0", "可升级: 是"} {
		if !strings.Contains(text, want) {
			t.Fatalf("check_only 文本缺 %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(fx.exe)
	if err != nil || string(got) != "old binary" {
		t.Fatalf("check_only 不得改动二进制: %q err=%v", got, err)
	}
	if len(fx.calls) != 0 {
		t.Fatalf("check_only 不得执行新二进制: %v", fx.calls)
	}
}

// 执行升级:报告字段齐全,二进制被替换、.old 留旧内容、新二进制跑
// install-skill、MCP 配置检查进报告。
func TestUpgradeToolPerformsUpgrade(t *testing.T) {
	fx, dep := newUpgradeFixture(t, "v0.3.0", "0.2.0")
	s, _, _ := newTestSrv(t, "")
	s.updeps = dep

	text, isErr := s.callTool(context.Background(), "mockit_upgrade", mustJSON(t, map[string]any{}))
	if isErr {
		t.Fatalf("升级不应失败: %s", text)
	}
	for _, want := range []string{
		"升级完成:0.2.0 → 0.3.0",
		"二进制:", fx.exe,
		"技能:", upgrade.SkillPath(fx.skills),
		"MCP 配置:正常",
		"请完全退出并重启 Claude Code",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("升级报告缺 %q:\n%s", want, text)
		}
	}
	got, err := os.ReadFile(fx.exe)
	if err != nil || string(got) != "new binary 0.3.0" {
		t.Fatalf("二进制应被替换: %q err=%v", got, err)
	}
	old, err := os.ReadFile(fx.exe + ".old")
	if err != nil || string(old) != "old binary" {
		t.Fatalf(".old 应留旧内容: %q err=%v", old, err)
	}
	if len(fx.calls) != 1 || fx.calls[0] != fx.exe+" install-skill" {
		t.Fatalf("应以新二进制执行 install-skill 一次, got %v", fx.calls)
	}
}

// 指定非法版本号:工具级错误,只断言关键片段。
func TestUpgradeToolBadVersion(t *testing.T) {
	_, dep := newUpgradeFixture(t, "v0.3.0", "0.2.0")
	s, _, _ := newTestSrv(t, "")
	s.updeps = dep

	text, isErr := s.callTool(context.Background(), "mockit_upgrade", mustJSON(t, map[string]any{"version": "abc"}))
	if !isErr || !strings.Contains(text, "不是 vX.Y.Z 形式") {
		t.Fatalf("非法版本应报错: isErr=%v\n%s", isErr, text)
	}
}

// ---- 后台新版检查与提醒(票 #16)----

// callViaLoop 走完整 tools/call 往返拿工具结果文本与 isError(提醒在应答点
// 追加,与取消语义一致)。提醒类用例必须走这里而不是 callTool。
func callViaLoop(t *testing.T, s *server, out *bytes.Buffer, id int, name, args string) (string, bool) {
	t.Helper()
	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", id, name, args)
	if code := s.loop(strings.NewReader(line)); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}
	envs := outLines(t, out)
	if len(envs) == 0 {
		t.Fatal("无响应")
	}
	res := wantNoErrorResp(t, envs[len(envs)-1])
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("结果 content 数 = %d, want 1", len(content))
	}
	c, _ := content[0].(map[string]any)
	text, _ := c["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

// newUpdateSrv 造一个开启新版检查的 server:ensure 指向假 serve,升级依赖
// 指向假 release;done 收到每次后台检查的结果(同步用)。
func newUpdateSrv(t *testing.T, rel *fakeRelease, current string) (*server, *bytes.Buffer, chan upgrade.Result) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(backend.Close)
	s, out, _ := newTestSrv(t, backend.URL)
	s.cfg.UpdateCheck = true
	dir := t.TempDir()
	exeName := "mockit"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exe := filepath.Join(dir, "bin", exeName)
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	dep := &upgradeDeps{
		checker: &upgrade.Checker{
			BaseURL: rel.srv.URL, DataDir: dir, Current: current,
			Client: &http.Client{Timeout: 5 * time.Second},
		},
		upgrader: &upgrade.Upgrader{
			BaseURL: rel.srv.URL, ExePath: exe, SkillsRoot: filepath.Join(dir, "skills"),
			DataDir: dir, ClaudeConfigPath: filepath.Join(dir, "claude.json"), Current: current,
			RunBinary: func(string, ...string) error { return nil },
			Client:    &http.Client{Timeout: 5 * time.Second},
		},
	}
	s.updeps = dep
	done := make(chan upgrade.Result, 8)
	s.onCheckDone = func(res upgrade.Result) { done <- res }
	return s, out, done
}

// waitCheckDone 等一次后台检查收尾(超时即失败)。
func waitCheckDone(t *testing.T, done chan upgrade.Result) upgrade.Result {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("后台检查 5 秒内未收尾")
		return upgrade.Result{}
	}
}

// 已知有新版(非开发版)后,下一个工具结果末尾附一次新版提醒,每个进程
// 至多一次;提醒复用每进程一次附加提示机制。
func TestUpdateNoticeAppendedOnce(t *testing.T) {
	rel := newFakeRelease(t, "v0.3.0")
	s, out, done := newUpdateSrv(t, rel, "0.2.0")

	first, isErr := callViaLoop(t, s, out, 1, "mockit_list", `{}`)
	if isErr || first != "暂无提交" {
		t.Fatalf("首个结果不应携带提醒也不应报错: isErr=%v\n%s", isErr, first)
	}
	res := waitCheckDone(t, done)
	if res.State != upgrade.StateUpgradable {
		t.Fatalf("后台检查状态 = %q, want upgradable(%+v)", res.State, res)
	}

	second, _ := callViaLoop(t, s, out, 2, "mockit_list", `{}`)
	for _, want := range []string{"暂无提交", "mockit 有新版 v0.3.0(当前 v0.2.0)", "mockit_upgrade"} {
		if !strings.Contains(second, want) {
			t.Fatalf("第二个结果应附新版提醒(缺 %q):\n%s", want, second)
		}
	}

	third, _ := callViaLoop(t, s, out, 3, "mockit_list", `{}`)
	if strings.Contains(third, "有新版") {
		t.Fatalf("提醒每进程只附一次,第三个结果不得再附:\n%s", third)
	}
}

// update_check=false:不发出任何检查请求、不提醒(mockit_upgrade 仍可手动用)。
func TestUpdateCheckDisabledNoRequests(t *testing.T) {
	rel := newFakeRelease(t, "v0.3.0")
	s, out, done := newUpdateSrv(t, rel, "0.2.0")
	s.cfg.UpdateCheck = false

	for id := 1; id <= 2; id++ {
		text, isErr := callViaLoop(t, s, out, id, "mockit_list", `{}`)
		if isErr || text != "暂无提交" {
			t.Fatalf("结果不应受影响: isErr=%v\n%s", isErr, text)
		}
	}
	select {
	case res := <-done:
		t.Fatalf("update_check=false 不得触发后台检查: %+v", res)
	case <-time.After(200 * time.Millisecond):
	}
	if got := atomic.LoadInt64(rel.hits); got != 0 {
		t.Fatalf("update_check=false 时 /latest 请求数 = %d, want 0", got)
	}

	// mockit_upgrade 仍可手动使用。
	text, isErr := callViaLoop(t, s, out, 3, "mockit_upgrade", `{"check_only":true}`)
	if isErr || !strings.Contains(text, "0.3.0") {
		t.Fatalf("update_check=false 不影响 mockit_upgrade 手动使用: isErr=%v\n%s", isErr, text)
	}
}

// 检查失败(如 release 服务器出错)静默:不影响任何工具结果、不提醒。
func TestUpdateCheckFailureSilent(t *testing.T) {
	var hits int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(backend.Close)
	s, out, _ := newTestSrv(t, backend.URL)
	s.cfg.UpdateCheck = true
	s.updeps = &upgradeDeps{
		checker: &upgrade.Checker{
			BaseURL: bad.URL, DataDir: t.TempDir(), Current: "0.2.0",
			Client: &http.Client{Timeout: 5 * time.Second},
		},
	}
	done := make(chan upgrade.Result, 8)
	s.onCheckDone = func(res upgrade.Result) { done <- res }

	for id := 1; id <= 2; id++ {
		text, isErr := callViaLoop(t, s, out, id, "mockit_list", `{}`)
		if isErr || text != "暂无提交" {
			t.Fatalf("检查失败应静默,结果不受影响: isErr=%v\n%s", isErr, text)
		}
		if id == 1 {
			waitCheckDone(t, done) // 等首次检查收尾再发第二次调用
		}
	}
	if got := atomic.LoadInt64(&hits); got == 0 {
		t.Fatal("应有检查请求发出(只是失败静默)")
	}
}

// 同一进程内不并发重复检查:检查在飞时后续工具调用不再触发新检查。
func TestUpdateCheckNoConcurrentDuplicates(t *testing.T) {
	gate := make(chan struct{})
	var hits int64
	rel := &fakeRelease{hits: &hits}
	rel.files = map[string]map[string][]byte{}
	rel.latest = "v0.3.0"
	rel.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			atomic.AddInt64(&hits, 1)
			<-gate // 检查挂在半空,制造在飞窗口
			http.Redirect(w, r, "/tag/"+rel.latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/tag/"):
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rel.srv.Close)

	s, _, done := newUpdateSrv(t, rel, "0.2.0")

	// 两行一次喂入:第一行同步占住在飞标记后,第二行的检查应被跳过。
	two := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"mockit_list","arguments":{}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mockit_list","arguments":{}}}` + "\n"
	if code := s.loop(strings.NewReader(two)); code != 0 {
		t.Fatalf("loop 退出码 = %d, want 0", code)
	}
	close(gate)
	waitCheckDone(t, done)

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("在飞窗口内 /latest 请求数 = %d, want 1(不得并发重复检查)", got)
	}
}

// 升级成功后本进程不再发新版提醒(报告已含重启指引,当前版本常量已过时)。
func TestUpgradeSuppressesUpdateNotice(t *testing.T) {
	_, dep := newUpgradeFixture(t, "v0.3.0", "0.2.0")
	dep.checker.Client = &http.Client{Timeout: 5 * time.Second}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(backend.Close)
	s, out, _ := newTestSrv(t, backend.URL)
	s.cfg.UpdateCheck = true
	s.updeps = dep

	rep, isErr := callViaLoop(t, s, out, 1, "mockit_upgrade", `{}`)
	if isErr || !strings.Contains(rep, "升级完成") {
		t.Fatalf("升级不应失败: isErr=%v\n%s", isErr, rep)
	}
	for id := 2; id <= 3; id++ {
		text, isErr := callViaLoop(t, s, out, id, "mockit_list", `{}`)
		if isErr || strings.Contains(text, "有新版") {
			t.Fatalf("升级后不得再附新版提醒: isErr=%v\n%s", isErr, text)
		}
	}
}
