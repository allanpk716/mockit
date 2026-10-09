package mcp

// ensure-server:定位或拉起本机 mockit serve(锁分离协议 D17,票 08)。
//
// 流程:读 server.lock → 活持有 ping 通:版本一致复用 / 不一致带 token
// 停旧并等确认退出后才清残骸(停旧失败不清理活持有)→ 冷启动:取
// start.lock(超时改锁外轮询等就绪)→ 复查 → spawn serve(零闪窗)→
// 等实例锁就绪 → 释放 start.lock。serve 活但 ping 不通 → 报错提示人工
// 处置(F9);超时按被拉起进程死活分别处理。
//
// 错误文本红线:不得携带内部 URL(经 errText 清洗)。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

// F10 超时常量(包级变量,测试可缩短)。
var (
	// ensureStartLockTimeout 是获取 start.lock 的默认超时。
	ensureStartLockTimeout = 30 * time.Second
	// ensureReadyTimeout 是等实例锁就绪的默认超时(锁外轮询复用同值)。
	ensureReadyTimeout = 30 * time.Second
	// ensureShutdownWait 是停旧后等确认退出的默认超时。
	ensureShutdownWait = 10 * time.Second
	// ensurePollInterval 是就绪/退出确认轮询的间隔。
	ensurePollInterval = 100 * time.Millisecond
)

// f9Hint 是"serve 活但 ping 不通"的统一人工处置提示(F9)。
const f9Hint = "serve 进程存活但 ping 不通,疑似卡死;请人工处置(结束该进程后重试,或手工运行 mockit serve 观察输出)"

// childProc 是被拉起进程的非阻塞存活探测句柄。
type childProc struct {
	done chan error // Wait 落定(收到值或通道关闭)即视为进程退出
}

// Exited 非阻塞报告被拉起进程是否已退出。
func (c *childProc) Exited() bool {
	if c == nil || c.done == nil {
		return false
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// spawnFunc 拉起一个 serve 进程;生产实现 defaultSpawnServe,测试注入。
type spawnFunc func() (*childProc, error)

// ensureServer 定位或拉起 serve,返回可用实例的 lock 记录。
func ensureServer(cfg *config.Config, spawn spawnFunc) (*lifecycle.Lock, error) {
	p, err := lifecycle.ProbeLock(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("定位 serve 失败: %w", err)
	}
	if p.Exists && p.LiveHeld() {
		if !p.Alive {
			return nil, errors.New(f9Hint)
		}
		if p.Rec.Version == Version {
			return p.Rec, nil
		}
		if err := stopOldServe(cfg, p); err != nil {
			return nil, err
		}
		return coldStart(cfg, spawn)
	}
	if p.Exists {
		if p.DeadResidue() {
			if err := lifecycle.CleanResidue(cfg.DataDir); err != nil {
				return nil, fmt.Errorf("清理实例锁残骸失败: %w", err)
			}
		} else {
			// Exists 且非活持有非残骸(状态不明,如 ping 通却无人持有):保守拒绝
			return nil, errors.New("实例锁状态不明,请人工处置(可手工运行 mockit serve 观察输出)")
		}
	}
	return coldStart(cfg, spawn)
}

// stopOldServe 停旧并等确认退出,确认后才清残骸;任何失败都不清理活持有。
func stopOldServe(cfg *config.Config, p *lifecycle.Probe) error {
	oldVer := ""
	if p.Rec != nil {
		oldVer = p.Rec.Version
	}
	if err := lifecycle.Shutdown(cfg.DataDir); err != nil {
		return fmt.Errorf("停旧 serve 失败(版本 %s → %s): %v", oldVer, Version, errText(err))
	}
	if err := waitNotLive(cfg.DataDir, ensureShutdownWait); err != nil {
		return fmt.Errorf("停旧 serve 后未确认退出(%v),保留其锁不清理;请人工处置", err)
	}
	// 确认退出:残骸(若有)此时才可清
	p2, err := lifecycle.ProbeLock(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("停旧后复查实例锁失败: %w", err)
	}
	if p2.Exists && p2.DeadResidue() {
		if err := lifecycle.CleanResidue(cfg.DataDir); err != nil {
			return fmt.Errorf("清理停旧残骸失败: %w", err)
		}
	} else if p2.Exists {
		return errors.New("停旧后实例锁仍有持有者或状态不明,不清理;请人工处置")
	}
	return nil
}

// waitNotLive 轮询直到实例锁不再被活持有(文件消失或转残骸)或超时。
func waitNotLive(dataDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		p, err := lifecycle.ProbeLock(dataDir)
		if err != nil {
			return err
		}
		if !p.LiveHeld() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 %v 超时", timeout)
		}
		time.Sleep(ensurePollInterval)
	}
}

