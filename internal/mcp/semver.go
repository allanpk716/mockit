package mcp

// 版本号解析与比较(ADR 0001/票 13):MCP 据此判定"只替换比自己旧的
// serve"。解析与比较共用 internal/upgrade 导出的唯一一份实现(审查 T3:
// 原先两包各写一份且严格度不一致——mcp 版的 Atoi 会接受 "+1" 这类
// 形式),统一为更严格的三段纯数字;解析不了的(带后缀、缺段、非数字)
// 一律交由调用方沿用旧行为("不等即替换")。

import (
	"mockit/internal/upgrade"
)

// serveUsable 按 ADR 0001 判定锁记录里的 serve 对本 MCP 是否可直接复用:
// 版本相等 → 复用;serve(semver 可比)更高 → 复用;更低 → 需停旧换新;
// 任一方解析不了 → 保持旧规则"不等即替换"(返回 false)。
func serveUsable(serveVer string) bool {
	if serveVer == Version {
		return true
	}
	sv, ok1 := upgrade.ParseSemver(serveVer)
	mv, ok2 := upgrade.ParseSemver(Version)
	if !ok1 || !ok2 {
		return false
	}
	return sv.Cmp(mv) > 0
}
