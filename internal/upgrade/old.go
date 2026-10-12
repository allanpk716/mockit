package upgrade

import (
	"fmt"
	"os"
)

// oldSuffix 是升级替换时旧二进制的改名后缀(票 #12):Windows 上运行中的
// exe 可改名不可覆盖,旧文件改名为 <名>.old 落位。若 .old 仍被更旧的
// 进程占用删不掉(如旧 serve 还在跑),依次改用备用名 .old.1、.old.2…
// (审查 S2:Windows 运行中也能再次升级),启动清理一并识别。
const oldSuffix = ".old"

// oldAltLimit 是备用旧文件名 .old.N 的序号上限,防异常情况下无限尝试。
const oldAltLimit = 16

// oldPath 返回第 i 个旧文件落位名:i 为 0 时是 <exe>.old,i>0 时是
// <exe>.old.<i>。
func oldPath(exePath string, i int) string {
	if i <= 0 {
		return exePath + oldSuffix
	}
	return fmt.Sprintf("%s%s.%d", exePath, oldSuffix, i)
}

// RemoveOldBinary 尽力删除 exePath 旁边的升级残留旧二进制(.old 及备用
// 名 .old.1、.old.2…)。
//
// mockit 任何子命令启动时调用(票 #12):文件不存在或删不掉
// (Windows 上旧进程还活着)都静默跳过,绝不影响该子命令。
func RemoveOldBinary(exePath string) {
	for i := 0; i <= oldAltLimit; i++ {
		_ = os.Remove(oldPath(exePath, i))
	}
}
