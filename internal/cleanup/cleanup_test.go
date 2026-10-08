package cleanup

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mockit/internal/store"
)

// day 一天的秒数(与 store 测试同款常量)。
const day = 86400

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCreate(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.CreateSubmission(id, "标题 "+id); err != nil {
		t.Fatalf("CreateSubmission(%s): %v", id, err)
	}
}

func mustAddVariant(t *testing.T, s *store.Store, subID string, seq int) {
	t.Helper()
	if err := s.AddVariant(subID, seq, "方案", store.KindHTML); err != nil {
		t.Fatalf("AddVariant(%s,%d): %v", subID, seq, err)
	}
}

// makePageFiles 在 <dataDir>/{id}/ 下铺一套页面文件(v1/index.html + note.txt),
// 模拟提交管道的落盘布局,供清理删除。
func makePageFiles(t *testing.T, dataDir, id string) {
	t.Helper()
	dir := filepath.Join(dataDir, id, "v1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("铺页面文件 %s: %v", dir, err)
	}
	for _, f := range []string{filepath.Join(dir, "index.html"), filepath.Join(dataDir, id, "note.txt")} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("写 %s: %v", f, err)
		}
	}
}

func dirExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("Stat %s: %v", path, err)
	return false
}

func mustGet(t *testing.T, s *store.Store, id string) *store.Submission {
	t.Helper()
	sub, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return sub
}

func isGone(t *testing.T, s *store.Store, id string) bool {
	t.Helper()
	_, err := s.Get(id)
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		return true
	default:
		t.Fatalf("Get(%s): %v", id, err)
		return false
	}
}

// discardLogger 丢日志的 logger(清理循环的可选依赖)。
func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// waitFor 轮询直到 cond 为真,超时 Fatal(给后台 goroutine 留调度余量)。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("条件在超时前未满足")
}

// futureClock 返回"从 base 起跳到 days 天后"的固定时钟注入:
// 数据由 store 用真实当前时间写入(created_at≈base),清理视角下即已超期 days 天,
// 从而不用白盒改库就能构造"老数据"。
func futureClock(base time.Time, days int) func() time.Time {
	return func() time.Time { return base.Add(time.Duration(days) * 24 * time.Hour) }
}

func TestRunOncePendingExpiredWholeDelete(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()
	mustCreate(t, s, "oldpend")
	mustAddVariant(t, s, "oldpend", 1)
	makePageFiles(t, dataDir, "oldpend")

	base := time.Now()
	cleaned := runOnce(s, dataDir, 14, 90, base.Add(30*24*time.Hour), discardLogger())

	if cleaned != 1 {
		t.Errorf("清理条数 = %d,想 1", cleaned)
	}
	if dirExists(t, filepath.Join(dataDir, "oldpend")) {
		t.Error("未审超期应整条删:目录仍在")
	}
	if !isGone(t, s, "oldpend") {
		t.Error("未审超期应整条删:记录仍在")
	}
}

func TestRunOnceReviewedExpiredFilesOnly(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()
	mustCreate(t, s, "oldrev")
	mustAddVariant(t, s, "oldrev", 1)
	makePageFiles(t, dataDir, "oldrev")
	if err := s.SaveReview("oldrev", store.DecisionApprove, 0, ""); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	base := time.Now()
	cleaned := runOnce(s, dataDir, 14, 90, base.Add(30*24*time.Hour), discardLogger())

	if cleaned != 1 {
		t.Errorf("清理条数 = %d,想 1", cleaned)
	}
	if dirExists(t, filepath.Join(dataDir, "oldrev")) {
		t.Error("已审超期应删页面文件:目录仍在")
	}
	sub := mustGet(t, s, "oldrev")
	if sub.Status != store.StatusReviewed {
		t.Errorf("记录应留档,status = %q,想 reviewed", sub.Status)
	}
	if !sub.FilesDeleted {
		t.Error("记录应留档且 files_deleted=1,得到 false")
	}
}

