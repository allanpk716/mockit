//go:build windows

package lifecycle

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// 进程已退出、但仍有他人持有其句柄时,Windows 保留进程对象,OpenProcess
// 照样成功。残留 server.lock 的 serve 常处此态(拉起它的父进程未关句柄),
// 若按"能打开即活"判定,DeadResidue 永不成立,MCP 只能报 F9 请人工处置。
func TestPidAliveExitedButHandleHeld(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("起子进程失败: %v", err)
	}
	pid := cmd.Process.Pid
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("打开子进程句柄失败: %v", err)
	}
	defer windows.CloseHandle(h) // 退出后一直持有,模拟"他人握着句柄"
	if err := cmd.Wait(); err != nil {
		t.Fatalf("等子进程退出失败: %v", err)
	}
	if PIDAlive(pid) {
		t.Fatal("已退出(仅句柄未关)的进程应判死")
	}
}
