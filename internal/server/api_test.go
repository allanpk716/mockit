package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockit/internal/store"
)

// ---- 测试脚手架 ----

// newTestServer 起 httptest 层 server(不经绑定循环/lock/信号,纯 handler 面)。
func newTestServer(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := newServer(dataDir, st, "test-token", log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s, dataDir
}

func postJSON(t *testing.T, ts *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal 请求体: %v", err)
	}
	resp, err := ts.Client().Post(ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// postJSONHeader 同 postJSON,附加请求头(shutdown token 用)。
func postJSONHeader(t *testing.T, ts *httptest.Server, path string, body any, header, value string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal 请求体: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, value)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func get(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func drain(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	return b
}

func decodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	b := drain(t, resp)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("解析 JSON %q: %v", b, err)
	}
}

// noRedirectClient 不跟随重定向的 client(目录穿越测试:301 也是拒绝的一种形态)。
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ---- 外部 JSON 契约形状 ----

type variantIn struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	ContentB64 string `json:"content_b64"`
}

type submitIn struct {
	Title    string      `json:"title"`
	Note     string      `json:"note"`
	Variants []variantIn `json:"variants"`
}

type variantOut struct {
	Seq     int    `json:"seq"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Entry   string `json:"entry"`
	Cleaned bool   `json:"cleaned"`
}

type detailOut struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Note          string       `json:"note"`
	Status        string       `json:"status"`
	Decision      string       `json:"decision"`
	ChosenVariant int          `json:"chosen_variant"`
	Comment       string       `json:"comment"`
	CreatedAt     int64        `json:"created_at"`
	ReviewedAt    int64        `json:"reviewed_at"`
	Pinned        bool         `json:"pinned"`
	FilesDeleted  bool         `json:"files_deleted"`
	Variants      []variantOut `json:"variants"`
}

type reviewIn struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Variant  *int   `json:"variant"`
	Comment  string `json:"comment"`
}

type pinIn struct {
	ID     string `json:"id"`
	Pinned bool   `json:"pinned"`
}

// ---- zip 构造 ----

type zEntry struct {
	name   string
	data   []byte
	mode   os.FileMode // 0 → 默认 0644
	method uint16      // zip.Store=0 / zip.Deflate=8
}

func buildZip(t *testing.T, ents []zEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		hdr := &zip.FileHeader{Name: e.name, Method: e.method}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("写 zip 条目 %s: %v", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatalf("写 zip 条目 %s 内容: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("收尾 zip: %v", err)
	}
	return buf.Bytes()
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func htmlVariant(label, content string) variantIn {
	return variantIn{Label: label, Kind: store.KindHTML, ContentB64: b64([]byte(content))}
}

// mustSubmit 提交一组候选,返回新提交 id。
func mustSubmit(t *testing.T, ts *httptest.Server, variants ...variantIn) string {
	t.Helper()
	in := submitIn{Title: "测试提交", Variants: variants}
	resp := postJSON(t, ts, "/api/submissions", in)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提交应 201,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var d detailOut
	decodeJSON(t, resp, &d)
	return d.ID
}

// ---- 提交管道 ----

func TestSubmitHTMLFullChain(t *testing.T) {
	ts, _, dataDir := newTestServer(t)
	html := "<!doctype html><html><body><h1>方案A</h1></body></html>"
	in := submitIn{
		Title: "登录页改版",
		Note:  "两个方案对比",
		Variants: []variantIn{
			htmlVariant("A", html),
			htmlVariant("B", "<p>B</p>"),
		},
	}
	resp := postJSON(t, ts, "/api/submissions", in)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提交应 201,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var d detailOut
	decodeJSON(t, resp, &d)

	if len(d.ID) != 6 {
		t.Fatalf("id 应 6 位,得 %q", d.ID)
	}
	if d.Status != store.StatusPending {
		t.Fatalf("新提交应 pending,得 %q", d.Status)
	}
	if d.Title != "登录页改版" || d.Note != "两个方案对比" {
		t.Fatalf("title/note 回显错: %+v", d)
	}
	if len(d.Variants) != 2 {
		t.Fatalf("应 2 候选,得 %d", len(d.Variants))
	}
	v0 := d.Variants[0]
	if v0.Seq != 1 || v0.Label != "A" || v0.Kind != store.KindHTML || v0.Entry != "index.html" || v0.Cleaned {
		t.Fatalf("候选 1 形状错: %+v", v0)
	}

	// 落盘:index.html 落盘;note 入库不再落盘(note.txt 边车废弃,
	// 留档语义:note 随记录留档 90 天,不被 14 天文件清理连带删除)
	got, err := os.ReadFile(filepath.Join(dataDir, d.ID, "v1", "index.html"))
	if err != nil {
		t.Fatalf("v1 未落盘: %v", err)
	}
	if string(got) != html {
		t.Fatalf("v1 内容错: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, d.ID, "v2", "index.html")); err != nil {
		t.Fatalf("v2 未落盘: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, d.ID, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("note 应入库,note.txt 不应再落盘(stat err=%v)", err)
	}

	// detail 可读
	resp = get(t, ts, "/api/submissions/"+d.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail 应 200,得 %d", resp.StatusCode)
	}
	var d2 detailOut
	decodeJSON(t, resp, &d2)
	if d2.ID != d.ID || d2.Note != "两个方案对比" || len(d2.Variants) != 2 {
		t.Fatalf("detail 形状错: %+v", d2)
	}

	// raw 可取
	rawResp := get(t, ts, "/raw/"+d.ID+"/1/index.html")
	if rawResp.StatusCode != http.StatusOK {
		t.Fatalf("raw 应 200,得 %d", rawResp.StatusCode)
	}
	if string(drain(t, rawResp)) != html {
		t.Fatalf("raw 内容错")
	}
}

// TestNoteServedFromDB note 以库值为准:即使提交目录里残留一份内容
// 过期的 note.txt 边车,API 也只回库值(新数据全走库,文件值不可信)。
func TestNoteServedFromDB(t *testing.T) {
	ts, _, dataDir := newTestServer(t)
	in := submitIn{Title: "带备注", Note: "库里的新备注", Variants: []variantIn{htmlVariant("A", "a")}}
	resp := postJSON(t, ts, "/api/submissions", in)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提交应 201,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var d detailOut
	decodeJSON(t, resp, &d)

	if err := os.WriteFile(filepath.Join(dataDir, d.ID, "note.txt"), []byte("过期的文件值"), 0o644); err != nil {
		t.Fatal(err)
	}
	var d2 detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+d.ID), &d2)
	if d2.Note != "库里的新备注" {
		t.Fatalf("note 应读库值 %q,得 %q", "库里的新备注", d2.Note)
	}
}

// TestLegacyNoteTxtCompat 存量兼容:note 入库改造前的老数据只有
// <data>/{id}/note.txt(库值为空),库空且文件存在时仍读文件,存量不丢。
func TestLegacyNoteTxtCompat(t *testing.T) {
	ts, _, dataDir := newTestServer(t)
	in := submitIn{Title: "存量形态", Variants: []variantIn{htmlVariant("A", "a")}}
	resp := postJSON(t, ts, "/api/submissions", in)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提交应 201,得 %d", resp.StatusCode)
	}
	var d detailOut
	decodeJSON(t, resp, &d)
	if err := os.WriteFile(filepath.Join(dataDir, d.ID, "note.txt"), []byte("存量备注"), 0o644); err != nil {
		t.Fatal(err)
	}
	var d2 detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+d.ID), &d2)
	if d2.Note != "存量备注" {
		t.Fatalf("库值空时应兼容读 note.txt,得 %q", d2.Note)
	}
}

func TestSubmitZipFullChain(t *testing.T) {
	ts, _, dataDir := newTestServer(t)
	zb := buildZip(t, []zEntry{
		{name: "index.html", data: []byte(`<link rel="stylesheet" href="assets/style.css">hi`)},
		{name: "assets/style.css", data: []byte("body{color:red}")},
	})
	in := submitIn{
		Title:    "zip 方案",
		Variants: []variantIn{{Label: "Z", Kind: store.KindZip, ContentB64: b64(zb)}},
	}
	resp := postJSON(t, ts, "/api/submissions", in)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("提交应 201,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var d detailOut
	decodeJSON(t, resp, &d)
	if d.Variants[0].Kind != store.KindZip {
		t.Fatalf("kind 应 zip: %+v", d.Variants[0])
	}

	// 落盘形状
	if b, err := os.ReadFile(filepath.Join(dataDir, d.ID, "v1", "index.html")); err != nil || !strings.Contains(string(b), "style.css") {
		t.Fatalf("zip index.html 落盘错: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dataDir, d.ID, "v1", "assets", "style.css")); err != nil || string(b) != "body{color:red}" {
		t.Fatalf("zip assets/style.css 落盘错: %q, %v", b, err)
	}

	// raw 可取:入口与子路径
	r1 := get(t, ts, "/raw/"+d.ID+"/1/index.html")
	if r1.StatusCode != http.StatusOK || !strings.Contains(string(drain(t, r1)), "style.css") {
		t.Fatalf("raw index.html 错")
	}
	r2 := get(t, ts, "/raw/"+d.ID+"/1/assets/style.css")
	if r2.StatusCode != http.StatusOK || string(drain(t, r2)) != "body{color:red}" {
		t.Fatalf("raw assets/style.css 错")
	}
}

func TestSubmitZIPRejections(t *testing.T) {
	ts, _, dataDir := newTestServer(t)
	okIdx := zEntry{name: "index.html", data: []byte("ok")}
	cases := []struct {
		name string
		ents []zEntry
	}{
		{"缺根 index.html", []zEntry{{name: "other.html", data: []byte("x")}}},
		{".. 上跳条目", []zEntry{okIdx, {name: "../evil.txt", data: []byte("evil")}}},
		{"嵌套 .. 上跳条目", []zEntry{okIdx, {name: "a/../../evil2.txt", data: []byte("evil")}}},
		{"绝对路径条目", []zEntry{okIdx, {name: "/abs.txt", data: []byte("abs")}}},
		{"符号链接条目", []zEntry{okIdx, {name: "lnk", data: []byte("../secret"), mode: os.ModeSymlink | 0o777}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			zb := buildZip(t, tc.ents)
			in := submitIn{
				Title:    "坏 zip",
				Variants: []variantIn{{Kind: store.KindZip, ContentB64: b64(zb)}},
			}
			resp := postJSON(t, ts, "/api/submissions", in)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("应 400 拒,得 %d: %s", resp.StatusCode, drain(t, resp))
			}
			drain(t, resp)
		})
	}
	// 穿越条目不得落盘到 dataDir 根
	for _, evil := range []string{"evil.txt", "evil2.txt", "abs.txt"} {
		if _, err := os.Stat(filepath.Join(dataDir, evil)); err == nil {
			t.Fatalf("穿越文件 %s 落盘了", evil)
		}
	}
}

func TestSubmitZipTooLarge(t *testing.T) {
	ts, _, _ := newTestServer(t)
	zb := buildZip(t, []zEntry{
		{name: "index.html", data: make([]byte, maxZipBytes+1), method: zip.Store},
	})
	in := submitIn{
		Title:    "超大 zip",
		Variants: []variantIn{{Kind: store.KindZip, ContentB64: b64(zb)}},
	}
	resp := postJSON(t, ts, "/api/submissions", in)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超 20MB 应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)
}

func TestSubmitVariantCount(t *testing.T) {
	ts, _, dataDir := newTestServer(t)

	// 0 候选拒
	resp := postJSON(t, ts, "/api/submissions", submitIn{Title: "空", Variants: nil})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("0 候选应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// 7 候选拒
	var seven []variantIn
	for i := 0; i < 7; i++ {
		seven = append(seven, htmlVariant(fmt.Sprintf("V%d", i+1), "x"))
	}
	resp = postJSON(t, ts, "/api/submissions", submitIn{Title: "七个", Variants: seven})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("7 候选应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// 6 候选过(上限)
	id := mustSubmit(t, ts, seven[:6]...)
	if _, err := os.Stat(filepath.Join(dataDir, id, "v6", "index.html")); err != nil {
		t.Fatalf("v6 未落盘: %v", err)
	}
}

func TestReviewFlows(t *testing.T) {
	ts, _, _ := newTestServer(t)

	// approve
	idA := mustSubmit(t, ts, htmlVariant("A", "a"), htmlVariant("B", "b"))
	resp := postJSON(t, ts, "/api/review", reviewIn{ID: idA, Decision: store.DecisionApprove, Comment: "可以"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve 应 200,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var d detailOut
	decodeJSON(t, resp, &d)
	if d.Status != store.StatusReviewed || d.Decision != store.DecisionApprove || d.Comment != "可以" || d.ReviewedAt == 0 {
		t.Fatalf("approve 后形状错: %+v", d)
	}

	// 重复裁决拒(409)
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: idA, Decision: store.DecisionReject})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复裁决应 409,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// detail 已审形状
	resp = get(t, ts, "/api/submissions/"+idA)
	var d2 detailOut
	decodeJSON(t, resp, &d2)
	if d2.Status != store.StatusReviewed || d2.Decision != store.DecisionApprove {
		t.Fatalf("detail 已审形状错: %+v", d2)
	}

	// reject
	idB := mustSubmit(t, ts, htmlVariant("A", "a"))
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: idB, Decision: store.DecisionReject, Comment: "不行"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject 应 200,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// choose 成功(选 2 号)
	idC := mustSubmit(t, ts, htmlVariant("A", "a"), htmlVariant("B", "b"), htmlVariant("C", "c"))
	two := 2
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: idC, Decision: store.DecisionChoose, Variant: &two})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("choose 应 200,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var dC detailOut
	decodeJSON(t, resp, &dC)
	if dC.Decision != store.DecisionChoose || dC.ChosenVariant != 2 {
		t.Fatalf("choose 形状错: %+v", dC)
	}

	// choose 坏 seq 拒
	idD := mustSubmit(t, ts, htmlVariant("A", "a"))
	bad := 99
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: idD, Decision: store.DecisionChoose, Variant: &bad})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("choose 坏 seq 应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// choose 缺 variant 拒
	idE := mustSubmit(t, ts, htmlVariant("A", "a"))
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: idE, Decision: store.DecisionChoose})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("choose 缺 variant 应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// 无效 decision 拒
	idF := mustSubmit(t, ts, htmlVariant("A", "a"))
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: idF, Decision: "maybe"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("无效 decision 应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// 不存在 id 拒
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: "zzzzzz", Decision: store.DecisionApprove})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在 id 应 404,得 %d", resp.StatusCode)
	}
	drain(t, resp)
}

func TestPinToggle(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	resp := postJSON(t, ts, "/api/pin", pinIn{ID: id, Pinned: true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pin 应 200,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	var d detailOut
	decodeJSON(t, resp, &d)
	if !d.Pinned {
		t.Fatalf("pin 后应 true")
	}

	resp = postJSON(t, ts, "/api/pin", pinIn{ID: id, Pinned: false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unpin 应 200,得 %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &d)
	if d.Pinned {
		t.Fatalf("unpin 后应 false")
	}

	resp = postJSON(t, ts, "/api/pin", pinIn{ID: "zzzzzz", Pinned: true})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pin 不存在应 404,得 %d", resp.StatusCode)
	}
	drain(t, resp)
}

func TestRawTraversalReject(t *testing.T) {
	ts, _, dataDir := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	const secret = "TOPSECRET-9f8e7d6c"
	if err := os.WriteFile(filepath.Join(dataDir, "secret.txt"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, id, "secret.txt"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}

	client := noRedirectClient()
	cases := []string{
		"/raw/" + id + "/1/../../secret.txt", // 两级上跳(ServeMux 清洗路径)
		"/raw/" + id + "/1/..%2fsecret.txt",  // 编码一级上跳
		"/raw/" + id + "/1/%2e%2e/%2e%2e/secret.txt",
		"/raw/" + id + "/1/..%5csecret.txt",      // 反斜杠一级上跳(Windows 真实向量)
		"/raw/" + id + "/1/..%5c..%5csecret.txt", // 反斜杠两级上跳
	}
	for _, path := range cases {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("%s 不应 200", path)
		}
		if strings.Contains(string(body), secret) {
			t.Fatalf("%s 泄露了秘密内容", path)
		}
	}
}

func TestPing(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp := get(t, ts, "/ping")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping 应 200,得 %d", resp.StatusCode)
	}
	var pj map[string]string
	decodeJSON(t, resp, &pj)
	if pj["version"] != "0.1.1" {
		t.Fatalf("ping 版本错: %q", pj["version"])
	}
}

func TestList(t *testing.T) {
	ts, _, _ := newTestServer(t)
	idA := mustSubmit(t, ts, htmlVariant("A", "a"), htmlVariant("B", "b"))
	idB := mustSubmit(t, ts, htmlVariant("C", "c"))
	if resp := postJSON(t, ts, "/api/review", reviewIn{ID: idA, Decision: store.DecisionApprove}); resp.StatusCode != http.StatusOK {
		t.Fatalf("review 失败: %d", resp.StatusCode)
	} else {
		drain(t, resp)
	}

	// 全量
	var list []detailOut
	decodeJSON(t, get(t, ts, "/api/submissions"), &list)
	if len(list) != 2 {
		t.Fatalf("应 2 条,得 %d", len(list))
	}
	if list[0].ID != idB {
		t.Fatalf("待审应在前: %s vs %s", list[0].ID, idB)
	}

	// status 过滤
	decodeJSON(t, get(t, ts, "/api/submissions?status=pending"), &list)
	if len(list) != 1 || list[0].ID != idB {
		t.Fatalf("pending 过滤错: %+v", list)
	}
	decodeJSON(t, get(t, ts, "/api/submissions?status=reviewed"), &list)
	if len(list) != 1 || list[0].ID != idA {
		t.Fatalf("reviewed 过滤错: %+v", list)
	}

	// limit
	decodeJSON(t, get(t, ts, "/api/submissions?limit=1"), &list)
	if len(list) != 1 || list[0].ID != idB {
		t.Fatalf("limit=1 错: %+v", list)
	}

	// 坏 limit
	if resp := get(t, ts, "/api/submissions?limit=abc"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("坏 limit 应 400,得 %d", resp.StatusCode)
	} else {
		drain(t, resp)
	}
}

func TestDetailAndInputErrors(t *testing.T) {
	ts, _, _ := newTestServer(t)

	// 不存在
	if resp := get(t, ts, "/api/submissions/zzzzzz"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("detail 不存在应 404,得 %d", resp.StatusCode)
	} else {
		drain(t, resp)
	}

	// 坏 JSON body
	resp, err := ts.Client().Post(ts.URL+"/api/submissions", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	// 未知 kind
	in := submitIn{Title: "x", Variants: []variantIn{{Kind: "pdf", ContentB64: b64([]byte("x"))}}}
	if resp := postJSON(t, ts, "/api/submissions", in); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知 kind 应 400,得 %d", resp.StatusCode)
	} else {
		drain(t, resp)
	}

	// 坏 base64
	in = submitIn{Title: "x", Variants: []variantIn{{Kind: store.KindHTML, ContentB64: "!!!"}}}
	if resp := postJSON(t, ts, "/api/submissions", in); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("坏 base64 应 400,得 %d", resp.StatusCode)
	} else {
		drain(t, resp)
	}

	// 不是 zip 的 zip
	in = submitIn{Title: "x", Variants: []variantIn{{Kind: store.KindZip, ContentB64: b64([]byte("not a zip"))}}}
	if resp := postJSON(t, ts, "/api/submissions", in); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("坏 zip 应 400,得 %d", resp.StatusCode)
	} else {
		drain(t, resp)
	}
}

func TestFilesDeletedCleaned(t *testing.T) {
	ts, s, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"), htmlVariant("B", "b"))

	if err := s.store.MarkFilesDeleted(id); err != nil {
		t.Fatalf("MarkFilesDeleted: %v", err)
	}
	var d detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+id), &d)
	if !d.FilesDeleted {
		t.Fatal("files_deleted 应 true")
	}
	for _, v := range d.Variants {
		if !v.Cleaned {
			t.Fatalf("候选 %d 应带 cleaned 标记", v.Seq)
		}
	}
}

func TestPages(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	for _, path := range []string{"/", "/s/" + id, "/s/" + id + "/v1"} {
		resp := get(t, ts, path)
		body := drain(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s 应 200,得 %d", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("%s Content-Type 错: %q", path, ct)
		}
		if !strings.Contains(string(body), "mockit") {
			t.Fatalf("%s 应为 mockit 占位页", path)
		}
	}

	// n 非数字 / id 非法 → 404
	for _, path := range []string{"/s/" + id + "/vX", "/raw/" + id + "/X/index.html", "/s/BAD_ID/v1"} {
		if resp := get(t, ts, path); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s 应 404,得 %d", path, resp.StatusCode)
		} else {
			drain(t, resp)
		}
	}
}

func TestShutdownToken(t *testing.T) {
	ts, s, _ := newTestServer(t)

	// 无 header → 403
	resp := postJSON(t, ts, "/shutdown", map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 token 应 403,得 %d", resp.StatusCode)
	}
	drain(t, resp)
	select {
	case <-s.stop:
		t.Fatal("错 token 不应触发停止")
	default:
	}

	// 错 token → 403
	resp = postJSONHeader(t, ts, "/shutdown", map[string]any{}, "X-Mockit-Token", "wrong")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("错 token 应 403,得 %d", resp.StatusCode)
	}
	drain(t, resp)
	select {
	case <-s.stop:
		t.Fatal("错 token 不应触发停止")
	default:
	}

	// 对 token → 200 且触发停止
	resp = postJSONHeader(t, ts, "/shutdown", map[string]any{}, "X-Mockit-Token", "test-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("对 token 应 200,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	select {
	case <-s.stop:
	case <-time.After(time.Second):
		t.Fatal("对 token 应触发停止")
	}
}
