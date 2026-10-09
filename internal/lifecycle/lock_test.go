package lifecycle

// 锁分离协议测试:实例占有锁 O_EXCL+OS 锁持有、start.lock 互斥、
// Probe 活持有/残骸判定、PID 探测。模拟"他人持锁"一律用本进程内
// 独立 open 的句柄(flock/LockFileEx 都按 open file description/句柄
// 计,同进程内两个句柄互相冲突,无需再拉子进程)。

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// findDeadPID 返回一个 PIDAlive 判定为死的 pid(高位 pid 空间几乎必然空闲)。
func findDeadPID(t *testing.T) int {
	t.Helper()
	for pid := 1_000_000; pid < 1_001_000; pid++ {
		if !PIDAlive(pid) {
			return pid
		}
	}
	t.Fatal("1_000_000 起找不到死 pid(异常环境)")
	return 0
}

func TestPidAliveSelf(t *testing.T) {
	if !PIDAlive(os.Getpid()) {
		t.Fatal("本进程 pid 应判活")
	}
}

func TestPidAliveDead(t *testing.T) {
	if PIDAlive(findDeadPID(t)) {
		t.Fatal("空闲高位 pid 应判死")
	}
	if PIDAlive(0) || PIDAlive(-1) {
		t.Fatal("非正 pid 应判死")
	}
}

func TestAcquireInstanceLockSecondFails(t *testing.T) {
	dir := t.TempDir()
	g, err := AcquireInstanceLock(dir, &Lock{Port: 1, PID: os.Getpid(), Version: "v", Token: "t", StartedAt: 2})
	if err != nil {
		t.Fatalf("首次获取实例锁失败: %v", err)
	}
	defer g.Release()
	_, err = AcquireInstanceLock(dir, &Lock{Port: 2, PID: 9, Version: "v2", Token: "t2"})
	if !errors.Is(err, ErrLockExists) {
		t.Fatalf("二次获取应报 ErrLockExists, got: %v", err)
	}
	// 输家尝试不得破坏赢家的记录
	lk, err := ReadLock(dir)
	if err != nil {
		t.Fatalf("ReadLock 失败: %v", err)
	}
	if lk.Port != 1 || lk.Token != "t" {
		t.Fatalf("赢家记录被破坏: %+v", lk)
	}
}

func TestInstanceLockWritesCompleteRecord(t *testing.T) {
	dir := t.TempDir()
	in := &Lock{Port: 9000, PID: os.Getpid(), Version: "0.1.0", Token: "tok", BaseHost: "", StartedAt: 1728000000}
	g, err := AcquireInstanceLock(dir, in)
	if err != nil {
		t.Fatalf("获取实例锁失败: %v", err)
	}
	defer g.Release()
	// 文件内容是写全的完整 JSON(半写防护由 Acquire 先建后写保证)
	b, err := os.ReadFile(LockPath(dir))
	if err != nil {
		t.Fatalf("读 lock 失败: %v", err)
	}
	if !strings.Contains(string(b), `"port":9000`) || !strings.Contains(string(b), `"started_at":1728000000`) {
		t.Fatalf("lock 记录不完整: %s", b)
	}
}

func TestInstanceGuardHoldsOSLock(t *testing.T) {
	dir := t.TempDir()
	g, err := AcquireInstanceLock(dir, &Lock{Port: freePort(t), PID: os.Getpid(), Version: "v", Token: "t"})
	if err != nil {
		t.Fatalf("获取实例锁失败: %v", err)
	}

	p, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("ProbeLock 失败: %v", err)
	}
	if !p.Exists || p.Holdable {
		t.Fatalf("持有中探测应 Holdable=false: %+v", p)
	}
	if !p.LiveHeld() {
		t.Fatalf("持有中应 LiveHeld: %+v", p)
	}
	if p.DeadResidue() {
		t.Fatalf("持有中不得判残骸: %+v", p)
	}

	g.Release()
	p2, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("Release 后 ProbeLock 失败: %v", err)
	}
	if p2.Exists {
		t.Fatalf("Release 应移除 lock 文件: %+v", p2)
	}
}

func TestProbeMissingLock(t *testing.T) {
	p, err := ProbeLock(t.TempDir())
	if err != nil {
		t.Fatalf("无 lock 文件 ProbeLock 不应报错: %v", err)
	}
	if p.Exists || p.LiveHeld() || p.DeadResidue() {
		t.Fatalf("无 lock 应全 false: %+v", p)
	}
}

