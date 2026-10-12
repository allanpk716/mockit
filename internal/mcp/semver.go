package mcp

// 版本号解析与比较(ADR 0001/票 13):MCP 据此判定"只替换比自己旧的
// serve"。只认 v?X.Y.Z 三段纯数字(mockit 版本常量与 release tag 的
// 形式);其余(带后缀、缺段、非数字)一律按"解析不了"处理,交由
// 调用方沿用旧行为("不等即替换")。

import (
	"strconv"
	"strings"
)

// semver 是解析后的三段版本号(major/minor/patch)。
type semver [3]int

// parseSemver 解析 "vX.Y.Z"/"X.Y.Z";任何偏离三段纯数字的形式都返回
// ok=false。
func parseSemver(v string) (semver, bool) {
	var out semver
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		if p == "" {
			return out, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// compareSemver 逐段比较:major → minor → patch;a<b 返回 -1,a>b 返回 1。
func compareSemver(a, b semver) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// serveUsable 按 ADR 0001 判定锁记录里的 serve 对本 MCP 是否可直接复用:
// 版本相等 → 复用;serve(semver 可比)更高 → 复用;更低 → 需停旧换新;
// 任一方解析不了 → 保持旧规则"不等即替换"(返回 false)。
func serveUsable(serveVer string) bool {
	if serveVer == Version {
		return true
	}
	sv, ok1 := parseSemver(serveVer)
	mv, ok2 := parseSemver(Version)
	if !ok1 || !ok2 {
		return false
	}
	return compareSemver(sv, mv) > 0
}
