// 执行升级入口(票 #15,包级说明见 skill.go):下载对应平台资产、替换
// 当前二进制(改 .old 落临时到位)、由新二进制执行 install-skill 写技能、
// 只读检查 MCP 配置,产出结构化升级报告。依赖全部经字段注入。

package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"mockit/internal/lifecycle"
)

// Upgrader 执行升级;依赖全部经字段注入(票 #15 验收):
// release 基地址、要替换的可执行文件路径、技能目录、数据目录(升级锁与
// 检查缓存)、用户级 Claude 配置路径、当前版本、执行新二进制的函数。
type Upgrader struct {
	BaseURL          string                                 // release 基地址
	ExePath          string                                 // 要替换的可执行文件(已解析软链的真实路径)
	SkillsRoot       string                                 // 技能根目录(报告技能路径)
	DataDir          string                                 // 数据目录(升级锁、检查缓存)
	ClaudeConfigPath string                                 // 用户级 Claude 配置(~/.claude.json),只读
	Current          string                                 // 当前版本(如 0.2.0)
	RunBinary        func(exe string, args ...string) error // 执行新二进制;nil 用真实 exec
	Client           *http.Client                           // 可选注入;默认遵守代理环境变量
}

// NewUpgrader 构造带生产默认值的执行升级入口:release 基地址(可被隐藏
// 环境变量 MOCKIT_RELEASE_BASE 覆盖)、执行新二进制的真实实现;其余依赖
// (路径、数据目录、当前版本)由调用方填入。
func NewUpgrader() *Upgrader {
	return &Upgrader{BaseURL: defaultBaseURL(), RunBinary: ExecBinary}
}

// CurrentExePath 返回当前运行文件解析软链之后的真实路径——与升级替换
// 目标一致(审查 T4:原先在 main.go 与 mcp/upgrade_tool.go 各解析一份)。
func CurrentExePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法确定当前可执行文件: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// ProdUpgrader 构造生产升级依赖(审查 T4:装配逻辑原先在 main.go 与
// mcp/upgrade_tool.go 各一份):替换目标为当前可执行文件真实路径,
// 技能目录与用户级 Claude 配置取默认,基地址走默认(可被隐藏环境变量
// 覆盖)。路径解析失败返回错误——调用方降级为"升级未配置"或直接报错。
func ProdUpgrader(dataDir, current string) (*Upgrader, error) {
	exe, err := CurrentExePath()
	if err != nil {
		return nil, err
	}
	skillsRoot, err := DefaultSkillsRoot()
	if err != nil {
		return nil, err
	}
	claudeCfg, err := DefaultClaudeConfigPath()
	if err != nil {
		return nil, err
	}
	u := NewUpgrader()
	u.ExePath = exe
	u.SkillsRoot = skillsRoot
	u.DataDir = dataDir
	u.ClaudeConfigPath = claudeCfg
	u.Current = current
	return u, nil
}

// ExecBinary 真实执行一个二进制(升级后以新二进制跑 install-skill 用)。
// Windows 下 HideWindow(零闪窗铁律,同 mcp 拉起 serve 的做法)。
func ExecBinary(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = io.Discard // 输出由升级报告呈现,不透传
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = procAttr()
	return cmd.Run()
}

// lockPath 返回数据目录下的升级锁文件路径。
func (u *Upgrader) lockPath() string { return filepath.Join(u.DataDir, "upgrade.lock") }

// client 返回生效的 HTTP 客户端(下载可能较大,不设整体超时,由 ctx 控制)。
func (u *Upgrader) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
}