func TestProbeDeadResidueCleanable(t *testing.T) {
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: freePort(t), PID: findDeadPID(t), Version: "v", Token: "t"})
	p, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("ProbeLock 失败: %v", err)
	}
	if !p.Exists || !p.Holdable || p.PIDAlive || p.Alive {
		t.Fatalf("死 pid+死端口+无持有应可清: %+v", p)
	}
	if !p.DeadResidue() || p.LiveHeld() {
		t.Fatalf("应判无持有者残留: %+v", p)
	}
	if err := CleanResidue(dir); err != nil {
		t.Fatalf("CleanResidue 失败: %v", err)
	}
	if _, err := os.Stat(LockPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("残骸应被移除: %v", err)
	}
}

// 锁半写且仍被(将死的)创建者持有 OS 锁:必须按活持有,不得清理。
func TestProbeHalfWrittenLiveHeldNotCleaned(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(LockPath(dir), []byte(`{"port":12`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 本进程内独立句柄模拟"创建者仍持 OS 锁"
	f, err := os.OpenFile(LockPath(dir), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	held, err := tryLockFileNB(f)
	if err != nil || held {
		t.Fatalf("夹具应成功取锁: held=%v err=%v", held, err)
	}

	p, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("ProbeLock 失败: %v", err)
	}
	if p.Rec != nil {
		t.Fatalf("半写 JSON 不应解析出记录: %+v", p.Rec)
	}
	if !p.LiveHeld() {
		t.Fatalf("OS 锁被持应判活持有(即使 PID 不可知): %+v", p)
	}
	if p.DeadResidue() {
		t.Fatalf("活持有不得判残骸: %+v", p)
	}

	f.Close() // 模拟创建者死亡=OS 锁释放
	p2, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("释放后 ProbeLock 失败: %v", err)
	}
	if !p2.DeadResidue() {
		t.Fatalf("锁释放+坏 JSON+无端口应判残骸(半写接管): %+v", p2)
	}
}

// 活 PID 判据独立成立:文件可锁(OS 无持有)但记录 PID 活 → 仍按活持有。
func TestProbeLivePIDAloneHolds(t *testing.T) {
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: freePort(t), PID: os.Getpid(), Version: "v", Token: "t"})
	p, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("ProbeLock 失败: %v", err)
	}
	if !p.Holdable {
		t.Fatalf("夹具无人持 OS 锁,应 Holdable=true: %+v", p)
	}
	if !p.LiveHeld() || p.DeadResidue() {
		t.Fatalf("PID 活即按活持有(保守优先): %+v", p)
	}
}

func TestStartLockMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	g, err := AcquireStartLock(dir, 2*time.Second)
	if err != nil {
		t.Fatalf("首次获取 start.lock 失败: %v", err)
	}
	defer g.Release()

	_, err = AcquireStartLock(dir, 300*time.Millisecond)
	if err == nil {
		t.Fatal("他人持有时应超时报错")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应说明超时: %v", err)
	}

	g.Release()
	g2, err := AcquireStartLock(dir, 2*time.Second)
	if err != nil {
		t.Fatalf("释放后应可再取: %v", err)
	}
	g2.Release()
}

// start.lock 文件常驻(空文件作锁载体,Release 只释放锁不删文件)。
func TestStartLockFilePersistsAfterRelease(t *testing.T) {
	dir := t.TempDir()
	g, err := AcquireStartLock(dir, 2*time.Second)
	if err != nil {
		t.Fatalf("获取 start.lock 失败: %v", err)
	}
	g.Release()
	if _, err := os.Stat(StartLockPath(dir)); err != nil {
		t.Fatalf("start.lock 文件应常驻: %v", err)
	}
}

// ProbeLock 对活实例(真 HTTP /ping)的 Alive/Version 探测。
func TestProbeAliveInstance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"7.7.7"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeLockJSON(t, dir, &Lock{Port: srv.Listener.Addr().(*net.TCPAddr).Port, PID: findDeadPID(t), Version: "7.7.7", Token: "t"})
	p, err := ProbeLock(dir)
	if err != nil {
		t.Fatalf("ProbeLock 失败: %v", err)
	}
	if !p.Alive || p.Version != "7.7.7" {
		t.Fatalf("活实例探测: %+v", p)
	}
	if p.LiveHeld() || p.DeadResidue() {
		t.Fatalf("无人持有+ping 通:既非活持有也非残骸: %+v", p)
	}
}
