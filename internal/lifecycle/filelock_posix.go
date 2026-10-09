//go:build !windows

package lifecycle

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFileNB 对已打开文件做非阻塞 OS 级排他锁(POSIX flock)。
// 返回值语义与 Windows 版一致:
//
//	held=false, err=nil  加锁成功(由调用方持有;关句柄即释放)
//	held=true,  err=nil  锁被他持(EWOULDBLOCK)
//	held=false, err!=nil 其他失败(无法判定)
//
// flock 锁属于 open file description:同一进程内两次独立 open 的句柄
// 互相冲突,探测语义与跨进程一致。
func tryLockFileNB(f *os.File) (held bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}