// Upgrade 执行升级:version 为空升级到最新 release,否则升级到指定版本
// (含降级)。已是最新(且未指定版本)只返回报告不改任何文件;开发版
// (当前版本解析不了或高于最新 release)不带版本号时拒绝。任何一步失败
// 都尽力回滚,确保原文件可用。
func (u *Upgrader) Upgrade(ctx context.Context, version string) (Report, error) {
	// 升级锁:非阻塞获取,拿不到立即失败,防止两边同时替换文件。
	guard, err := lifecycle.TryExclusiveLock(u.lockPath())
	if err != nil {
		if errors.Is(err, lifecycle.ErrLocked) {
			return Report{}, fmt.Errorf("另一升级进行中(%s 被占用),请稍后再试", u.lockPath())
		}
		return Report{}, err
	}
	defer guard.Release()

	// 定目标版本:不带版本号 → 最新 release;带版本号 → 校验形式后指定(含降级)。
	target := version
	if target == "" {
		ck := &Checker{BaseURL: u.BaseURL, DataDir: u.DataDir, Current: u.Current, Client: u.Client}
		res := ck.Check(ctx, true) // 升级是显式动作,强制刷新,不吃可能过期的缓存
		if res.State == StateFailed {
			return Report{}, fmt.Errorf("查询最新版本失败: %s", res.Err)
		}
		if res.State == StateDev {
			return Report{}, fmt.Errorf("当前版本 %s 是开发版(解析不了或高于最新 release %s),不带 --version 时拒绝升级;确要更换版本请显式指定 --version vX.Y.Z",
				dispVersion(u.Current), dispVersion(res.Latest))
		}
		if res.State == StateLatest {
			return Report{AlreadyLatest: true, From: u.Current, To: res.Latest}, nil
		}
		target = res.Latest
	} else {
		tag, err := normalizeTag(version)
		if err != nil {
			return Report{}, err
		}
		target = tag
	}

	oldPath, err := u.replace(ctx, target)
	if err != nil {
		return Report{}, err
	}

	// 以新二进制执行 install-skill 写技能,保证技能与二进制同版本;
	// 失败不回滚二进制,报告里标出并给手动命令。
	skillErr := ""
	if err := u.runBinary(u.ExePath, "install-skill"); err != nil {
		skillErr = fmt.Sprintf("新二进制 install-skill 失败:%v;手动修复:运行 mockit install-skill", err)
	}

	return Report{
		From:       u.Current,
		To:         target,
		BinaryPath: u.ExePath,
		OldPath:    oldPath,
		SkillPath:  SkillPath(u.SkillsRoot),
		SkillErr:   skillErr,
		MCP:        CheckMCPConfig(u.ClaudeConfigPath, u.ExePath),
	}, nil
}

// runBinary 执行新二进制(未注入时用真实 exec)。
func (u *Upgrader) runBinary(exe string, args ...string) error {
	if u.RunBinary == nil {
		return ExecBinary(exe, args...)
	}
	return u.RunBinary(exe, args...)
}

