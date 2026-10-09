//go:build windows

package lifecycle

import (
	"errors"

	"golang.org/x/sys/windows"
)

// PIDAlive 报告 pid 对应进程是否存活。
// OpenProcess 成功 → 活;ERROR_ACCESS_DENIED(进程存在但拒绝查询,如提权
// 进程)→ 保守按活;其余(含 ERROR_INVALID_PARAMETER=pid 不存在)→ 死。
// PID 复用致误判属接受的保守降级(F12 留底)。
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	windows.CloseHandle(h)
	return true
}
