package mcp

// ensure-server 测试(锁分离协议 D17/票 08):
// 复用/冷启动/双并发单实例/崩溃报错/F9/版本换新/停旧失败不清理。
// 假 serve 一律用 httptest /ping + 手写 lock 文件;"另一进程持锁"用
// lifecycle.AcquireInstanceLock 在本进程内持真 OS 锁。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

// findDeadPID 返回一个 lifecycle.PIDAlive 判定为死的 pid。
func findDeadPID(t *testing.T) int {
	t.Helper()
	for pid := 1_000_000; pid < 1_001_000; pid++ {
		if !lifecycle.PIDAlive(pid) {
			return pid
		}
	}
	t.Fatal("找不到死 pid")
	return 0
}

// freePort 拿一个空闲端口立刻释放。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

// pingServer 起一个回固定版本的 /ping 假 serve,返回端口。
func pingServer(t *testing.T, version string) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version":%q}`, version)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

// writeLock 写 server.lock JSON 夹具。
func writeLock(t *testing.T, dir string, l *lifecycle.Lock) {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lifecycle.LockPath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// aliveChild 造一个"进程活着"的假子进程句柄;stop 后视为退出。
type aliveChild struct {
	done chan error
}

func newAliveChild() *aliveChild       { return &aliveChild{done: make(chan error, 1)} }
func (c *aliveChild) proc() *childProc { return &childProc{done: c.done} }
func (c *aliveChild) exit()            { close(c.done) }

// spawnNever 造一个绝不应被调到的 spawn 桩。
func spawnNever(t *testing.T) spawnFunc {
	t.Helper()
	return func() (*childProc, error) {
		t.Error("不应发生 spawn")
		return nil, errors.New("不应 spawn")
	}
}

// shortenEnsureTimeouts 把超时常量缩到测试量级,测毕恢复。
func shortenEnsureTimeouts(t *testing.T) {
	t.Helper()
	oldReady, oldWait, oldPoll := ensureReadyTimeout, ensureShutdownWait, ensurePollInterval
	ensureReadyTimeout = 400 * time.Millisecond
	ensureShutdownWait = 400 * time.Millisecond
	ensurePollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		ensureReadyTimeout, ensureShutdownWait, ensurePollInterval = oldReady, oldWait, oldPoll
	})
}

// assertNoURL 断言错误文本无内部 URL 泄漏。
func assertNoURL(t *testing.T, context string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if s := err.Error(); strings.Contains(s, "http") || strings.Contains(s, "://") {
		t.Fatalf("%s 错误文本泄漏 URL:\n%s", context, s)
	}
}

func TestEnsureReusesLiveSameVersion(t *testing.T) {
	port := pingServer(t, Version)
	dir := t.TempDir()
	writeLock(t, dir, &lifecycle.Lock{Port: port, PID: os.Getpid(), Version: Version, Token: "t"})

	lk, err := ensureServer(&config.Config{DataDir: dir}, spawnNever(t))
	if err != nil {
		t.Fatalf("同版本活实例应复用: %v", err)
	}
	if lk.Port != port {
		t.Fatalf("复用端口 = %d, want %d", lk.Port, port)
	}
}

func TestEnsureColdStartSpawnsAndWaitsReady(t *testing.T) {
	shortenEnsureTimeouts(t)
	port := pingServer(t, Version)
	dir := t.TempDir()
	child := newAliveChild()
	spawnCalls := 0
	spawn := func() (*childProc, error) {
		spawnCalls++
		// 假 serve:写 lock(活 pid)后进入服务
		writeLock(t, dir, &lifecycle.Lock{Port: port, PID: os.Getpid(), Version: Version, Token: "tok"})
		return child.proc(), nil
	}

	lk, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err != nil {
		t.Fatalf("冷启动应成功: %v", err)
	}
	if lk.Port != port || spawnCalls != 1 {
		t.Fatalf("结果 = %+v spawnCalls=%d", lk, spawnCalls)
	}
	// ensure 返回时 start.lock 必须已释放(临界区结束的硬保证)
	g, err := lifecycle.AcquireStartLock(dir, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ensure 返回后 start.lock 应已释放: %v", err)
	}
	g.Release()
	child.exit()
}

// 验收场景 1:双 MCP 同时冷启动,只产生一个 serve;后来者在 start.lock
// 排队,拿到后复查即复用。
func TestEnsureDoubleColdStartSingleSpawn(t *testing.T) {
	shortenEnsureTimeouts(t)
	port := pingServer(t, Version)
	dir := t.TempDir()

	var calls int32
	var mu sync.Mutex // 串行化桩内的 lock 写入
	spawn := func() (*childProc, error) {
		if atomic.AddInt32(&calls, 1) > 1 {
			return nil, errors.New("第二个冷启动不应再 spawn")
		}
		mu.Lock()
		defer mu.Unlock()
		writeLock(t, dir, &lifecycle.Lock{Port: port, PID: os.Getpid(), Version: Version, Token: "tok"})
		return newAliveChild().proc(), nil
	}

	cfg := &config.Config{DataDir: dir}
	var wg sync.WaitGroup
	ports := make(chan int, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lk, err := ensureServer(cfg, spawn)
			if err != nil {
				errs <- err
				return
			}
			ports <- lk.Port
		}()
	}
	wg.Wait()
	close(errs)
	close(ports)
	for err := range errs {
		t.Fatalf("并发冷启动失败: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("spawn 次数 = %d, want 1", n)
	}
	for p := range ports {
		if p != port {
			t.Fatalf("端口 = %d, want %d", p, port)
		}
	}
}

