package mcp

// 审查 T3:semver 解析/比较与 internal/upgrade 合并后,解析规则统一为
// 更严格的"三段纯数字"(mcp 原实现用 strconv.Atoi,会接受 "+1" 这类
// 形式,与 upgrade 版不一致)。serve 报出任何解析不了的形式时,一律按
// ADR 0001 的旧行为"不等即替换"(serveUsable 返回 false)。

import "testing"

func TestServeUsableStrictParsing(t *testing.T) {
	for _, v := range []string{"+9.9.9", "v+1.2.3", "1.2.3.4", "v1.2", "1.2.x", "v1.2.3-beta", ""} {
		if serveUsable(v) {
			t.Fatalf("serveUsable(%q) 应为 false(解析不了 → 不等即替换)", v)
		}
	}
	for _, v := range []string{Version, "9.9.9", "v9.9.9"} {
		if !serveUsable(v) {
			t.Fatalf("serveUsable(%q) 应为 true(相等或更新 → 复用)", v)
		}
	}
}
