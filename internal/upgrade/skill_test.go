package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

// InstallSkill 测试缝:技能根目录注入(临时目录),只断言外部行为——
// 写入路径、文件内容、目录创建与覆盖,不测内部拆法(票 #12)。

func TestInstallSkillWritesContentCreatesDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "deep", "skills") // 故意不存在,断言级联创建
	content := []byte("---\nname: mockit\n---\n\n正文\n")

	path, err := InstallSkill(root, content)
	if err != nil {
		t.Fatalf("InstallSkill 失败: %v", err)
	}
	if want := filepath.Join(root, "mockit", "SKILL.md"); path != want {
		t.Fatalf("返回路径 = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回写入文件失败: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("写入内容与给定正本不一致: got %q", got)
	}
}

func TestInstallSkillOverwritesExisting(t *testing.T) {
	root := t.TempDir()
	path := SkillPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("旧版技能内容"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := InstallSkill(root, []byte("新版技能内容"))
	if err != nil {
		t.Fatalf("InstallSkill 失败: %v", err)
	}
	if got != path {
		t.Fatalf("返回路径 = %q, want %q", got, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "新版技能内容" {
		t.Fatalf("同名旧文件应被覆盖, got %q", b)
	}
}
