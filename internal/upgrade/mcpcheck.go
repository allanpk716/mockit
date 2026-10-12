// 用户级 Claude 配置的只读检查(票 #15):检查 mcpServers.mockit 是否
// 存在且符合预期,结果进升级报告;从不写这个文件(它由 Claude Code
// 频繁写,mockit 只读——规格 #11)。

package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MCPState 是 mcpServers.mockit 的检查结论,分三种(票 #15 验收)。
type MCPState string

const (
	MCPOk       MCPState = "ok"       // 正常:等于 {"command":"mockit","args":["mcp"]},或 command 指向本二进制
	MCPMissing  MCPState = "missing"  // 缺失:配置文件或 mockit 键不存在
	MCPMismatch MCPState = "mismatch" // 不一致:存在但内容与预期不符
)

// String 给出报告用的中文结论。
func (s MCPState) String() string {
	switch s {
	case MCPOk:
		return "正常"
	case MCPMissing:
		return "缺失"
	case MCPMismatch:
		return "不一致"
	}
	return "未知"
}

// MCPCheck 是一次只读检查的结果;缺失/不一致附修复指引。
type MCPCheck struct {
	State MCPState
	Path  string // 被检查的配置文件路径
	Hint  string // 修复指引(正常时为空)
}

// mcpEntry 是 mcpServers 下一个 server 条目里我们关心的字段。
type mcpEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// DefaultClaudeConfigPath 返回用户级 Claude 配置路径(~/.claude.json)。
func DefaultClaudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("upgrade: 无法确定家目录: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

// CheckMCPConfig 只读检查 cfgPath 里的 mcpServers.mockit:正常、缺失、
// 不一致三种结论,后两种给出修复指引。command 为裸命令 mockit,或绝对
// 路径指向 exePath,且 args 为 ["mcp"],都算正常。
func CheckMCPConfig(cfgPath, exePath string) MCPCheck {
	mc := MCPCheck{Path: cfgPath}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		mc.State = MCPMissing
		mc.Hint = repairHint(cfgPath, fmt.Sprintf("读不到用户级 Claude 配置(%v)", err))
		return mc
	}
	var cfg struct {
		MCPServers map[string]mcpEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		mc.State = MCPMismatch
		mc.Hint = fmt.Sprintf("配置文件不是合法 JSON(%v),请先修复 %s 再按 INSTALL.md 步骤 4 配置 mockit", err, cfgPath)
		return mc
	}
	e, ok := cfg.MCPServers["mockit"]
	if !ok {
		mc.State = MCPMissing
		mc.Hint = repairHint(cfgPath, "mcpServers 下没有 mockit 键")
		return mc
	}
	if argsMCP(e.Args) && (e.Command == "mockit" || samePath(e.Command, exePath)) {
		mc.State = MCPOk
		return mc
	}
	mc.State = MCPMismatch
	mc.Hint = fmt.Sprintf("mcpServers.mockit 与预期不符(command=%q,args=%v),请按 INSTALL.md 步骤 4 改为 {\"command\":\"mockit\",\"args\":[\"mcp\"]}(command 用本二进制绝对路径 %s 也可以)", e.Command, e.Args, exePath)
	return mc
}

// repairHint 生成缺失类问题的修复指引。
func repairHint(cfgPath, cause string) string {
	return fmt.Sprintf("%s;修复:在 %s 的 mcpServers 下写入 \"mockit\": {\"command\":\"mockit\",\"args\":[\"mcp\"]}(步骤见 INSTALL.md 步骤 4)", cause, cfgPath)
}

// argsMCP 判断 args 是否恰为 ["mcp"]。
func argsMCP(args []string) bool {
	return len(args) == 1 && args[0] == "mcp"
}

// samePath 判断两个路径是否同一(清理分隔符;Windows 上不区分大小写)。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
