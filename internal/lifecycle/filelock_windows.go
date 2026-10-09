//go:build windows

package lifecycle

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFileNB 对已打开文件做非阻塞 OS 级排他锁(Windows LockFileEx,
// 锁 [lockByteOffset, +1) 字节区间——避开偏移 0 的记录数据,强制锁不挡读)。
// 返回值:
//
//	held=false, err=nil  加锁成功(由调用方持有句柄;关句柄即释放)
//	held=true,  err=nil  锁被他持(ERROR_LOCK_VIOLATION)
//	held=false, err!=nil 其他失败(无法判定)
//
// 句柄为同步打开(os.OpenFile 不带 FILE_FLAG_OVERLAPPED),LockFileEx
// 同步完成;FAIL_IMMEDIATELY 保证不阻塞。
func tryLockFileNB(f *os.File) (held bool, err error) {
	ov := new(windows.Overlapped)
	ov.Offset = lockByteOffset
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ov)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}
