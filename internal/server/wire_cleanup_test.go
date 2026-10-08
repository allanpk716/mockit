package server

import (
	"database/sql"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mockit/internal/config"
	"mockit/internal/store"

	_ "modernc.org/sqlite" // 直连 mockit.db 回改 created_at,构造"老数据"
)

// TestStartCleanupScansAndStops 端到端验证接线函数:
// 到期数据在 startCleanup 启动后被首扫清掉(保留期配置被真实传入),
// 且 stop 返回即循环已退。
func TestStartCleanupScansAndStops(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	if err := st.CreateSubmission("oldwire", "待清接线", ""); err != nil {
		t.Fatalf("CreateSubmission: %v", err)
	}
	subDir := filepath.Join(dataDir, "oldwire", "v1")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("铺目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatalf("铺文件: %v", err)
	}
	// 回改 created_at 至 30 天前(store 无注入口,经第二条连接直改库)
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "mockit.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	if _, err := db.Exec(`UPDATE submissions SET created_at = ? WHERE id = ?`,
		time.Now().Add(-30*24*time.Hour).Unix(), "oldwire"); err != nil {
		t.Fatalf("回改 created_at: %v", err)
	}
	_ = db.Close()

	cfg := &config.Config{
		DataDir:            dataDir,
		PageRetentionDays:  14,
		DecisionRetentionD: 90,
	}
	lg := log.New(io.Discard, "", 0)

	stop := startCleanup(cfg, st, lg)

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := st.Get("oldwire")
		if errors.Is(err, store.ErrNotFound) {
			break // 已被清理
		}
		if err != nil {
			t.Fatalf("Get(oldwire): %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("未审超期数据未被启动首扫清理")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "oldwire")); !os.IsNotExist(err) {
		t.Error("目录应随整条删一并清掉")
	}

	stop() // 返回即循环已退,不挂死即通过
}
