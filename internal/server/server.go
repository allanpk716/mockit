// Package server 实现 mockit serve:常驻 HTTP server(页面展示、提交管道、
// 审核记录、静态文件)。
//
// 本票(03)刻意不做:URL 拼接与基址语义(票 09/08)、进程拉起与互斥(票 07)。
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
	"syscall"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
	"mockit/internal/store"
)

// Version 与 main.go 的 Version 常量保持同步(勿 import main;两处同步维护)。
const Version = "0.1.0"

// maxBindAttempts 端口漂移尝试次数:配置端口起,被占 +1,最多这多个(D8)。
const maxBindAttempts = 10

// shutdownGrace 优雅退出的收尾期限。
const shutdownGrace = 5 * time.Second

// Serve 启动常驻 HTTP server,返回进程退出码:
//
//	0 — 收到退出信号或 shutdown(token 校验通过)后优雅退出
//	1 — 启动失败(store 打不开、端口全占、lock 写失败等)
func Serve(cfg *config.Config) int {
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mockit serve:", err)
		return 1
	}
	defer st.Close()

	// 日志:<data>/logs/serve.log,同时回显 stderr
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

	// 清理调度:启动即扫 + 每 24 小时一扫(票 05 接线,协调者补)
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
		return 1
	}
	// base_host 留空:基址语义属后续票(F1),本票不做任何 URL 拼接
	if err := lifecycle.WriteLock(cfg.DataDir, &lifecycle.Lock{
		Port:      port,
		PID:       os.Getpid(),
		Version:   Version,
		Token:     token,
		StartedAt: time.Now().Unix(),
	}); err != nil {
		lg.Println("写 lock 失败:", err)
		return 1
	}

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
