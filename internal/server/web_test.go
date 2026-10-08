package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- 票 04 · 前端三页骨架断言 ----
// 只做静态骨架检查:起临时服务 GET 三页,断言关键 HTML 标记、JS 取数与
// 单/多候选分支标记存在、零外部资源引用。不做浏览器级测试,手机真实观感
// (悬浮条/切换/投票动线)列入晨报人工验收。

// webBody GET 页面并要求 200 + text/html。
func webBody(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	resp := get(t, ts, path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 应 200,得 %d", path, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("GET %s Content-Type 应为 text/html,得 %q", path, ct)
	}
	return string(drain(t, resp))
}

// mustContain 断言页面依次包含全部关键标记。
func mustContain(t *testing.T, page, name string, marks ...string) {
	t.Helper()
	for _, mk := range marks {
		if !strings.Contains(page, mk) {
			t.Fatalf("%s 缺少关键标记 %q", name, mk)
		}
	}
}

func TestWebPagesSkeleton(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts,
		htmlVariant("方案A", "<p>a</p>"),
		htmlVariant("方案B", "<p>b</p>"),
	)

	type page struct{ path, name, body string }
	pages := make([]page, 0, 3)
	for _, p := range []struct{ path, name string }{
		{"/", "列表页 index.html"},
		{"/s/" + id, "详情页 detail.html"},
		{"/s/" + id + "/v1", "壳页 variant.html"},
	} {
		pages = append(pages, page{p.path, p.name, webBody(t, ts, p.path)})
	}

	for _, p := range pages {
		// 手机优先 viewport
		mustContain(t, p.body, p.name, `width=device-width, initial-scale=1`)
		// 验收:无任何外网资源引用(内联单文件,禁外部脚本/样式/绝对地址)
		for _, bad := range []string{"http://", "https://", "<script src=", "<link "} {
			if strings.Contains(p.body, bad) {
				t.Fatalf("%s 含被禁的资源引用形态 %q", p.name, bad)
			}
		}
	}

	// 列表页:拉列表、pin 切换、空态、待审/已审分组、候选数
	list := pages[0].body
	mustContain(t, list, pages[0].name,
		`data-page="list"`,
		"/api/submissions",
		"/api/pin",
		`id="empty"`,
		"待审",
		"已审",
		"候选 ",
	)

	// 详情页:按路径 id 取详情、pin、候选入口、cleaned 禁用态、裁决与批注文案
	detail := pages[1].body
	mustContain(t, detail, pages[1].name,
		`data-page="detail"`,
		"'/api/submissions/' + id",
		"/api/pin",
		`id="variants"`,
		"页面已清理",
		"通过",
		"打回",
		"选中",
		"批注",
	)

	// 壳页:全屏 iframe 指 raw 入口、底部悬浮条、pill 切换、批注、提交与跳转、就地报错
	variant := pages[2].body
	mustContain(t, variant, pages[2].name,
		`data-page="variant"`,
		`id="frame"`,
		`id="bar"`,
		`id="pills"`,
		`id="comment"`,
		`id="err"`,
		"'/api/submissions/' + id",
		"'/raw/' + id + '/' + n + '/index.html'",
		"/api/review",
		"'/s/' + id",
	)
}

func TestWebVariantJSBranches(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts,
		htmlVariant("方案A", "<p>a</p>"),
		htmlVariant("方案B", "<p>b</p>"),
	)
	body := webBody(t, ts, "/s/"+id+"/v1")

	// 按钮/提交分支:多候选=选它(choose+variant=n) / 单候选=通过(approve);
	// 打回两分支共用;失败(如已审 409)就地显示,成功跳详情页。
	mustContain(t, body, "壳页分支标记",
		"multi ? 'choose' : 'approve'",
		"multi ? '选它' : '通过'",
		"dataset.mode = multi ? 'multi' : 'single'",
		"body.variant = n",
		"vote('reject')",
		"'提交失败: '",
	)
}