// 验收场景 2:spawn 后写锁前死 → 干净报错,无残骸。
func TestEnsureCrashBeforeLockCleanError(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	spawn := func() (*childProc, error) {
		c := newAliveChild()
		c.exit() // spawn 即死,未写任何 lock
		return c.proc(), nil
	}
	_, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err == nil {
		t.Fatal("被拉进程死应报错")
	}
	if !strings.Contains(err.Error(), "启动中退出") {
		t.Fatalf("错误应说明启动中退出: %v", err)
	}
	assertNoURL(t, "崩溃报错", err)
	if _, statErr := os.Stat(lifecycle.LockPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("不应留下 server.lock: %v", statErr)
	}
}

// 崩溃变体:半写 lock(建了文件没写全)后死 → 错误路上把残骸清掉。
func TestEnsureCrashAfterHalfWriteCleansResidue(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	spawn := func() (*childProc, error) {
		if err := os.WriteFile(lifecycle.LockPath(dir), []byte(`{"port":12`), 0o644); err != nil {
			return nil, err
		}
		c := newAliveChild()
		c.exit()
		return c.proc(), nil
	}
	_, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err == nil || !strings.Contains(err.Error(), "启动中退出") {
		t.Fatalf("应报启动中退出: %v", err)
	}
	if _, statErr := os.Stat(lifecycle.LockPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("半写残骸应被清掉: %v", statErr)
	}
}

// 验收 F9:拉起的 serve 活着但一直不就绪 → 报错提示人工处置。
func TestEnsureReadyTimeoutAliveF9(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	child := newAliveChild()
	defer child.exit()
	spawn := func() (*childProc, error) { return child.proc(), nil }

	_, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err == nil {
		t.Fatal("就绪超时应报错")
	}
	if !strings.Contains(err.Error(), "人工处置") {
		t.Fatalf("错误应提示人工处置: %v", err)
	}
	assertNoURL(t, "F9 报错", err)
}

// F9 初始态:锁活持有(PID 活)但 ping 不通 → 报人工处置,不 spawn 不清理。
func TestEnsureInitialLiveNoPingF9(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	writeLock(t, dir, &lifecycle.Lock{Port: freePort(t), PID: os.Getpid(), Version: Version, Token: "t"})

	_, err := ensureServer(&config.Config{DataDir: dir}, spawnNever(t))
	if err == nil || !strings.Contains(err.Error(), "人工处置") {
		t.Fatalf("活持有 ping 不通应报人工处置: %v", err)
	}
	assertNoURL(t, "F9 报错", err)
	// 锁文件不被清理
	if _, statErr := os.Stat(lifecycle.LockPath(dir)); statErr != nil {
		t.Fatalf("锁文件不应被清理: %v", statErr)
	}
}

// 验收:版本不符 → 带token停旧 → 等确认退出 → 清残 → 拉新。
func TestEnsureVersionMismatchStopsOldStartsNew(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()

	var gotToken string
	var shutdownCalls int
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ping":
			fmt.Fprint(w, `{"version":"0.0.9-old"}`)
		case "/shutdown":
			shutdownCalls++
			gotToken = r.Header.Get("X-Mockit-Token")
			// 真实行为:优雅退出=移除锁文件并停服务
			os.Remove(lifecycle.LockPath(dir))
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oldSrv.Close()
	oldPort := oldSrv.Listener.Addr().(*net.TCPAddr).Port
	writeLock(t, dir, &lifecycle.Lock{Port: oldPort, PID: os.Getpid(), Version: "0.0.9-old", Token: "old-token"})

	newPort := pingServer(t, Version)
	spawn := func() (*childProc, error) {
		writeLock(t, dir, &lifecycle.Lock{Port: newPort, PID: os.Getpid(), Version: Version, Token: "new"})
		return newAliveChild().proc(), nil
	}

	lk, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err != nil {
		t.Fatalf("版本换新应成功: %v", err)
	}
	if lk.Port != newPort {
		t.Fatalf("应拿到新端口 %d, got %d", newPort, lk.Port)
	}
	if shutdownCalls != 1 || gotToken != "old-token" {
		t.Fatalf("应带 token 停旧一次: calls=%d token=%q", shutdownCalls, gotToken)
	}
	assertNoURL(t, "版本换新", err)
}

