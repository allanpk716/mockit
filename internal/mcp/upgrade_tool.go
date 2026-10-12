// mockit_upgrade 工具(票 #16):调 upgrade 模块的同一套检查/执行入口,
// 输出检查结果或升级报告的文本形式。与后台新版提醒(update.go)共用
// upgradeDeps 注入依赖;生产默认值由 Run 构造,测试注入假 release。

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mockit/internal/config"
	"mockit/internal/upgrade"
)

// upgradeDeps 聚合 mockit_upgrade 工具与后台新版检查的依赖;nil 或字段
// 缺失时工具报未配置、后台检查不进行(其余工具不受影响)。
type upgradeDeps struct {
	checker  *upgrade.Checker  // 检查最新版本(带 24h 缓存)
	upgrader *upgrade.Upgrader // 执行升级
}

// prodUpgradeDeps 构造生产升级依赖:统一走 upgrade.ProdUpgrader 装配
// (替换目标为当前可执行文件真实路径、技能目录与用户级 Claude 配置取
// 默认、基地址走默认),检查缓存用 cfg.DataDir 与 Version。路径解析
// 失败返回 nil——mockit_upgrade 届时报错,不影响其他工具。
func prodUpgradeDeps(cfg *config.Config) *upgradeDeps {
	u, err := upgrade.ProdUpgrader(cfg.DataDir, Version)
	if err != nil {
		return nil
	}
	return &upgradeDeps{
		checker:  upgrade.NewChecker(cfg.DataDir, Version),
		upgrader: u,
	}
}

// upgradeArgs 是 mockit_upgrade 的参数:version 可选(目标版本,含降级),
// check_only 可选(只查不装)。
type upgradeArgs struct {
	Version   string `json:"version"`
	CheckOnly bool   `json:"check_only"`
}

// toolUpgrade 处理 mockit_upgrade:check_only 只查当前/最新版本(强制
// 刷新,与 CLI --check 同口径);默认执行升级并返回报告文本。
func (s *server) toolUpgrade(ctx context.Context, raw json.RawMessage) (string, bool) {
	if s.updeps == nil || s.updeps.checker == nil || s.updeps.upgrader == nil {
		return "升级未配置(无法确定安装路径),请改用 CLI: mockit upgrade", true
	}
	var a upgradeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "参数解析失败: " + err.Error(), true
	}
	if a.CheckOnly { // 与 --version 同时给时以 check_only 为准(同 CLI)
		res := s.updeps.checker.Check(ctx, true)
		return res.String(), res.State == upgrade.StateFailed
	}
	rep, err := s.updeps.upgrader.Upgrade(ctx, strings.TrimSpace(a.Version))
	if err != nil {
		return "升级失败: " + errText(err), true
	}
	// 本进程已升到目标版本:不再做后台检查/发新版提醒(版本常量已过时,
	// 报告已含重启指引)。
	s.updateState.setUpgraded()
	return rep.String(), false
}

// updateNotice 生成新版提醒文本(票 #16 验收文案)。
func updateNotice(latest, current string) string {
	return fmt.Sprintf("mockit 有新版 v%s(当前 v%s),可让 agent 调 mockit_upgrade 升级",
		strings.TrimPrefix(latest, "v"), strings.TrimPrefix(current, "v"))
}
