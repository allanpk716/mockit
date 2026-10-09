//go:build windows

package mcp

import "syscall"

// procAttr 返回 spawn serve 的进程属性。
// 零闪窗铁律:Windows 下任何被无控制台宿主拉起的 console 程序必须
// HideWindow,否则用户桌面闪黑窗(两次事故级红线)。
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
