package mcp

// 三工具(mockit_submit / mockit_get_review / mockit_list)与 serve 定位。
//
// F1 红线:本夜任何工具输出都不得拼接/返回 URL——submit 成功文案必须带
// "暂无链接"说明;get_review/list 同样只出纯文字(F6)。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// maxFileBytes 单候选文件上限(spec 内容管道:单文件 ≤20MB)。
const maxFileBytes = 20 << 20

// errText 把错误压成可给 agent 看的一行文本(F1:工具输出零 URL)。
// Go http 客户端请求失败的错误原文自带完整 URL(形如
// Get "http://127.0.0.1:port/path": dial tcp ...),弱 agent 会把它当审核
// 链接贴给用户;这里剥掉 *url.Error 外壳只留底层原因(dial tcp ...),
// 非 url.Error 原样返回。
func errText(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

// ---- tools/list ----

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

// toolDefs 返回三工具的参数 schema(形状按票面「MCP 工具面」)。
func toolDefs() []toolDef {
	return []toolDef{
		{
			Name:        "mockit_submit",
			Description: "提交 1~6 个候选 mock(单 HTML 或 zip)供用户对比拍板;返回提交 id 与状态(本夜不含 URL,见 F1/票 09)。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string", "description": "提交标题"},
					"note":  map[string]any{"type": "string", "description": "补充说明(可选)"},
					"variants": map[string]any{
						"type":        "array",
						"description": "候选清单,1~6 个;每个为内联 html 或本地 path 之一",
						"minItems":    1,
						"maxItems":    6,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label": map[string]any{"type": "string", "description": "候选标签"},
								"html":  map[string]any{"type": "string", "description": "内联 HTML 内容;与 path 二选一"},
								"path":  map[string]any{"type": "string", "description": "本地文件路径(.html/.htm/.zip);与 html 二选一"},
							},
							"required": []string{"label"},
						},
					},
				},
				"required": []string{"title", "variants"},
			},
		},
		{
			Name:        "mockit_get_review",
			Description: "按提交 id 取回审核结果:状态/裁决/选中候选/批注。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "提交 id"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "mockit_list",
			Description: "列出提交(可按状态过滤、限条数);返回 id/标题/状态简列。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{"type": "string", "enum": []string{"pending", "reviewed"}, "description": "按状态过滤(可选)"},
					"limit":  map[string]any{"type": "integer", "description": "返回条数上限(可选)"},
				},
			},
		},
	}
}

// callTool 分发工具调用;返回 (结果文本, 是否工具级错误)。
func (s *server) callTool(name string, rawArgs json.RawMessage) (string, bool) {
	if len(rawArgs) == 0 {
		rawArgs = json.RawMessage("{}")
	}
	switch name {
	case "mockit_submit":
		return s.toolSubmit(rawArgs)
	case "mockit_get_review":
		return s.toolGetReview(rawArgs)
	case "mockit_list":
		return s.toolList(rawArgs)
	default:
		return fmt.Sprintf("未知工具 %q(可用:mockit_submit / mockit_get_review / mockit_list)", name), true
	}
}

// resolveBase 定位 serve:测试 override → ensure(复用/拉起,票 08)。
// 返回的基址仅内部拼接请求用,绝不进入工具输出;ensure 错误再过一道
// errText(纵深防御,杜绝内部 URL 泄漏)。
func (s *server) resolveBase() (string, error) {
	if s.baseOverride != "" {
		return s.baseOverride, nil
	}
	if s.ensure == nil {
		return "", errors.New("serve 定位未配置(ensure 缺失)")
	}
	lk, err := s.ensure()
	if err != nil {
		s.logf("定位/拉起 serve 失败: %v", err)
		return "", errors.New(errText(err))
	}
	return fmt.Sprintf("http://127.0.0.1:%d", lk.Port), nil
}

// ---- HTTP 薄封装 ----

