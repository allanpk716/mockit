// Package upgrade 承载 mockit 的安装与升级辅助(规格 #11):
//   - Checker:检查最新版本(票 #14,结果缓存 24 小时,也是新版提醒的数据源);
//   - Upgrader:执行升级(票 #15,下载、SHA256 校验、替换二进制、
//     新二进制写技能、MCP 配置只读检查、结构化升级报告);
//   - InstallSkill:把内嵌技能正本写入用户级技能目录(install-skill 与升级共用);
//   - RemoveOldBinary:启动时尽力清理升级残留的 .old 旧二进制。
//
// 依赖全部经参数/字段注入(基地址、路径、当前版本、执行函数),生产默认值由调用方提供。
package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
)

// InstallSkill 把技能正本 content 写到技能根目录 skillsRoot 下的
// mockit/SKILL.md:目录不存在就级联创建,同名文件直接覆盖;返回写入路径。
func InstallSkill(skillsRoot string, content []byte) (string, error) {
	path := SkillPath(skillsRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("install-skill: 创建技能目录失败: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("install-skill: 写入 %s 失败: %w", path, err)
	}
	return path, nil
}

// SkillPath 返回技能根目录 skillsRoot 下的写入目标 mockit/SKILL.md。
func SkillPath(skillsRoot string) string {
	return filepath.Join(skillsRoot, "mockit", "SKILL.md")
}

// DefaultSkillsRoot 返回用户级技能根目录(~/.claude/skills)。
func DefaultSkillsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("install-skill: 无法确定家目录: %w", err)
	}
	return filepath.Join(home, ".claude", "skills"), nil
}
