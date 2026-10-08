// Package lifecycle 提供 server.lock 读写与实例探测助手(serve 与 mcp 共用)。
//
// 注意:进程互斥/拉起协议属票 07(受评审约束 F2 暂停);本包只做无争议的
// lock 文件读写、ping、shutdown 调用。
//
// 线上契约(票 01 定义,serve 票 03 / mcp 票 06 按此实现):
//   - GET  /ping     → 200 且 body 为 {"version":"..."};兼容非 JSON 的纯文本 body
//     (整段 trim 后视为版本号);非 200 视为不存活。
//   - POST /shutdown → 请求头 X-Mockit-Token 携带 lock.token,服务端校验;
//     200 视为成功,其余状态码报错。
package lifecycle

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// pingTimeout 是 ping 探测的总超时(票面定值 500ms)。
const pingTimeout = 500 * time.Millisecond

// shutdownTimeout 是 shutdown 请求的总超时;留 2s 给服务端收尾。
const shutdownTimeout = 2 * time.Second

// tokenHeader 是 /shutdown 携带 lock token 的请求头名。
const tokenHeader = "X-Mockit-Token"

// Lock 是 server.lock 的内容。
type Lock struct {
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	Version   string `json:"version"`
	Token     string `json:"token"`
	BaseHost  string `json:"base_host"` // 手机可达基址 host(票 08/F1 定案前可为空)
	StartedAt int64  `json:"started_at"`
}

// PingResult 是 Ping 的探测结论。
type PingResult struct {
	Alive   bool   // lock 所指端口上是否有活着的 mockit serve
	Version string // 存活时对端报告的版本
}

// LockPath 返回 lock 文件路径。
func LockPath(dataDir string) string { return dataDir + "/server.lock" }

// ReadLock 读取 lock 文件;缺失或坏 JSON 均返回明确错误。
func ReadLock(dataDir string) (*Lock, error) {
	p := LockPath(dataDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("lifecycle: lock 文件不存在: %s", p)
		}
		return nil, fmt.Errorf("lifecycle: 读取 lock 文件失败: %s: %w", p, err)
	}
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("lifecycle: lock 文件不是有效 JSON: %s: %w", p, err)
	}
	return &l, nil
}

// WriteLock 原子写 lock 文件:同目录临时文件写完 fsync 后 rename 覆盖,
// 任何一步失败都不留半个文件(临时文件会被清掉)。
func WriteLock(dataDir string, l *Lock) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("lifecycle: 创建数据目录失败: %s: %w", dataDir, err)
	}
	b, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("lifecycle: 序列化 lock 失败: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, "server.lock-*.tmp")
	if err != nil {
		return fmt.Errorf("lifecycle: 创建临时文件失败: %s: %w", dataDir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return fmt.Errorf("lifecycle: 写临时文件失败: %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("lifecycle: 刷盘临时文件失败: %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("lifecycle: 关闭临时文件失败: %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, LockPath(dataDir)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("lifecycle: 原子替换 lock 文件失败: %s -> %s: %w", tmpName, LockPath(dataDir), err)
	}
	return nil
}

// Ping 探测 lock 所指实例是否存活。
// lock 缺失/坏 JSON → 返回 error;请求失败或非 200 → Alive=false 不算错误。
func Ping(dataDir string) (*PingResult, error) {
	l, err := ReadLock(dataDir)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: pingTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/ping", l.Port))
	if err != nil {
		return &PingResult{Alive: false}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &PingResult{Alive: false}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return &PingResult{Alive: false}, nil
	}
	version := pingVersion(body)
	return &PingResult{Alive: true, Version: version}, nil
}

// pingVersion 优先按 {"version":"..."} 解析,失败则把整段 body trim 后当版本号。
func pingVersion(body []byte) string {
	var pj struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &pj); err == nil && pj.Version != "" {
		return pj.Version
	}
	return strings.TrimSpace(string(body))
}

// Shutdown 请求 lock 所指实例退出(token 经 X-Mockit-Token 头携带)。
func Shutdown(dataDir string) error {
	l, err := ReadLock(dataDir)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/shutdown", l.Port), nil)
	if err != nil {
		return fmt.Errorf("lifecycle: 构造 shutdown 请求失败: %w", err)
	}
	req.Header.Set(tokenHeader, l.Token)
	client := &http.Client{Timeout: shutdownTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("lifecycle: shutdown 请求失败(实例可能已退出): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lifecycle: shutdown 被拒绝: HTTP %d", resp.StatusCode)
	}
	return nil
}
