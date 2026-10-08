// Package lifecycle 提供 server.lock 读写与实例探测助手(serve 与 mcp 共用)。
//
// 注意:进程互斥/拉起协议属票 07(受评审约束 F2 暂停);本包只做无争议的
// lock 文件读写、ping、shutdown 调用。
package lifecycle

import "errors"

// Lock 是 server.lock 的内容。
type Lock struct {
	Port     int    `json:"port"`
	PID      int    `json:"pid"`
	Version  string `json:"version"`
	Token    string `json:"token"`
	BaseHost string `json:"base_host"` // 手机可达基址 host(票 08/F1 定案前可为空)
	StartedAt int64 `json:"started_at"`
}

// LockPath 返回 lock 文件路径。
func LockPath(dataDir string) string { return dataDir + "/server.lock" }

// ReadLock 读取 lock 文件。
func ReadLock(dataDir string) (*Lock, error) { return nil, errors.New("lifecycle: 未实施(票 01)") }

// WriteLock 原子写 lock 文件。
func WriteLock(dataDir string, l *Lock) error { return errors.New("lifecycle: 未实施(票 01)") }