func (s *server) apiPost(base, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

func (s *server) apiGet(base, path string) (int, []byte, error) {
	resp, err := s.httpc.Get(base + path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

// apiErrText 把非 2xx 响应压成一行错误文本(截长 body)。
func apiErrText(action string, status int, body []byte) string {
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 300 {
		snippet = snippet[:300] + "…"
	}
	if snippet == "" {
		return fmt.Sprintf("%s失败:serve 返回 HTTP %d", action, status)
	}
	return fmt.Sprintf("%s失败:serve 返回 HTTP %d: %s", action, status, snippet)
}

// statusText 给状态值加中文注记。
func statusText(st string) string {
	switch st {
	case "pending":
		return "pending(待审)"
	case "reviewed":
		return "reviewed(已审)"
	default:
		return st
	}
}

// ---- mockit_submit ----

type variantArg struct {
	Label string `json:"label"`
	HTML  string `json:"html"`
	Path  string `json:"path"`
}

type submitArgs struct {
	Title    string       `json:"title"`
	Note     string       `json:"note"`
	Variants []variantArg `json:"variants"`
}

type payloadVariant struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"` // html | zip
	ContentB64 string `json:"content_b64"`
}

type submitPayload struct {
	Title    string           `json:"title"`
	Note     string           `json:"note"`
	Variants []payloadVariant `json:"variants"`
}

// submitResponse 承接 server 的提交响应(subToJSON 形状):候选在
// variants 数组里,无 variant_count 字段——候选数取 len(Variants)。
type submitResponse struct {
	ID       string         `json:"id"`
	Status   string         `json:"status"`
	Variants []variantBrief `json:"variants"`
}

func (s *server) toolSubmit(raw json.RawMessage) (string, bool) {
	var a submitArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "参数解析失败: " + err.Error(), true
	}
	if strings.TrimSpace(a.Title) == "" {
		return "title 不能为空", true
	}
	if len(a.Variants) == 0 || len(a.Variants) > 6 {
		return "候选数须在 1~6 个", true
	}
	payload := submitPayload{Title: a.Title, Note: a.Note}
	for i, v := range a.Variants {
		content, kind, err := variantContent(v)
		if err != nil {
			return fmt.Sprintf("候选 %d(%s): %v", i+1, v.Label, err), true
		}
		payload.Variants = append(payload.Variants, payloadVariant{
			Label:      v.Label,
			Kind:       kind,
			ContentB64: base64.StdEncoding.EncodeToString(content),
		})
	}
	base, err := s.resolveBase()
	if err != nil {
		return err.Error(), true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "请求序列化失败: " + err.Error(), true
	}
	status, respBody, err := s.apiPost(base, "/api/submissions", body)
	if err != nil {
		return fmt.Sprintf("提交失败(请求 serve): %v", errText(err)), true
	}
	if status < 200 || status >= 300 {
		return apiErrText("提交", status, respBody), true
	}
	var r submitResponse
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "提交响应解析失败: " + err.Error(), true
	}
	// F1:本夜结果不含任何 URL;"暂无链接"为票面规定文案。
	return fmt.Sprintf("已提交:id=%s 状态=%s 候选数=%d\nURL 功能待评审约束 F1 定案后启用(票 09),暂无链接。",
		r.ID, statusText(r.Status), len(r.Variants)), false
}

// variantContent 把候选参数变成 (内容字节, kind):html 内联直用;path 按扩展名读本地文件。
func variantContent(v variantArg) ([]byte, string, error) {
	switch {
	case v.HTML != "" && v.Path != "":
		return nil, "", errors.New("html 与 path 只能二选一")
	case v.HTML != "":
		if len(v.HTML) > maxFileBytes {
			return nil, "", errors.New("内联 html 超过 20MB 上限")
		}
		return []byte(v.HTML), "html", nil
	case v.Path != "":
		data, err := os.ReadFile(v.Path)
		if err != nil {
			return nil, "", err
		}
		if len(data) > maxFileBytes {
			return nil, "", errors.New("文件超过 20MB 上限")
		}
		switch strings.ToLower(filepath.Ext(v.Path)) {
		case ".html", ".htm":
			return data, "html", nil
		case ".zip":
			return data, "zip", nil
		default:
			return nil, "", fmt.Errorf("不支持的文件类型 %q(仅 .html/.htm/.zip)", filepath.Ext(v.Path))
		}
	default:
		return nil, "", errors.New("需要 html(内联)或 path(本地文件)之一")
	}
}