func TestRunOnceRecordExpiredAfterDecisionDays(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()
	mustCreate(t, s, "oldrec")
	mustAddVariant(t, s, "oldrec", 1)
	if err := s.SaveReview("oldrec", store.DecisionApprove, 0, ""); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	// 文件已在更早一轮按 14 天保留期清掉,此处只考 90 天记录到期
	if err := s.MarkFilesDeleted("oldrec"); err != nil {
		t.Fatalf("MarkFilesDeleted: %v", err)
	}

	base := time.Now()
	cleaned := runOnce(s, dataDir, 14, 90, base.Add(91*24*time.Hour), discardLogger())

	if cleaned != 1 {
		t.Errorf("清理条数 = %d,想 1", cleaned)
	}
	if !isGone(t, s, "oldrec") {
		t.Error("reviewed_at 超 90 天,记录应删")
	}
}

func TestRunOncePinnedExempt(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()

	// 三种"若不钉住都会被清"的形态,钉住后应全不动
	mustCreate(t, s, "pinpend") // 未审超期 → 本应整条删
	mustAddVariant(t, s, "pinpend", 1)
	makePageFiles(t, dataDir, "pinpend")
	if err := s.SetPinned("pinpend", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}

	mustCreate(t, s, "pinrev") // 已审超期 → 本应删文件
	mustAddVariant(t, s, "pinrev", 1)
	makePageFiles(t, dataDir, "pinrev")
	if err := s.SaveReview("pinrev", store.DecisionApprove, 0, ""); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	if err := s.SetPinned("pinrev", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}

	mustCreate(t, s, "pinrec") // reviewed_at 超 90 天 → 本应删记录
	if err := s.SaveReview("pinrec", store.DecisionApprove, 0, ""); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	if err := s.SetPinned("pinrec", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}

	base := time.Now()
	cleaned := runOnce(s, dataDir, 14, 90, base.Add(120*24*time.Hour), discardLogger())

	if cleaned != 0 {
		t.Errorf("钉住应全豁免,清理条数 = %d,想 0", cleaned)
	}
	for _, id := range []string{"pinpend", "pinrev", "pinrec"} {
		if !isGone(t, s, id) {
			continue // isGone=false 才是期望,直接取反断言
		}
		t.Errorf("钉住的 %s 记录不应被删", id)
	}
	if !dirExists(t, filepath.Join(dataDir, "pinpend")) || !dirExists(t, filepath.Join(dataDir, "pinrev")) {
		t.Error("钉住的目录不应被删")
	}
	if sub := mustGet(t, s, "pinrev"); sub.FilesDeleted {
		t.Error("钉住的 pinrev 不应被标 files_deleted")
	}
}

func TestRunOnceEmptyStoreNoop(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()
	if cleaned := runOnce(s, dataDir, 14, 90, time.Now(), discardLogger()); cleaned != 0 {
		t.Errorf("空库清理条数 = %d,想 0", cleaned)
	}
}

func TestStartScansImmediately(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()
	mustCreate(t, s, "due001")
	makePageFiles(t, dataDir, "due001")

	base := time.Now()
	// 间隔拉到 1 小时:若启动不立即扫,首扫要等 1 小时,2 秒超时内必失败 → 恰好证明"启动即扫"
	stop := Start(s, dataDir, 14, 90, discardLogger(), futureClock(base, 30), time.Hour)
	defer stop()

	waitFor(t, 2*time.Second, func() bool { return isGone(t, s, "due001") })
}

func TestStartRescansOnInterval(t *testing.T) {
	s := testStore(t)
	dataDir := t.TempDir()
	base := time.Now()
	stop := Start(s, dataDir, 14, 90, discardLogger(), futureClock(base, 30), 25*time.Millisecond)
	defer stop()

	// 先吃掉首扫
	mustCreate(t, s, "early01")
	makePageFiles(t, dataDir, "early01")
	waitFor(t, 2*time.Second, func() bool { return isGone(t, s, "early01") })

	// 首扫已过,late01 只能被后续 ticker 轮扫到
	mustCreate(t, s, "late01")
	makePageFiles(t, dataDir, "late01")
	waitFor(t, 2*time.Second, func() bool { return isGone(t, s, "late01") })
}

func TestStartStop(t *testing.T) {
	s := testStore(t)
	base := time.Now()
	stop := Start(s, t.TempDir(), 14, 90, discardLogger(), futureClock(base, 30), time.Hour)
	stop()  // 阻塞至循环退出
	stop() // 二次调用必须安全不 panic
}
