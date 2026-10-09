package lifecycle

// 锁分离协议(D17)核心:start.lock 启动互斥、server.lock 实例占有、
// 以及实例锁状态探测(ProbeLock)与残骸清理。
//
// 判定口径(spec「两把锁」):
//   - 活持有 = OS 锁不可获取 或 PID 存活(任一即按活持有,保守优先)。
//   - 无持有者残留(可清理)= OS 锁可获取 且 PID 死 且 ping 不通,三条件同立。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// startLockPollInterval 是获取 start.lock 的轮询间隔。
const startLockPollInterval = 20 * time.Millisecond

// lockByteOffset 是 OS 锁作用的字节偏移(server.lock 与 start.lock 共用)。
// Windows LockFileEx 是强制锁:若锁住文件头部,持有期间其他句柄连读记录都
// 会失败;故数据写在偏移 0,锁打在高偏移的 1 字节上,持有/探测都用同一区间。
// POSIX flock 锁整文件且为建议锁,不挡读写,偏移对它无意义。
const lockByteOffset = 1 << 30

// ErrLockExists 表示 server.lock 已被他人创建(O_EXCL 失败),输家应复查后退出。
var ErrLockExists = errors.New("lifecycle: server.lock 已被他人创建")

// StartLockPath 返回启动互斥锁(start.lock)文件路径。
func StartLockPath(dataDir string) string { return dataDir + "/start.lock" }

// StartGuard 是 start.lock 的持有句柄;Release 释放(OS 锁随句柄关闭消失,
// 文件本身保留——空文件常驻,删除反而制造 open/unlink 竞态)。
type StartGuard struct{ f *os.File }

// AcquireStartLock 获取 start.lock 的 OS 级排他锁:非阻塞尝试+轮询,直到
// 拿到或超时。文件不存在则创建(仅作锁载体,内容无意义)。serve 永不调用。
func AcquireStartLock(dataDir string, timeout time.Duration) (*StartGuard, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("lifecycle: 创建数据目录失败: %s: %w", dataDir, err)
	}
	f, err := os.OpenFile(StartLockPath(dataDir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: 打开 start.lock 失败: %s: %w", StartLockPath(dataDir), err)
	}
	deadline := time.Now().Add(timeout)
	for {
		held, err := tryLockFileNB(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lifecycle: 锁 start.lock 失败: %s: %w", StartLockPath(dataDir), err)
		}
		if !held {
			return &StartGuard{f: f}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("lifecycle: 获取 start.lock 超时(%v,另一拉起者正在临界区)", timeout)
		}
		time.Sleep(startLockPollInterval)
	}
}

// Release 释放启动互斥锁。
func (g *StartGuard) Release() {
	if g != nil && g.f != nil {
		g.f.Close() // 关句柄=释放 OS 锁
		g.f = nil
	}
}

// InstanceGuard 是 server.lock 的持有句柄:创建成功即持有 OS 级排他锁,
// 句柄保持打开直至 Release 或进程退出(进程死=OS 自动释放锁)。
type InstanceGuard struct {
	f    *os.File
	path string
}

// AcquireInstanceLock 创建并占有 server.lock:
// O_CREATE|O_EXCL 创建 → 立即取 OS 级排他锁 → 记录写全后 fsync。
// 文件已存在 → 返回包裹 ErrLockExists 的错误(输家应复查后自行退出);
// 锁定/写入失败 → 清掉刚建的文件,不留半写残骸。
func AcquireInstanceLock(dataDir string, rec *Lock) (*InstanceGuard, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("lifecycle: 创建数据目录失败: %s: %w", dataDir, err)
	}
	p := LockPath(dataDir)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lifecycle: %s: %w", p, ErrLockExists)
		}
		return nil, fmt.Errorf("lifecycle: 创建 server.lock 失败: %s: %w", p, err)
	}
	cleanup := func(why error) error {
		f.Close()
		os.Remove(p)
		return why
	}
	held, err := tryLockFileNB(f)
	if err != nil {
		return nil, cleanup(fmt.Errorf("lifecycle: 锁定 server.lock 失败: %s: %w", p, err))
	}
	if held {
		// 新建文件不该有他人持锁;防御分支。
		return nil, cleanup(fmt.Errorf("lifecycle: server.lock 竞争异常: %s", p))
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, cleanup(fmt.Errorf("lifecycle: 序列化 lock 记录失败: %w", err))
	}
	if _, err := f.Write(b); err != nil {
		return nil, cleanup(fmt.Errorf("lifecycle: 写 server.lock 失败: %s: %w", p, err))
	}
	if err := f.Sync(); err != nil {
		return nil, cleanup(fmt.Errorf("lifecycle: 刷盘 server.lock 失败: %s: %w", p, err))
	}
	return &InstanceGuard{f: f, path: p}, nil
}

