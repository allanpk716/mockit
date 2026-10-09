// Package server 实现 mockit serve:常驻 HTTP server(页面展示、提交管道、
// 审核记录、静态文件)。
//
// 启动序列(锁分离协议 D17,票 08):
//
//	配置校验(external_url 票 09/D16,前置)→ 查实例锁(活持有→输家退出;
//	残骸→清理接管)→ 打开 store → 绑端口(漂移)→ 定 base_host
//	(external_url > NetBird 探测,票 09)→ O_EXCL 建实例锁写记录(失败
//	关监听退出非零)→ HTTP 服务至退出信号或 /shutdown。
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
	"mockit/internal/store"
)

// Version 与 main.go 的 Version 常量保持同步(勿 import main;两处同步维护)。
const Version = "0.1.1"

// maxBindAttempts 端口漂移尝试次数:配置端口起,被占 +1,最多这多个(D8)。
const maxBindAttempts = 10

// shutdownGrace 优雅退出的收尾期限。
const shutdownGrace = 5 * time.Second

// instanceCheckBackoff 是"活持有但 ping 不通"时的复查退避;包级变量供测试缩短。
var instanceCheckBackoff = 800 * time.Millisecond

// acquireInstanceLockFn 建实例锁;包级变量供测试注入失败/冲突场景。
var acquireInstanceLockFn = lifecycle.AcquireInstanceLock

// Serve 启动常驻 HTTP server,返回进程退出码:
//
//	0 — 收到退出信号或 shutdown(token 校验通过)后优雅退出
//	1 — 启动失败(配置非法、store 打不开、端口全占、实例锁失败、已有活实例等)
func Serve(cfg *config.Config) int {
	// 配置校验前置(票 09/D16):非法 external_url 启动即报错,
	// 不接受也不静默剥离,不触碰日志/store/端口/实例锁。
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "mockit serve: 配置错误:", err)
		return 1
	}

	// 日志先行:实例锁检查的结论也要留痕
	logDir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mockit serve: 创建日志目录失败:", err)
		return 1
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mockit serve: 打开日志文件失败:", err)
		return 1
	}
	defer logFile.Close()
	lg := log.New(io.MultiWriter(os.Stderr, logFile), "mockit ", log.LstdFlags)

	// 实例锁检查:每机唯一 serve(D17)。输家在触碰 store/端口前退出。
	if code := checkInstance(cfg.DataDir, lg); code != 0 {
		return code
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mockit serve:", err)
		return 1
	}
	defer st.Close()

	// 清理调度:启动即扫 + 每 24 小时一扫
	stopCleanup := startCleanup(cfg, st, lg)
	defer stopCleanup()

	// 端口绑定循环(漂移结果记入 lock)
	ln, port, err := bindLoop(cfg.Addr, cfg.Port, maxBindAttempts)
	if err != nil {
		lg.Printf("绑定失败(%s:%d 起 %d 个): %v", cfg.Addr, cfg.Port, maxBindAttempts, err)
		return 1
	}

	token, err := genToken()
	if err != nil {
		lg.Println(err)
		ln.Close()
		return 1
	}
	// 对外基址主机(票 09/D16 三层):external_url 已在启动时校验,直接用
	// (与校验同口径 TrimSpace);未配置则探测 NetBird 段唯一命中;
	// 歧义/零命中留空——server 照常启动,仅 submit 侧在基址不可定时报错。
	baseHost := strings.TrimSpace(cfg.ExternalURL)
	if baseHost == "" {
		if baseHost = detectBaseHost(); baseHost == "" {
			lg.Println("未配置 external_url 且 100.64.0.0/10 无唯一命中,base_host 留空:submit 将报无法确定手机可达地址")
		} else {
			lg.Printf("base_host 取探测地址: %s", baseHost)
		}
	}
	guard, err := acquireInstance(cfg.DataDir, port, token, baseHost, lg)
	if err != nil {
		lg.Printf("写实例锁失败,关闭监听退出: %v", err)
		ln.Close() // 不留孤儿端口
		return 1
	}
	defer guard.Release()

	s := newServer(cfg.DataDir, st, token, lg)
	httpSrv := &http.Server{Handler: s.Handler()}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	lg.Printf("serve 已启动: http://%s:%d (pid %d, version %s)", cfg.Addr, port, os.Getpid(), Version)

	code := 0
	select {
	case <-sigCh:
		lg.Println("收到退出信号,优雅退出")
	case <-s.stop:
		lg.Println("收到 shutdown 请求,优雅退出")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			lg.Println("HTTP 服务异常退出:", err)
			code = 1
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		lg.Println("优雅退出超时:", err)
	}
	<-serveErr // 等 Serve goroutine 收尾(ErrServerClosed)
	lg.Println("serve 已退出")
	return code
}

