//go:build windows

package upgrade

import "syscall"

// procAttr 返回执行新二进制子进程的进程属性(零闪窗铁律,同 mcp 包的
// spawn:Windows 下无控制台宿主(如 Claude Code 拉起的 mockit mcp)执行
// console 程序必须 HideWindow,否则用户桌面闪黑窗)。
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}
