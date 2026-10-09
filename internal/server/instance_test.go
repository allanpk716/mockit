package server

// 票 08 启动互斥:serve 启动序列的实例锁场景测试。
//
// 模拟"另一实例持锁"用 lifecycle.AcquireInstanceLock 在本进程内持真 OS 锁
// (锁冲突按句柄计,同进程内即成立);"写锁失败"用注入桩确定性触发。

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

// freePort 拿一个当前空闲端口并立刻释放(造"死端口"用)。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占空闲端口失败: %v", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// findDeadPID 返回一个 PIDAlive 判定为死的 pid。
func findDeadPID(t *testing.T) int {
	t.Helper()
	for pid := 1_000_000; pid < 1_001_000; pid++ {
		if !lifecycle.PIDAlive(pid) {
			return pid
		}
	}
	t.Fatal("1_000_000 起找不到死 pid(异常环境)")
	return 0
}

// serveCode 在 goroutine 里起 Serve 并回收退出码。
func serveCode(cfg *config.Config) <-chan int {
	ch := make(chan int, 1)
	go func() { ch <- Serve(cfg) }()
	return ch
}

func waitCode(t *testing.T, ch <-chan int, timeout time.Duration) int {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(timeout):
		t.Fatal("Serve 未在期限内退出")
		return -1
	}
}

// readServeLog 读取 serve.log 全文。
func readServeLog(t *testing.T, dataDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "logs", "serve.log"))
	if err != nil {
		t.Fatalf("读 serve.log 失败: %v", err)
	}
	return string(b)
}

// 已有活实例(ping 通)时,第二个 serve 应打印"已有实例"后干净退出,
// 不干扰第一实例。
func TestServeDuplicateExitsWhenInstanceAlive(t *testing.T) {
	dataDir := t.TempDir()
	cfg := &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
	first := serveCode(cfg)
	lk := waitLock(t, dataDir, 10*time.Second)
	defer func() {
		if code := shutdownReq(t, lk.Port, lk.Token); code != http.StatusOK {
			t.Fatalf("收尾 shutdown 失败: %d", code)
		}
		if c := waitCode(t, first, 10*time.Second); c != 0 {
			t.Fatalf("第一实例应优雅退出, got %d", c)
		}
	}()

	second := serveCode(cfg)
	if c := waitCode(t, second, 10*time.Second); c != 1 {
		t.Fatalf("重复实例应退出码 1, got %d", c)
	}
	if logText := readServeLog(t, dataDir); !strings.Contains(logText, "已有实例") {
		t.Fatalf("日志应打印已有实例:\n%s", logText)
	}
	// 第一实例仍在服务
	resp, err := localClient().Get(fmt.Sprintf("http://127.0.0.1:%d/ping", lk.Port))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("第一实例应存活: %v", err)
	} else {
		resp.Body.Close()
	}
}