// 验收:停旧被拒(非 200)→ 报错,不清理活持有,不拉新。
func TestEnsureShutdownRejectedKeepsOld(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ping":
			fmt.Fprint(w, `{"version":"0.0.9-old"}`)
		case "/shutdown":
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oldSrv.Close()
	writeLock(t, dir, &lifecycle.Lock{
		Port: oldSrv.Listener.Addr().(*net.TCPAddr).Port,
		PID:  os.Getpid(), Version: "0.0.9-old", Token: "tok",
	})

	_, err := ensureServer(&config.Config{DataDir: dir}, spawnNever(t))
	if err == nil || !strings.Contains(err.Error(), "停旧") {
		t.Fatalf("停旧被拒应报错: %v", err)
	}
	assertNoURL(t, "停旧被拒", err)
	// 活持有不被清理
	lk, rerr := lifecycle.ReadLock(dir)
	if rerr != nil || lk.Token != "tok" {
		t.Fatalf("旧锁不应被清理: %+v err=%v", lk, rerr)
	}
}

// 停旧回 200 但旧实例不退出 → 等待超时报错,保留其锁。
func TestEnsureShutdownNoExitKeepsOld(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ping":
			fmt.Fprint(w, `{"version":"0.0.9-old"}`)
		case "/shutdown":
			w.WriteHeader(http.StatusOK) // 答应但不退出
		default:
			http.NotFound(w, r)
		}
	}))
	defer oldSrv.Close()
	writeLock(t, dir, &lifecycle.Lock{
		Port: oldSrv.Listener.Addr().(*net.TCPAddr).Port,
		PID:  os.Getpid(), Version: "0.0.9-old", Token: "tok",
	})

	_, err := ensureServer(&config.Config{DataDir: dir}, spawnNever(t))
	if err == nil || !strings.Contains(err.Error(), "未确认退出") {
		t.Fatalf("旧实例不退应报未确认退出: %v", err)
	}
	assertNoURL(t, "不退等待", err)
	if _, statErr := os.Stat(lifecycle.LockPath(dir)); statErr != nil {
		t.Fatalf("旧锁不应被清理: %v", statErr)
	}
}

// 残骸(死 PID+死端口+无持有)→ 清掉后冷启动。
func TestEnsureTakesOverResidue(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	writeLock(t, dir, &lifecycle.Lock{Port: freePort(t), PID: findDeadPID(t), Version: "0.0.9", Token: "dead"})

	port := pingServer(t, Version)
	spawn := func() (*childProc, error) {
		writeLock(t, dir, &lifecycle.Lock{Port: port, PID: os.Getpid(), Version: Version, Token: "new"})
		return newAliveChild().proc(), nil
	}
	lk, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err != nil {
		t.Fatalf("残骸应被接管: %v", err)
	}
	if lk.Token != "new" {
		t.Fatalf("应使用新实例: %+v", lk)
	}
}

// 崩溃竞态:拉起的 serve 死了,但期间手工 serve 已就绪 → 复用活实例。
func TestEnsureCrashedChildButLiveInstanceAppeared(t *testing.T) {
	shortenEnsureTimeouts(t)
	port := pingServer(t, Version)
	dir := t.TempDir()
	spawn := func() (*childProc, error) {
		c := newAliveChild()
		c.exit()
		// 子进程死前,另一路径(手工 serve)已起好并写了锁
		writeLock(t, dir, &lifecycle.Lock{Port: port, PID: os.Getpid(), Version: Version, Token: "manual"})
		return c.proc(), nil
	}
	lk, err := ensureServer(&config.Config{DataDir: dir}, spawn)
	if err != nil {
		t.Fatalf("活实例出现应复用而非报错: %v", err)
	}
	if lk.Token != "manual" {
		t.Fatalf("应复用手工实例: %+v", lk)
	}
}

// start.lock 被长期占用(另一拉起者卡死)→ 锁外轮询超时 → 干净报错。
func TestEnsureStartLockTimeoutOutsidePollFails(t *testing.T) {
	shortenEnsureTimeouts(t)
	dir := t.TempDir()
	g, err := lifecycle.AcquireStartLock(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Release()

	// 缩短 start.lock 等待,使流程快速走到锁外轮询再超时
	oldStart := ensureStartLockTimeout
	ensureStartLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { ensureStartLockTimeout = oldStart })

	_, err = ensureServer(&config.Config{DataDir: dir}, spawnNever(t))
	if err == nil || !strings.Contains(err.Error(), "拉起") {
		t.Fatalf("锁外轮询超时应报错: %v", err)
	}
	assertNoURL(t, "锁外轮询", err)
}

// 全链路兜底:resolveLock 对 ensure 错误再做一次 errText 清洗(纵深防御)。
func TestResolveLockSanitizesEnsureError(t *testing.T) {
	inner := &url.Error{
		Op:  "Post",
		URL: "http://127.0.0.1:9/shutdown",
		Err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused"),
	}
	s, _, _ := newTestSrv(t, "")
	s.ensure = func() (*lifecycle.Lock, error) { return nil, fmt.Errorf("停旧失败: %w", inner) }
	lk, err := s.resolveLock()
	if err == nil || lk != nil {
		t.Fatalf("应报错: %v %v", lk, err)
	}
	if strings.Contains(err.Error(), "http") {
		t.Fatalf("resolveLock 错误泄漏 URL:\n%v", err)
	}
}
