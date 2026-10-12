package main

import (
	"os"
	"strings"
	"testing"
)

// 票 #12 验收:二进制内嵌仓内技能正本,不另存副本(两者不会分叉);
// usage 文案列出 install-skill。CLI 分派是薄转发,不单测。

func TestEmbeddedSkillMatchesRepoOriginal(t *testing.T) {
	disk, err := os.ReadFile("skills/mockit/SKILL.md") // 独立来源:构建后磁盘上的正本
	if err != nil {
		t.Fatalf("读仓内技能正本失败: %v", err)
	}
	if len(skillMD) == 0 {
		t.Fatal("内嵌技能为空:go:embed 未生效或路径指错")
	}
	if string(skillMD) != string(disk) {
		t.Fatal("内嵌技能与仓内正本 skills/mockit/SKILL.md 不一致(出现分叉)")
	}
}

func TestUsageMentionsInstallSkill(t *testing.T) {
	if !strings.Contains(usageText(), "install-skill") {
		t.Fatalf("usage 文案应包含 install-skill: %q", usageText())
	}
}

func TestUsageMentionsUpgradeVersionFlag(t *testing.T) {
	if !strings.Contains(usageText(), "upgrade [--version vX.Y.Z] [--check]") {
		t.Fatalf("usage 文案应包含 upgrade 与其参数: %q", usageText())
	}
}