// waitNewLock 轮询等待 token 不等于 oldToken 的新 lock(接管场景旧文件先在)。
func waitNewLock(t *testing.T, dataDir, oldToken string, timeout time.Duration) *lifecycle.Lock {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		lk, err := lifecycle.ReadLock(dataDir)
		if err == nil && lk.Token != oldToken {
			return lk
		}
		if time.Now().After(deadline) {
			t.Fatalf("新 lock 未在 %v 内写入: %v", timeout, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// 残骸(死 PID+死端口+无人持锁)应被清理接管。
func TestServeTakesOverDeadResidue(t *testing.T) {
	dataDir := t.TempDir()
	residue := fmt.Sprintf(`{"port":%d,"pid":%d,"version":"0.0.1","token":"dead","base_host":"","started_at":1}`,
		freePort(t), findDeadPID(t))
	if err := os.WriteFile(lifecycle.LockPath(dataDir), []byte(residue), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
	ch := serveCode(cfg)
	lk := waitNewLock(t, dataDir, "dead", 10*time.Second)
	if lk.Version != Version {
		t.Fatalf("lock 应被新实例覆盖: %+v", lk)
	}
	if code := shutdownReq(t, lk.Port, lk.Token); code != http.StatusOK {
		t.Fatalf("shutdown 失败: %d", code)
	}
	if c := waitCode(t, ch, 10*time.Second); c != 0 {
		t.Fatalf("接管实例应优雅退出, got %d", c)
	}
}

// 活持有(OS 锁被持)但 ping 不通:退避复查后退出,不清理锁文件。
func TestServeLiveHoldPingDeadExitsNoCleanup(t *testing.T) {
	oldBackoff := instanceCheckBackoff
	instanceCheckBackoff = 20 * time.Millisecond
	t.Cleanup(func() { instanceCheckBackoff = oldBackoff })

	dataDir := t.TempDir()
	g, err := lifecycle.AcquireInstanceLock(dataDir, &lifecycle.Lock{
		Port: freePort(t), PID: findDeadPID(t), Version: "v", Token: "t",
	})
	if err != nil {
		t.Fatalf("造活持有夹具失败: %v", err)
	}
	defer g.Release()

	cfg := &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
	ch := serveCode(cfg)
	if c := waitCode(t, ch, 10*time.Second); c != 1 {
		t.Fatalf("活持有 ping 不通应退出码 1, got %d", c)
	}
	// 不清理:锁文件仍在且仍可读出夹具记录
	lk, err := lifecycle.ReadLock(dataDir)
	if err != nil || lk.Token != "t" {
		t.Fatalf("锁文件不应被清理: %+v err=%v", lk, err)
	}
	if logText := readServeLog(t, dataDir); !strings.Contains(logText, "ping 不通") {
		t.Fatalf("日志应说明 ping 不通:\n%s", logText)
	}
}

// 绑定成功后写锁失败:关监听退出非零,不留孤儿端口。
func TestServeAcquireFailAfterBindClosesListener(t *testing.T) {
	dataDir := t.TempDir()
	var boundPort int
	old := acquireInstanceLockFn
	acquireInstanceLockFn = func(dir string, rec *lifecycle.Lock) (*lifecycle.InstanceGuard, error) {
		boundPort = rec.Port
		return nil, errors.New("注入的写锁失败")
	}
	t.Cleanup(func() { acquireInstanceLockFn = old })

	cfg := &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
	if c := waitCode(t, serveCode(cfg), 15*time.Second); c != 1 {
		t.Fatalf("写锁失败应退出码 1, got %d", c)
	}
	// 监听已关:该端口连接应被拒
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", boundPort))
	if err != nil {
		t.Fatalf("绑定端口应已释放(监听关闭),却仍被占: %v", err)
	}
	ln.Close()
	// 不留 lock 文件
	if _, err := os.Stat(lifecycle.LockPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("写锁失败不应留下 server.lock: %v", err)
	}
	if logText := readServeLog(t, dataDir); !strings.Contains(logText, "写锁失败") && !strings.Contains(logText, "实例锁") {
		t.Fatalf("日志应说明实例锁失败:\n%s", logText)
	}
}

// O_EXCL 冲突(建锁瞬间他人已建):复查为活持有→输家退出并打印已有实例。
// 用注入桩模拟竞态:checkInstance 时无锁,serve 调建锁瞬间"赢家"才出现
// (真 ping 通),复查即见活实例。
func TestServeOExclLoserExitsAfterRecheck(t *testing.T) {
	// 赢家:真 /ping 服务 + 真 OS 锁
	winnerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.1.0"}`))
	}))
	defer winnerSrv.Close()
	winnerPort := winnerSrv.Listener.Addr().(*net.TCPAddr).Port

	dataDir := t.TempDir()
	var winner *lifecycle.InstanceGuard
	old := acquireInstanceLockFn
	acquireInstanceLockFn = func(dir string, rec *lifecycle.Lock) (*lifecycle.InstanceGuard, error) {
		if winner == nil {
			g, err := lifecycle.AcquireInstanceLock(dir, &lifecycle.Lock{
				Port: winnerPort, PID: os.Getpid(), Version: Version, Token: "winner",
			})
			if err != nil {
				return nil, err
			}
			winner = g
		}
		return nil, fmt.Errorf("lifecycle: %s: %w", lifecycle.LockPath(dir), lifecycle.ErrLockExists)
	}
	t.Cleanup(func() {
		acquireInstanceLockFn = old
		if winner != nil {
			winner.Release()
		}
	})

	cfg := &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
	if c := waitCode(t, serveCode(cfg), 15*time.Second); c != 1 {
		t.Fatalf("输家应退出码 1, got %d", c)
	}
	if logText := readServeLog(t, dataDir); !strings.Contains(logText, "已有实例") {
		t.Fatalf("输家日志应打印已有实例:\n%s", logText)
	}
	// 赢家记录未被破坏
	lk, err := lifecycle.ReadLock(dataDir)
	if err != nil || lk.Token != "winner" {
		t.Fatalf("赢家记录不应被破坏: %+v err=%v", lk, err)
	}
}

// O_EXCL 冲突但复查发现锁已消失(建了又删):清理重试一次后正常启动。
func TestServeOExclConflictRetriesAfterVanish(t *testing.T) {
	dataDir := t.TempDir()
	real := lifecycle.AcquireInstanceLock
	calls := 0
	old := acquireInstanceLockFn
	acquireInstanceLockFn = func(dir string, rec *lifecycle.Lock) (*lifecycle.InstanceGuard, error) {
		calls++
		if calls == 1 {
			// 第一击:造一个真实的"已存在"冲突,随即让对端死亡释放(文件被删)
			g, err := real(dir, &lifecycle.Lock{Port: 1, PID: os.Getpid(), Version: "x", Token: "ghost"})
			if err != nil {
				return nil, err
			}
			g.Release() // 文件移除
			return nil, fmt.Errorf("lifecycle: %s: %w", lifecycle.LockPath(dir), lifecycle.ErrLockExists)
		}
		return real(dir, rec)
	}
	t.Cleanup(func() { acquireInstanceLockFn = old })

	cfg := &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: dataDir,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
	ch := serveCode(cfg)
	lk := waitLock(t, dataDir, 10*time.Second)
	if lk.Token == "ghost" {
		t.Fatal("应重试建新锁而非沿用幽灵记录")
	}
	if code := shutdownReq(t, lk.Port, lk.Token); code != http.StatusOK {
		t.Fatalf("shutdown 失败: %d", code)
	}
	if c := waitCode(t, ch, 10*time.Second); c != 0 {
		t.Fatalf("重试成功后应正常服务, got %d", c)
	}
}
