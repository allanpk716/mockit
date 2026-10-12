// Package upgrade 承载 mockit 的安装与升级辅助(规格 #11)。
//
// 本票(#12)交付其中两件:
//   - InstallSkill:把内嵌技能正本写入用户级技能目录(install-skill 与升级共用);
//   - RemoveOldBinary:启动时尽力清理升级残留的 .old 旧二进制。
//
// 依赖全部经参数注入(技能根目录、正本内容、可执行文件路径),生产默认值由调用方提供。
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
