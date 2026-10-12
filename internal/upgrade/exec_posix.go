//go:build !windows

package upgrade

import "syscall"

// procAttr 返回执行新二进制子进程的进程属性(非 Windows 无控制台窗口
// 可言,零属性)。
func procAttr() *syscall.SysProcAttr {
	return nil
}
