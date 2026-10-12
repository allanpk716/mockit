package upgrade

// 执行升级测试(票 #15):httptest 假 release 服务器 + 临时目录夹具,
// 只测外部行为——升级报告字段、文件系统结果、新二进制调用记录,
// 不测内部拆法;错误文案只断言关键片段。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mockit/internal/lifecycle"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeUpstream 是假 release 服务器:/latest 302 到 /tag/<latest>;
// /download/<tag>/<资产名> 下发 addTag 登记的内容,SHA256SUMS.txt 按
// 登记自动生成(corrupt 登记过的 tag 一律给全零错误哈希)。
type fakeUpstream struct {
	srv     *httptest.Server
	latest  string
	mu      sync.Mutex
	files   map[string]map[string][]byte
	corrupt map[string]bool
}

func newFakeUpstream(t *testing.T, latest string) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{latest: latest, files: map[string]map[string][]byte{}, corrupt: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
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

// addTag 登记一个 tag 的二进制资产(当前平台资产名)。
func (f *fakeUpstream) addTag(tag string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files[tag] == nil {
		f.files[tag] = map[string][]byte{}
	}
	f.files[tag][assetName()] = content
}

// corruptSums 让指定 tag 的 SHA256SUMS 全部给错(校验不一致用例)。
func (f *fakeUpstream) corruptSums(tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.corrupt[tag] = true
}

// sumsLocked 生成 sha256sum 格式的校验和(须持 f.mu 调用)。
func (f *fakeUpstream) sumsLocked(tag string) string {
	var b strings.Builder
	for name, content := range f.files[tag] {
		sum := strings.Repeat("0", 64)
		if !f.corrupt[tag] {
			h := sha256.Sum256(content)
			sum = hex.EncodeToString(h[:])
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, name)
	}
	return b.String()
}

// fixture 组装一套执行升级夹具:假 release、旧二进制、技能目录、数据目录、
// 配好 mockit 的 Claude 配置,全部在临时目录;calls 记录新二进制调用,
// runErr 可注入新二进制失败。
type fixture struct {
	up     *fakeUpstream
	u      *Upgrader
	exeDir string
	exe    string
	skills string
	data   string
	claude string
	calls  []string
	runErr error
}

func newFixture(t *testing.T, latest, current string) *fixture {
	t.Helper()
	dir := t.TempDir()
	exeName := "mockit"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	fx := &fixture{
		up:     newFakeUpstream(t, latest),
		exeDir: filepath.Join(dir, "bin"),
		exe:    filepath.Join(dir, "bin", exeName),
		skills: filepath.Join(dir, "skills"),
		data:   filepath.Join(dir, "data"),
		claude: filepath.Join(dir, "claude.json"),
	}
	if err := os.MkdirAll(fx.exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.claude, []byte(`{"mcpServers":{"mockit":{"command":"mockit","args":["mcp"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.u = &Upgrader{
		BaseURL:          fx.up.srv.URL,
		ExePath:          fx.exe,
		SkillsRoot:       fx.skills,
		DataDir:          fx.data,
		ClaudeConfigPath: fx.claude,
		Current:          current,
		RunBinary: func(exe string, args ...string) error {
			fx.calls = append(fx.calls, strings.Join(append([]string{exe}, args...), " "))
			return fx.runErr
		},
	}
	return fx
}

// onlyFile 断言目录 dir 里恰好只剩名字 name 的一个文件(无临时残留)。
func onlyFile(t *testing.T, dir, name string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != name {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("目录 %s 内容 = %v,想只剩 [%s]", dir, names, name)
	}
}

func TestUpgradeReplacesBinaryWritesSkillReports(t *testing.T) {
	fx := newFixture(t, "v0.3.0", "0.2.0")
	fx.up.addTag("v0.3.0", []byte("new binary 0.3.0"))

	rep, err := fx.u.Upgrade(context.Background(), "")

	if err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	// 二进制被替换为下载内容,旧字节挪到 .old
	got, err := os.ReadFile(fx.exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary 0.3.0" {
		t.Fatalf("替换后二进制内容 = %q", got)
	}
	old, err := os.ReadFile(fx.exe + ".old")
	if err != nil {
		t.Fatalf(".old 应存在: %v", err)
	}
	if string(old) != "old binary" {
		t.Fatalf(".old 内容 = %q,想旧字节", old)
	}
	// 由新二进制执行 install-skill 写技能
	if len(fx.calls) != 1 || fx.calls[0] != fx.exe+" install-skill" {
		t.Fatalf("新二进制调用 = %v,想 [%s install-skill]", fx.calls, fx.exe)
	}
	// 报告字段:旧→新版本、二进制路径、技能路径、MCP 检查、重启提示
	if rep.From != "0.2.0" || rep.To != "v0.3.0" {
		t.Fatalf("版本 = %s → %s,想 0.2.0 → v0.3.0", rep.From, rep.To)
	}
	if rep.BinaryPath != fx.exe {
		t.Fatalf("报告二进制路径 = %q", rep.BinaryPath)
	}
	if rep.SkillPath != SkillPath(fx.skills) {
		t.Fatalf("报告技能路径 = %q", rep.SkillPath)
	}
	if rep.MCP.State != MCPOk {
		t.Fatalf("MCP 检查 = %+v,想正常", rep.MCP)
	}
	s := rep.String()
	for _, want := range []string{"0.2.0 → 0.3.0", fx.exe, SkillPath(fx.skills), "完全退出并重启 Claude Code", "只开新会话不够"} {
		if !strings.Contains(s, want) {
			t.Fatalf("升级报告 %q 缺 %q", s, want)
		}
	}
}

// 指定版本与降级:--version 任意已发布版本(含降级、等于当前重装);
// 版本号须 vX.Y.Z 形式(允许省略 v)。
func TestUpgradeSpecifiedVersionAndDowngrade(t *testing.T) {
	t.Run("降级到指定版本", func(t *testing.T) {
		fx := newFixture(t, "v0.3.1", "0.3.0")
		fx.up.addTag("v0.3.1", []byte("newest"))
		fx.up.addTag("v0.2.0", []byte("binary 0.2.0"))

		rep, err := fx.u.Upgrade(context.Background(), "v0.2.0")

		if err != nil {
			t.Fatalf("指定版本升级失败: %v", err)
		}
		if rep.To != "v0.2.0" || rep.From != "0.3.0" {
			t.Fatalf("版本 = %s → %s,想 0.3.0 → v0.2.0", rep.From, rep.To)
		}
		got, _ := os.ReadFile(fx.exe)
		if string(got) != "binary 0.2.0" {
			t.Fatalf("替换后二进制内容 = %q,想 v0.2.0 资产", got)
		}
	})

	t.Run("省略 v 前缀也认", func(t *testing.T) {
		fx := newFixture(t, "v0.3.1", "0.2.0")
		fx.up.addTag("v0.3.1", []byte("newest"))
		fx.up.addTag("v0.3.0", []byte("binary 0.3.0"))

		rep, err := fx.u.Upgrade(context.Background(), "0.3.0")

		if err != nil || rep.To != "v0.3.0" {
			t.Fatalf("省略 v 前缀应按 v0.3.0 处理: rep=%+v err=%v", rep, err)
		}
	})

	t.Run("版本号不是 vX.Y.Z 拒绝", func(t *testing.T) {
		fx := newFixture(t, "v0.3.0", "0.2.0")
		fx.up.addTag("v0.3.0", []byte("new"))

		_, err := fx.u.Upgrade(context.Background(), "latest")

		if err == nil || !strings.Contains(err.Error(), "vX.Y.Z") {
			t.Fatalf("坏版本号应拒绝并说明形式,得: %v", err)
		}
		onlyFile(t, fx.exeDir, filepath.Base(fx.exe)) // 什么都没动
	})
}

// SHA256 不一致:删除临时文件并中止,原文件字节不变,无残留。
func TestUpgradeChecksumMismatchAborts(t *testing.T) {
	fx := newFixture(t, "v0.3.0", "0.2.0")
	fx.up.addTag("v0.3.0", []byte("new binary"))
	fx.up.corruptSums("v0.3.0")

	_, err := fx.u.Upgrade(context.Background(), "")

	if err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("校验不一致应中止并说明,得: %v", err)
	}
	got, _ := os.ReadFile(fx.exe)
	if string(got) != "old binary" {
		t.Fatalf("原文件字节被改动: %q", got)
	}
	onlyFile(t, fx.exeDir, filepath.Base(fx.exe)) // 无临时文件、无 .old
	if len(fx.calls) != 0 {
		t.Fatalf("不应执行新二进制,得 %v", fx.calls)
	}
}

// 已是最新(且未指定版本):只返回"已是最新 vX",不修改任何文件。
func TestUpgradeAlreadyLatestNoChanges(t *testing.T) {
	fx := newFixture(t, "v0.3.0", "0.3.0")
	fx.up.addTag("v0.3.0", []byte("same"))
	before, _ := os.ReadFile(fx.claude)

	rep, err := fx.u.Upgrade(context.Background(), "")

	if err != nil {
		t.Fatalf("已是最新不应报错: %v", err)
	}
	if !rep.AlreadyLatest {
		t.Fatalf("应标记已是最新: %+v", rep)
	}
	if !strings.Contains(rep.String(), "已是最新 0.3.0") {
		t.Fatalf("报告应说明已是最新: %q", rep.String())
	}
	got, _ := os.ReadFile(fx.exe)
	if string(got) != "old binary" {
		t.Fatalf("二进制被改动: %q", got)
	}
	if _, err := os.Stat(fx.exe + ".old"); !os.IsNotExist(err) {
		t.Fatal("不应产生 .old")
	}
	if _, err := os.Stat(SkillPath(fx.skills)); !os.IsNotExist(err) {
		t.Fatal("不应写技能")
	}
	after, _ := os.ReadFile(fx.claude)
	if string(before) != string(after) {
		t.Fatal("不应改 Claude 配置")
	}
	if len(fx.calls) != 0 {
		t.Fatalf("不应执行新二进制,得 %v", fx.calls)
	}
}

// 开发版(解析不了或高于最新 release)不带 --version 拒绝;显式指定版本则放行。
func TestUpgradeDevRejectedWithoutVersion(t *testing.T) {
	for _, current := range []string{"dev-build", "0.9.9"} {
		t.Run("当前="+current, func(t *testing.T) {
			fx := newFixture(t, "v0.3.0", current)
			fx.up.addTag("v0.3.0", []byte("new"))

			_, err := fx.u.Upgrade(context.Background(), "")

			if err == nil || !strings.Contains(err.Error(), "开发版") {
				t.Fatalf("开发版应拒绝并说明原因,得: %v", err)
			}
			got, _ := os.ReadFile(fx.exe)
			if string(got) != "old binary" {
				t.Fatalf("二进制被改动: %q", got)
			}
		})
	}
	t.Run("开发版显式指定版本放行", func(t *testing.T) {
		fx := newFixture(t, "v0.3.0", "dev-build")
		fx.up.addTag("v0.3.0", []byte("new"))

		if _, err := fx.u.Upgrade(context.Background(), "v0.3.0"); err != nil {
			t.Fatalf("显式指定版本应放行: %v", err)
		}
		got, _ := os.ReadFile(fx.exe)
		if string(got) != "new" {
			t.Fatalf("二进制应被替换: %q", got)
		}
	})
}

// 升级锁被占用:立即失败,提示"另一升级进行中",不动文件。
func TestUpgradeLockHeldFailsImmediately(t *testing.T) {
	fx := newFixture(t, "v0.3.0", "0.2.0")
	fx.up.addTag("v0.3.0", []byte("new"))
	g, err := lifecycle.TryExclusiveLock(filepath.Join(fx.data, "upgrade.lock"))
	if err != nil {
		t.Fatalf("预占锁失败: %v", err)
	}
	defer g.Release()

	_, err = fx.u.Upgrade(context.Background(), "")

	if err == nil || !strings.Contains(err.Error(), "另一升级进行中") {
		t.Fatalf("锁被占应立即失败并提示,得: %v", err)
	}
	got, _ := os.ReadFile(fx.exe)
	if string(got) != "old binary" {
		t.Fatalf("二进制被改动: %q", got)
	}
	onlyFile(t, fx.exeDir, filepath.Base(fx.exe))
}

// 目标目录不可写:直接失败,给出手动升级命令;不提权、不改安装位置。
// (用"安装目录其实是普通文件"制造不可写,Windows/POSIX 都成立。)
func TestUpgradeUnwritableDirFailsWithManualCommand(t *testing.T) {
	fx := newFixture(t, "v0.3.0", "0.2.0")
	fx.up.addTag("v0.3.0", []byte("new"))
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	exeName := "mockit"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	fx.u.ExePath = filepath.Join(blocker, exeName) // 父"目录"是文件 → 建临时文件必败

	_, err := fx.u.Upgrade(context.Background(), "")

	if err == nil || !strings.Contains(err.Error(), "手动") {
		t.Fatalf("不可写应失败并给手动命令,得: %v", err)
	}
}

// install-skill 失败:升级本身仍算完成,报告标出并给手动命令。
func TestUpgradeSkillFailureReported(t *testing.T) {
	fx := newFixture(t, "v0.3.0", "0.2.0")
	fx.up.addTag("v0.3.0", []byte("new"))
	fx.runErr = errors.New("boom")

	rep, err := fx.u.Upgrade(context.Background(), "")

	if err != nil {
		t.Fatalf("技能失败不应让整个升级报错: %v", err)
	}
	if rep.SkillErr == "" || !strings.Contains(rep.SkillErr, "install-skill") {
		t.Fatalf("报告应标出技能失败并给手动命令: %q", rep.SkillErr)
	}
	if !strings.Contains(rep.String(), "手动") {
		t.Fatalf("报告文本应含手动修复: %q", rep.String())
	}
	got, _ := os.ReadFile(fx.exe)
	if string(got) != "new" {
		t.Fatalf("二进制应已替换: %q", got)
	}
}

// MCP 配置只读检查:正常(裸命令/绝对路径)、缺失、不一致三种结论;
// 后两种附修复指引;从不写这个文件。
func TestCheckMCPConfigStates(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "claude.json")
	exePath := filepath.Join(dir, "mockit.exe")

	cases := []struct {
		name string
		json string // 空串 = 文件不存在
		want MCPState
		hint bool // 是否应带修复指引
	}{
		{"裸命令正常", `{"mcpServers":{"mockit":{"command":"mockit","args":["mcp"]}}}`, MCPOk, false},
		{"绝对路径指向本二进制也算正常", `{"mcpServers":{"mockit":{"command":` + strconv.Quote(exePath) + `,"args":["mcp"]}}}`, MCPOk, false},
		{"没有 mockit 键缺失", `{"mcpServers":{}}`, MCPMissing, true},
		{"配置文件不存在缺失", "", MCPMissing, true},
		{"指向别的命令不一致", `{"mcpServers":{"mockit":{"command":"other-tool","args":["mcp"]}}}`, MCPMismatch, true},
		{"args 不对不一致", `{"mcpServers":{"mockit":{"command":"mockit","args":["serve"]}}}`, MCPMismatch, true},
		{"坏 JSON 不一致", `{not-json`, MCPMismatch, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.json == "" {
				_ = os.Remove(cfgPath)
			} else if err := os.WriteFile(cfgPath, []byte(tc.json), 0o644); err != nil {
				t.Fatal(err)
			}

			mc := CheckMCPConfig(cfgPath, exePath)

			if mc.State != tc.want {
				t.Fatalf("结论 = %q,想 %q(hint=%q)", mc.State, tc.want, mc.Hint)
			}
			if (mc.Hint != "") != tc.hint {
				t.Fatalf("修复指引有无不对: %q", mc.Hint)
			}
			if tc.hint && !strings.Contains(mc.Hint, "INSTALL") {
				t.Fatalf("指引应指向 INSTALL.md: %q", mc.Hint)
			}
			// 只读:文件字节不变
			if tc.json != "" {
				b, _ := os.ReadFile(cfgPath)
				if string(b) != tc.json {
					t.Fatalf("配置文件被改动: %q", b)
				}
			}
		})
	}
}
