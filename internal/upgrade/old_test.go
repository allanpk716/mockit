package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

// RemoveOldBinary 测试缝:可执行文件路径注入(临时目录)。
// 覆盖票 #12 的启动清理语义:删得掉就删,不存在/删不掉都静默跳过、不返回错误。

func TestRemoveOldBinaryDeletesResidue(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mockit.exe") // 同名 .old 残留
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := exe + ".old"
	if err := os.WriteFile(old, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	RemoveOldBinary(exe)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf(".old 残留应被删除, err=%v", err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("当前二进制不得被波及: %v", err)
	}
}

func TestRemoveOldBinaryNoResidueIsNoop(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mockit")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	RemoveOldBinary(exe) // 无 .old:静默无事发生

	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("当前二进制不得被波及: %v", err)
	}
}

func TestRemoveOldBinaryUndeletableSilent(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mockit.exe")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 非空目录在 Windows/Unix 上 os.Remove 都删不掉,模拟"旧进程占用删不动"
	old := exe + ".old"
	if err := os.MkdirAll(filepath.Join(old, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}

	RemoveOldBinary(exe) // 不得 panic、不得影响调用方

	if _, err := os.Stat(old); err != nil {
		t.Fatalf("删不掉的 .old 应原样保留(静默跳过): %v", err)
	}
}

// 启动清理连带识别备用名 .old.N(审查 S2:.old 被占用时旧文件改用的落位)。
func TestRemoveOldBinaryCleansAlternateNames(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mockit.exe")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	alt1, alt2 := exe+".old.1", exe+".old.2"
	for _, p := range []string{alt1, alt2} {
		if err := os.WriteFile(p, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	RemoveOldBinary(exe)

	for _, p := range []string{alt1, alt2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("备用名残留 %s 应被删除, err=%v", p, err)
		}
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("当前二进制不得被波及: %v", err)
	}

	t.Run("占用中的 .old 不影响清理备用名", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "mockit.exe")
		if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
			t.Fatal(err)
		}
		old := exe + ".old"
		if err := os.MkdirAll(filepath.Join(old, "locked"), 0o755); err != nil {
			t.Fatal(err)
		}
		alt1 := exe + ".old.1"
		if err := os.WriteFile(alt1, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}

		RemoveOldBinary(exe)

		if _, err := os.Stat(old); err != nil {
			t.Fatalf("删不掉的 .old 应原样保留: %v", err)
		}
		if _, err := os.Stat(alt1); !os.IsNotExist(err) {
			t.Fatalf(".old.1 应被删除: %v", err)
		}
	})
}
