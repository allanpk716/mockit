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
		// issue #2 回归:作者样式 #stage-msg{display:flex} 曾盖过 UA 的 [hidden],
		// 空遮罩永久盖住 iframe 致候选页整页空白;各页须带 hidden 兜底
		mustContain(t, p.body, p.name, `[hidden]{display:none!important}`)
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
		"'裁决失败: '",
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
	// (textarea 不用 maxlength:它按 UTF-16 码元计数,emoji 记 2,会拦在
	// 2000 字得;计数与拦截都按码点自行实现,服务端 400 兕底)
	mustContain(t, body, "壳页面板",
		`id="panel"`,
		`<textarea id="comment"`,
		`id="count"`,
		`id="panel-ok"`,
		`id="panel-cancel"`,
		"MAX_COMMENT = 2000",
	)
	if strings.Contains(body, `maxlength="2000"`) {
		t.Fatal("批注框不应依赖 maxlength:它按 UTF-16 码元计数,与按字符计的口径不一致")
	}

	// 关键分支:打回空批注前端拦截;超限前端拦截(计数器转错误态、确认
	// 被拒并给出明确文案);裁决失败就地报错且批注不丢
	mustContain(t, body, "壳页面板分支",
		"打回必须写批注",
		"'已超 '",
		"批注超过 2000 字上限",
		"'裁决失败: '",
	)

	// 已审态底栏摘要:批注截断约 60 字(用常量),全文留给详情页
	mustContain(t, body, "壳页已审摘要",
		"truncate(sub.comment, SUMMARY_LEN)",
	)

	// 手机验收反馈:只有两个按钮时用户看不出能写批注。底栏常驻一行批注
	// 输入条(点开即面板的「写批注」模式,面板内直接 选它/打回),有草稿时
	// 显示草稿首行;已审态隐藏输入条。
	mustContain(t, body, "壳页常驻批注输入条",
		`id="note-entry"`,
		"写批注…(打回必填)",
		"openPanel('')",
		`id="panel-choose"`,
		`id="panel-reject"`,
		"$('note-entry').hidden = true",
		"syncEntry()",
	)
}

// ---- 票 07 · 批注草稿按提交保存 ----
// 写到一半的批注按提交 id 存手机浏览器本地存储,同一提交所有候选页共用一份
// (切候选/刷新/误关后回来都在);输入实时保存,页面加载恢复,裁决成功后
// 清除、失败保留;私有模式等 localStorage 访问可能抛错,读写一律 try/catch 兜底。
func TestWebVariantCommentDraft(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts,
		htmlVariant("方案A", "<p>a</p>"),
		htmlVariant("方案B", "<p>b</p>"),
	)
	body := webBody(t, ts, "/s/"+id+"/v1")

	// 存储键按提交 id 区分:不同提交的草稿互不串
	mustContain(t, body, "壳页草稿",
		"var DRAFT_PREFIX = 'mockit.draft.'",
		"DRAFT_PREFIX + id",
		"localStorage.getItem(draftKey)",
		"localStorage.setItem(draftKey, v)",
		"localStorage.removeItem(draftKey)",
	)

	// localStorage 访问 try/catch 兜底(私有模式可能抛错,退化不阻拦裁决)
	mustContain(t, body, "壳页草稿兜底",
		"try{ return localStorage.getItem(draftKey) || ''; }catch(e){ return ''; }",
		"try{ localStorage.setItem(draftKey, v); }catch(e){}",
		"try{ localStorage.removeItem(draftKey); }catch(e){}",
	)

	// 动线:输入实时保存;页面加载恢复(面板打开即见草稿);裁决成功才清除
	mustContain(t, body, "壳页草稿动线",
		"saveDraft($('comment').value)",
		"$('comment').value = loadDraft();",
		"if(res.ok){ clearDraft();",
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