// ---- mockit_get_review / mockit_list ----

type idArgs struct {
	ID string `json:"id"`
}

type variantBrief struct {
	Seq   int    `json:"seq"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// submissionJSON 同时承接详情与列表条目(列表为简列,缺省字段零值)。
type submissionJSON struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Status        string         `json:"status"`
	Decision      string         `json:"decision"`
	ChosenVariant any            `json:"chosen_variant"`
	Comment       string         `json:"comment"`
	Variants      []variantBrief `json:"variants"`
}

type listArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

func (s *server) toolGetReview(raw json.RawMessage) (string, bool) {
	var a idArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "参数解析失败: " + err.Error(), true
	}
	if strings.TrimSpace(a.ID) == "" {
		return "id 不能为空", true
	}
	base, err := s.resolveBase()
	if err != nil {
		return err.Error(), true
	}
	status, body, err := s.apiGet(base, "/api/submissions/"+url.PathEscape(a.ID))
	if err != nil {
		return fmt.Sprintf("查询失败(请求 serve): %v", errText(err)), true
	}
	if status < 200 || status >= 300 {
		return apiErrText("查询", status, body), true
	}
	var d submissionJSON
	if err := json.Unmarshal(body, &d); err != nil {
		return "详情响应解析失败: " + err.Error(), true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "提交 %s《%s》\n", d.ID, d.Title)
	fmt.Fprintf(&b, "状态: %s\n", statusText(d.Status))
	switch {
	case d.Decision == "":
		b.WriteString("裁决: (未裁决)\n")
	case d.Decision == "choose" && d.ChosenVariant != nil:
		fmt.Fprintf(&b, "裁决: choose(选中候选 %v)\n", d.ChosenVariant)
	default:
		fmt.Fprintf(&b, "裁决: %s\n", d.Decision)
	}
	if d.Comment == "" {
		b.WriteString("批注: (无)\n")
	} else {
		fmt.Fprintf(&b, "批注: %s\n", d.Comment)
	}
	if len(d.Variants) > 0 {
		parts := make([]string, len(d.Variants))
		for i, v := range d.Variants {
			parts[i] = fmt.Sprintf("[%d] %s(%s)", v.Seq, v.Label, v.Kind)
		}
		fmt.Fprintf(&b, "候选: %s\n", strings.Join(parts, " "))
	}
	return b.String(), false
}

func (s *server) toolList(raw json.RawMessage) (string, bool) {
	var a listArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "参数解析失败: " + err.Error(), true
	}
	base, err := s.resolveBase()
	if err != nil {
		return err.Error(), true
	}
	q := url.Values{}
	if st := strings.TrimSpace(a.Status); st != "" {
		q.Set("status", st)
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	path := "/api/submissions"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	status, body, err := s.apiGet(base, path)
	if err != nil {
		return fmt.Sprintf("查询失败(请求 serve): %v", errText(err)), true
	}
	if status < 200 || status >= 300 {
		return apiErrText("查询", status, body), true
	}
	// server 列表契约是裸数组(internal/server/api_test.go TestList 定案,
	// web 页同按裸数组消费),直接解码数组,勿包 {items} 外壳。
	var items []submissionJSON
	if err := json.Unmarshal(body, &items); err != nil {
		return "列表响应解析失败: " + err.Error(), true
	}
	if len(items) == 0 {
		return "暂无提交", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 条:\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "- %s [%s] %s\n", it.ID, statusText(it.Status), it.Title)
	}
	return b.String(), false
}