// assetName 返回当前平台的 release 资产名(契约:mockit-<os>-<arch>[.exe])。
func assetName() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("mockit-%s-%s.exe", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("mockit-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// normalizeTag 规范化 --version 参数为 vX.Y.Z 形式(允许省略 v 前缀),
// 形式不对直接拒绝。
func normalizeTag(v string) (string, error) {
	tag := strings.TrimSpace(v)
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	if _, ok := ParseSemver(tag); !ok {
		return "", fmt.Errorf("--version %q 不是 vX.Y.Z 形式", v)
	}
	return tag, nil
}

// replace 下载 tag 的资产并替换 u.ExePath,返回旧二进制实际落位(改名
// 后的路径):临时文件建在安装目录旁边(创建失败即目标目录不可写,
// 直接失败给手动命令)→ SHA256SUMS 校验 → 设可执行权限 → 原文件改名
// 为 <名>.old(被占用时改用备用名 .old.1、.old.2…)→ 临时文件改名到位;
// 任何一步失败都尽力回滚,原文件保持可用。
func (u *Upgrader) replace(ctx context.Context, tag string) (string, error) {
	dir := filepath.Dir(u.ExePath)
	asset := assetName()

	tmp, err := os.CreateTemp(dir, ".mockit-upgrade-*")
	if err != nil {
		return "", u.manualErr(dir, err, tag, asset)
	}
	tmpName := tmp.Name()
	// 关句柄+删除残留:成功改名后此路径已不存在,两步都落空无害;
	// Windows 上不关句柄连删除都不允许。
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	body, err := u.fetch(ctx, tag, asset)
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(body); err != nil {
		return "", fmt.Errorf("写临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("关闭临时文件失败: %w", err)
	}

	want, err := u.expectedSum(ctx, tag, asset)
	if err != nil {
		return "", err
	}
	got, err := fileSHA256(tmpName)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, want) {
		return "", fmt.Errorf("SHA256 校验不一致(资产 %s:期望 %s,实得 %s),已中止,原文件未动", asset, want, got)
	}

	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", fmt.Errorf("设置可执行权限失败: %w", err)
	}
	oldPath, err := u.reserveOld()
	if err != nil {
		return "", err
	}
	if err := os.Rename(u.ExePath, oldPath); err != nil {
		return "", fmt.Errorf("把原文件改名为 %s 失败: %w", oldPath, err)
	}
	if err := os.Rename(tmpName, u.ExePath); err != nil {
		if rb := os.Rename(oldPath, u.ExePath); rb != nil {
			return "", fmt.Errorf("临时文件改名到位失败:%v;回滚亦失败:%v(原文件在 %s,请手动恢复)", err, rb, oldPath)
		}
		return "", fmt.Errorf("临时文件改名到位失败(已回滚,原文件可用): %w", err)
	}
	return oldPath, nil
}

// reserveOld 挑一个旧二进制的落位名并腾出位置:优先 <名>.old(不存在或
// 删掉旧残留即用);被占用删不掉(Windows 上旧 serve/MCP 仍从该文件
// 运行)则依次改用 .old.1、.old.2…(审查 S2:运行中也能再次升级)。
func (u *Upgrader) reserveOld() (string, error) {
	for i := 0; i <= oldAltLimit; i++ {
		cand := oldPath(u.ExePath, i)
		if err := os.Remove(cand); err == nil || os.IsNotExist(err) {
			return cand, nil // 名字可用(不存在或残留已删)
		}
	}
	return "", fmt.Errorf("无法腾出旧文件位置(%s%s 及备用名均被占用,请手动清理后重试)", u.ExePath, oldSuffix)
}

// manualErr 生成"目标目录不可写"的失败:直接失败并给出手动升级命令;
// 不提权,也不改装到别处(规格 #11)。
func (u *Upgrader) manualErr(dir string, cause error, tag, asset string) error {
	return fmt.Errorf("无法在安装目录 %s 创建临时文件(%v);mockit 不提权、也不改装到别处,请手动升级:从 %s/download/%s/%s 下载并替换 %s(步骤见 INSTALL.md)",
		dir, cause, strings.TrimSuffix(u.BaseURL, "/"), tag, asset, u.ExePath)
}

// expectedSum 取 tag 的 SHA256SUMS.txt 里 asset 的期望哈希。
func (u *Upgrader) expectedSum(ctx context.Context, tag, asset string) (string, error) {
	b, err := u.fetch(ctx, tag, "SHA256SUMS.txt")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS.txt 里没有资产 %s 的条目", asset)
}

// fetch 下载 tag 下资产 name 的完整内容。
func (u *Upgrader) fetch(ctx context.Context, tag, name string) ([]byte, error) {
	url := fmt.Sprintf("%s/download/%s/%s", strings.TrimSuffix(u.BaseURL, "/"), tag, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 %s 失败: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 失败: HTTP %d", name, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("下载 %s 失败: %w", name, err)
	}
	return b, nil
}

// fileSHA256 计算文件内容的 SHA256(十六进制小写)。
func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读 %s 失败: %w", path, err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Report 是一次执行升级的结构化报告(规格 #11:旧→新版本、二进制路径、
// 技能路径、MCP 配置检查结果、重启提示)。
type Report struct {
	From          string   // 旧版本(原样)
	To            string   // 新版本 tag(如 v0.3.0)
	BinaryPath    string   // 被替换的二进制路径
	OldPath       string   // 旧二进制改名后的实际落位(.old 或备用名 .old.N)
	SkillPath     string   // 技能写入目标
	SkillErr      string   // 非空 = 新二进制 install-skill 失败(含手动命令)
	MCP           MCPCheck // MCP 配置只读检查结果
	AlreadyLatest bool     // 已是最新:未做任何修改
}

// String 给出人读的升级报告(CLI `mockit upgrade` 直接打印)。
func (r Report) String() string {
	if r.AlreadyLatest {
		return fmt.Sprintf("已是最新 %s,未做任何修改", dispVersion(r.To))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "升级完成:%s → %s\n", dispVersion(r.From), dispVersion(r.To))
	fmt.Fprintf(&b, "二进制:%s(旧二进制改名为 %s,之后的 mockit 启动会尽力清理)\n", r.BinaryPath, r.OldPath)
	if r.SkillErr != "" {
		fmt.Fprintf(&b, "技能:未写入!%s\n", r.SkillErr)
	} else {
		fmt.Fprintf(&b, "技能:%s\n", r.SkillPath)
	}
	fmt.Fprintf(&b, "MCP 配置:%s(%s)\n", r.MCP.State, r.MCP.Path)
	if r.MCP.State != MCPOk && r.MCP.Hint != "" {
		fmt.Fprintf(&b, "    %s\n", r.MCP.Hint)
	}
	b.WriteString("请完全退出并重启 Claude Code(只开新会话不够);正在运行的 serve 不受影响,重启后由新 MCP 接管对齐。")
	return b.String()
}
