//go:build !windows

package lifecycle

import (
	"errors"
	"syscall"
)

// PIDAlive 报告 pid 对应进程是否存活(kill(pid, 0) 探测)。
// nil 或 EPERM(存在但属于他人)→ 活;ESRCH → 死。
// PID 复用致误判属接受的保守降级(F12 留底)。
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
