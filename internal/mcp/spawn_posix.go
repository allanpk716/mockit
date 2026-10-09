//go:build !windows

package mcp

import "syscall"

// procAttr 返回 spawn serve 的进程属性:POSIX 侧 setsid 脱离会话,
// MCP 退出不牵连 serve(detached 语义)。
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
