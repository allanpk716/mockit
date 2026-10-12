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
	// 打回两分支共用;点按钮先弹底部批注面板,确认才发裁决;失败(如已审 409)
	// 在面板内就地显示,成功跳详情页。
	mustContain(t, body, "壳页分支标记",
		"multi ? 'choose' : 'approve'",
		"multi ? '选它' : '通过'",
		"dataset.mode = multi ? 'multi' : 'single'",
		"body.variant = n",
		"openPanel('reject')",
		"'提交失败: '",
	)
}

// ---- 票 06 · 底栏改版与底部批注面板 ----
// 底栏只剩候选圆点与两个按钮;点任一按钮从底部弹出面板(多行批注框、
// 剩余字数、确认、取消);打回空批注前端拦截;已审态底栏只显摘要。
func TestWebVariantReviewPanel(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts,
		htmlVariant("方案A", "<p>a</p>"),
		htmlVariant("方案B", "<p>b</p>"),
	)
	body := webBody(t, ts, "/s/"+id+"/v1")

	// 底栏:候选圆点 + 两个按钮;常驻单行批注输入框退场(批注改入面板)
	mustContain(t, body, "壳页底栏",
		`id="pills"`,
		`id="btn-primary"`,
		`id="btn-reject"`,
	)
	if strings.Contains(body, `<input id="comment"`) {
		t.Fatal("壳页底栏不应再常驻单行批注输入框,批注应改入底部面板")
	}

	// 底部面板:多行批注框、剩余字数计数、确认、取消;上限 2000 字按字符计
	mustContain(t, body, "壳页面板",
		`id="panel"`,
		`<textarea id="comment"`,
		`maxlength="2000"`,
		`id="count"`,
		`id="panel-ok"`,
		`id="panel-cancel"`,
		"MAX_COMMENT = 2000",
	)

	// 关键分支:打回空批注前端拦截;裁决失败就地报错且批注不丢
	mustContain(t, body, "壳页面板分支",
		"打回必须写批注",
		"'提交失败: '",
	)

	// 已审态底栏摘要:批注截断约 60 字,全文留给详情页
	mustContain(t, body, "壳页已审摘要",
		"truncate(sub.comment, 60)",
	)
}

// TestWebDetailCommentFullText 详情页批注全文:候选页底栏只给摘要,
// 详情页显示全文且保留换行(white-space:pre-wrap + textContent 写入)。
func TestWebDetailCommentFullText(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("方案A", "<p>a</p>"))
	body := webBody(t, ts, "/s/"+id)

	mustContain(t, body, "详情页批注",
		`id="comment"`,
		"white-space:pre-wrap",
		"'批注:' + s.comment",
	)
}

// ---- 票 05 · [hidden] 兜底规则防退化 ----
// #stage-msg 的 id 选择器 display:flex 优先级压过 UA 的 [hidden]{display:none},
// 空遮罩曾永远盖住候选 iframe 致整页空白(原 feedback/2026-10-10-variant-page-blank-
// overlay.md)。三个嵌入页公共样式统一兜底,防任何页面再踩“hidden 属性被自身
// display 样式覆盖”的同类坑。
func TestWebPagesHiddenFallback(t *testing.T) {
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

	// 每个嵌入页都有 [hidden] 兜底:一律不显示且优先级压过元素自身 display 样式
	for _, p := range pages {
		mustContain(t, p.body, p.name, "[hidden]{display:none!important}")
	}

	// 壳页具体病灶与显示逻辑:遮罩保留 flex 布局(隐藏只靠兜底规则,而非删掉
	// display:flex);取消 hidden 后加载失败提示仍可见,fail 时悬浮条仍隐藏
	variant := pages[2].body
	mustContain(t, variant, pages[2].name,
		"#stage-msg{",
		"el.hidden = false",
		"$('bar').hidden = true",
	)
}