// checkInstance 执行启动序列的实例锁检查,返回 0=继续启动,非 0=退出码:
//   - 无 server.lock → 继续;
//   - 活持有且 ping 通 → 打印"已有实例在端口 X"后输家退出;
//   - 活持有且 ping 不通 → 短暂退避复查后退出,不清理(保守);
//   - 无持有者残留(锁可获取+PID 死+ping 不通)→ 清残骸继续。
func checkInstance(dataDir string, lg *log.Logger) int {
	p, err := lifecycle.ProbeLock(dataDir)
	if err != nil {
		lg.Printf("探测实例锁失败,退出: %v", err)
		return 1
	}
	if !p.Exists {
		return 0
	}
	if p.LiveHeld() {
		if p.Alive {
			lg.Printf("已有实例在端口 %d(pid %d),本进程退出", lockPort(p), lockPID(p))
			return 1
		}
		// 活持有但 ping 不通:可能是对方仍在启动窗口,退避复查一次;
		// 仍活持有则退出且不清理(不误伤可能正在写锁/起服务的对端)。
		lg.Printf("实例锁被持有但 ping 不通(端口 %d),退避 %v 后复查", lockPort(p), instanceCheckBackoff)
		time.Sleep(instanceCheckBackoff)
		p2, err := lifecycle.ProbeLock(dataDir)
		if err != nil {
			lg.Printf("复查实例锁失败,退出: %v", err)
			return 1
		}
		if p2.Exists && p2.LiveHeld() {
			if p2.Alive {
				lg.Printf("已有实例在端口 %d(pid %d),本进程退出", lockPort(p2), lockPID(p2))
				return 1
			}
			lg.Printf("实例锁仍被持有且 ping 不通(端口 %d),退出且不清理;如确认无实例可手工删除 server.lock", lockPort(p2))
			return 1
		}
		p = p2 // 对端消失:按复查结果走残骸分支
	}
	if p.DeadResidue() {
		if err := lifecycle.CleanResidue(dataDir); err != nil {
			lg.Printf("清理实例锁残骸失败,退出: %v", err)
			return 1
		}
		lg.Println("已清理无持有者的实例锁残骸,继续启动")
	}
	return 0
}

// acquireInstance 建实例锁并持有至退出;O_EXCL 冲突时复查一次:
// 活持有→输家退出;残骸/锁消失→清理后重试一次;再失败则报错(调用方关监听)。
// baseHost 为对外通告主机名(票 09/D16),随 lock.base_host 落盘供 MCP 拼接。
func acquireInstance(dataDir string, port int, token, baseHost string, lg *log.Logger) (*lifecycle.InstanceGuard, error) {
	rec := &lifecycle.Lock{
		Port:      port,
		PID:       os.Getpid(),
		Version:   Version,
		Token:     token,
		BaseHost:  baseHost,
		StartedAt: time.Now().Unix(),
	}
	g, err := acquireInstanceLockFn(dataDir, rec)
	if err == nil {
		return g, nil
	}
	if !errors.Is(err, lifecycle.ErrLockExists) {
		return nil, err
	}
	// O_EXCL 失败:复查后输家自行退出(或清理重试)
	p, perr := lifecycle.ProbeLock(dataDir)
	if perr != nil {
		return nil, fmt.Errorf("实例锁创建冲突且复查失败: %w", perr)
	}
	switch {
	case p.LiveHeld():
		lg.Printf("已有实例在端口 %d(pid %d),本进程退出", lockPort(p), lockPID(p))
		return nil, errors.New("已有实例在运行,输家退出")
	case p.DeadResidue():
		if cerr := lifecycle.CleanResidue(dataDir); cerr != nil {
			return nil, cerr
		}
		lg.Println("已清理无持有者的实例锁残骸,重试建锁")
		return acquireInstanceLockFn(dataDir, rec)
	case !p.Exists:
		// 冲突窗口里对端建了又删:直接重试一次
		return acquireInstanceLockFn(dataDir, rec)
	default:
		// Exists 且非活持有非残骸(状态不明):保守退出
		return nil, errors.New("实例锁状态不明,保守退出")
	}
}

// lockPort 取探测结果里的端口,未知记 0。
func lockPort(p *lifecycle.Probe) int {
	if p.Rec != nil {
		return p.Rec.Port
	}
	return 0
}

// lockPID 取探测结果里的 PID,未知记 0。
func lockPID(p *lifecycle.Probe) int {
	if p.Rec != nil {
		return p.Rec.PID
	}
	return 0
}

// bindLoop 从 basePort 起逐个尝试 TCP 绑定,被占则 +1;全部失败返回最后一次错误。
func bindLoop(addr string, basePort, attempts int) (net.Listener, int, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		port := basePort + i
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("%d 个端口均不可用,最后一次: %w", attempts, lastErr)
}
