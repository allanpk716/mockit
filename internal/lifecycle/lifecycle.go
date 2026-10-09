// Package lifecycle 提供锁分离协议(D17)的两把锁与实例探测助手(serve 与 mcp 共用)。
//
// 两把锁:
//   - <data>/start.lock 启动互斥锁:OS 级排他,只由 MCP ensure-server 持有,
//     罩"复查 ping→spawn serve→等实例锁就绪"临界区;serve 进程永不接触。
//   - <data>/server.lock 实例占有锁兼记录:serve 绑定成功后 O_CREATE|O_EXCL
//     创建并立即取 OS 级排他锁持有至进程退出(进程死=OS 自动释放);
//     记录 {port,pid,version,token,base_host,started_at} 写全后 fsync。
//
// 线上契约(票 01 定义,serve 票 03/08 / mcp 票 06/08 按此实现):
//   - GET  /ping     → 200 且 body 为 {"version":"..."};兼容非 JSON 的纯文本 body
//     (整段 trim 后视为版本号);非 200 视为不存活。
//   - POST /shutdown → 请求头 X-Mockit-Token 携带 lock.token,服务端校验;
//     200 视为成功,其余状态码报错。
package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	BaseHost  string `json:"base_host"` // 手机可达基址 host(票 09 落地探测;票 08 只随锁写入流转)
	StartedAt int64  `json:"started_at"`
}

// PingResult 是 Ping 的探测结论。
type PingResult struct {
	Alive   bool   // lock 所指端口上是否有活着的 mockit serve
	Version string // 存活时对端报告的版本
}

// LockPath 返回实例占有锁(server.lock)文件路径。
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

// errText 剥掉 *url.Error 外壳只留底层原因(Go http 客户端错误原文自带完整
// 内部 URL,不得泄漏进 MCP 工具层错误文本;与 internal/mcp/tools.go 同款)。
func errText(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// Ping 探测 lock 所指实例是否存活。
// lock 缺失/坏 JSON → 返回 error;请求失败或非 200 → Alive=false 不算错误。
func Ping(dataDir string) (*PingResult, error) {
	l, err := ReadLock(dataDir)
	if err != nil {
		return nil, err
	}
	alive, version := pingPort(l.Port)
	return &PingResult{Alive: alive, Version: version}, nil
}

// pingPort 对 127.0.0.1:port 的 /ping 做一次探测(该 URL 仅内部使用,
// 错误不外抛——以 (alive, version) 表达结论)。
func pingPort(port int) (alive bool, version string) {
	client := &http.Client{Timeout: pingTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/ping", port))
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false, ""
	}
	return true, pingVersion(body)
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
// 错误文本经 errText 清洗,不携带内部 URL。
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
		return fmt.Errorf("lifecycle: shutdown 请求失败(实例可能已退出): %v", errText(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lifecycle: shutdown 被拒绝: HTTP %d", resp.StatusCode)
	}
	return nil
}