// waitReady 锁外轮询等任一 serve 就绪(活持有+ping 通)。
func waitReady(dataDir string, timeout time.Duration) (*lifecycle.Lock, bool) {
	deadline := time.Now().Add(timeout)
	for {
		p, err := lifecycle.ProbeLock(dataDir)
		if err == nil && p.Exists && p.LiveHeld() && p.Alive && p.Rec != nil {
			return p.Rec, true
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(ensurePollInterval)
	}
}

// coldStart 拉起临界区:取 start.lock → 复查 → spawn → 等实例锁就绪 → 释放。
func coldStart(cfg *config.Config, spawn spawnFunc) (*lifecycle.Lock, error) {
	sl, err := lifecycle.AcquireStartLock(cfg.DataDir, ensureStartLockTimeout)
	if err != nil {
		// 取锁超时:另一拉起者在临界区,锁外轮询等它把 serve 带起来(F10)。
		if lk, ok := waitReady(cfg.DataDir, ensureReadyTimeout); ok {
			if lk.Version == Version {
				return lk, nil
			}
			return nil, fmt.Errorf("另一 serve 已就绪但版本不符(%s ≠ %s),请人工处置", lk.Version, Version)
		}
		return nil, fmt.Errorf("拉起 serve 失败: 等待另一拉起者超时(%v)", errText(err))
	}
	defer sl.Release()

	// 临界区内复查(赢家可能已在临界区外把 serve 起好)
	p, err := lifecycle.ProbeLock(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("定位 serve 失败: %w", err)
	}
	if p.Exists {
		switch {
		case p.LiveHeld() && p.Alive && p.Rec.Version == Version:
			return p.Rec, nil
		case p.LiveHeld() && p.Alive:
			if err := stopOldServe(cfg, p); err != nil {
				return nil, err
			}
		case p.LiveHeld():
			return nil, errors.New(f9Hint)
		case p.DeadResidue():
			if err := lifecycle.CleanResidue(cfg.DataDir); err != nil {
				return nil, fmt.Errorf("清理实例锁残骸失败: %w", err)
			}
		default:
			return nil, errors.New("实例锁状态不明,请人工处置")
		}
	}

	child, err := spawn()
	if err != nil {
		return nil, fmt.Errorf("拉起 serve 失败: %v", errText(err))
	}

	// 等实例锁就绪;超时按被拉起进程死活分别处理。
	deadline := time.Now().Add(ensureReadyTimeout)
	for {
		if child.Exited() {
			// 进程死:复查一次——期间若有别的 serve 就绪则复用,否则清残骸报错。
			if lk, ok := waitReady(cfg.DataDir, ensurePollInterval); ok && lk.Version == Version {
				return lk, nil
			}
			if p2, perr := lifecycle.ProbeLock(cfg.DataDir); perr == nil && p2.DeadResidue() {
				_ = lifecycle.CleanResidue(cfg.DataDir)
			}
			return nil, errors.New("拉起的 serve 启动中退出(未就绪);可手工运行 mockit serve 查看报错")
		}
		p, err := lifecycle.ProbeLock(cfg.DataDir)
		if err == nil && p.Exists && p.LiveHeld() && p.Alive && p.Rec != nil {
			if p.Rec.Version == Version {
				return p.Rec, nil
			}
			return nil, fmt.Errorf("拉起的 serve 版本异常(%s ≠ %s),请人工处置", p.Rec.Version, Version)
		}
		if time.Now().After(deadline) {
			if child.Exited() {
				if p2, perr := lifecycle.ProbeLock(cfg.DataDir); perr == nil && p2.DeadResidue() {
					_ = lifecycle.CleanResidue(cfg.DataDir)
				}
				return nil, errors.New("拉起的 serve 启动中退出(未就绪);可手工运行 mockit serve 查看报错")
			}
			return nil, fmt.Errorf("serve 拉起后 %v 内未就绪且进程仍在;%s", ensureReadyTimeout, f9Hint)
		}
		time.Sleep(ensurePollInterval)
	}
}

// defaultSpawnServe 生产实现:以本二进制拉起 `mockit serve`(detached;
// Windows 必须 HideWindow——零闪窗铁律)。子进程继承环境与家目录配置,
// 与本进程解析出同一份 config。
func defaultSpawnServe() (*childProc, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("定位自身可执行文件失败: %w", err)
	}
	cmd := exec.Command(exe, "serve")
	cmd.SysProcAttr = procAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 serve 进程失败: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return &childProc{done: done}, nil
}