// Release 释放实例锁:关句柄(释放 OS 锁)并移除文件。供 serve 优雅退出调用;
// 进程被杀时 OS 自动释放锁,文件残骸由下一轮按无持有者残留清理。
func (g *InstanceGuard) Release() {
	if g == nil || g.f == nil {
		return
	}
	g.f.Close()
	os.Remove(g.path)
	g.f = nil
}

// Probe 是 server.lock 的一次快照式状态探测。
type Probe struct {
	Exists   bool   // 文件存在
	Rec      *Lock  // 记录(文件在且 JSON 可解析且含端口;半写/坏 JSON 为 nil)
	Holdable bool   // OS 锁可获取(=无活 OS 持有)
	PIDAlive bool   // 记录 PID 存活(Rec 为 nil 时为 false)
	Alive    bool   // 记录端口 ping 通(Rec 为 nil 时为 false)
	Version  string // ping 报告的版本
}

// ProbeLock 探测 server.lock 当前状态。文件不存在返回零值 Probe(Exists=false)。
// 探测本身失败(打不开/锁调用异常)返回 error——与"不活"严格区分。
func ProbeLock(dataDir string) (*Probe, error) {
	p := LockPath(dataDir)
	probe := &Probe{}
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return probe, nil
		}
		return nil, fmt.Errorf("lifecycle: stat server.lock 失败: %s: %w", p, err)
	}
	probe.Exists = true
	if b, err := os.ReadFile(p); err == nil {
		var l Lock
		if json.Unmarshal(b, &l) == nil && l.Port > 0 {
			probe.Rec = &l
		}
	} // 读失败/坏 JSON/缺端口 → Rec=nil(半写或残骸形态)
	f, err := os.OpenFile(p, os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: 打开 server.lock 探测失败: %s: %w", p, err)
	}
	held, err := tryLockFileNB(f)
	f.Close() // 成功取到的探测锁随句柄关闭释放
	if err != nil {
		return nil, fmt.Errorf("lifecycle: 探测 server.lock 锁状态失败: %s: %w", p, err)
	}
	probe.Holdable = !held
	if probe.Rec != nil {
		probe.PIDAlive = PIDAlive(probe.Rec.PID)
		probe.Alive, probe.Version = pingPort(probe.Rec.Port)
	}
	return probe, nil
}

// LiveHeld 活持有判据:OS 锁不可获取或 PID 存活,任一即真(保守优先)。
func (p *Probe) LiveHeld() bool { return p.Exists && (!p.Holdable || p.PIDAlive) }

// DeadResidue 无持有者残留判据:OS 锁可获取且 PID 死且 ping 不通,三条件同立。
func (p *Probe) DeadResidue() bool {
	return p.Exists && p.Holdable && !p.PIDAlive && !p.Alive
}

// CleanResidue 移除 server.lock 残骸。调用方必须先以 Probe 确认 DeadResidue;
// 活持有(或状态不明)时不得调用(停旧失败不清理活持有)。
func CleanResidue(dataDir string) error {
	if err := os.Remove(LockPath(dataDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("lifecycle: 清理 server.lock 残骸失败: %w", err)
	}
	return nil
}
