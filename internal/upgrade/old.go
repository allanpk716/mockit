package upgrade

import "os"

// RemoveOldBinary 尽力删除 exePath 旁边的升级残留旧二进制(exePath + ".old")。
//
// mockit 任何子命令启动时调用(票 #12):文件不存在或删不掉
// (Windows 上旧进程还活着)都静默跳过,绝不影响该子命令。
func RemoveOldBinary(exePath string) {
	_ = os.Remove(exePath + ".old")
}
