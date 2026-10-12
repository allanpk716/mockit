//go:build windows

package lifecycle

import (
	"errors"

	"golang.org/x/sys/windows"
)

// PIDAlive 报告 pid 对应进程是否存活。
// OpenProcess 成功且退出码为 STILL_ACTIVE → 活;ERROR_ACCESS_DENIED(进程
// 存在但拒绝查询,如提权进程)→ 保守按活;其余(含 ERROR_INVALID_PARAMETER=
// pid 不存在)→ 死。进程已退出但他人仍持有其句柄时进程对象不消失、
// OpenProcess 照样成功,故须再查退出码。
// PID 复用致误判属接受的保守降级(F12 留底)。
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // 查不到退出码:保守按活
	}
	return code == stillActive
}

// stillActive 是 GetExitCodeProcess 对运行中进程返回的 STILL_ACTIVE(259)。
// 进程自己以 259 退出时会被误判为活,同属接受的保守降级。
const stillActive = 259
