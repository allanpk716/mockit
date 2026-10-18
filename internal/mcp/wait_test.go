package mcp

// 票 09:mockit_get_review 的 wait 参数——校验、透传、超时不受基础客户端
// 固定超时卡死,以及协议层等待调用被 notifications/cancelled 中断(后者
// 在 concurrent_test.go)。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGetReviewWaitParamValidation wait 越界 → 工具参数错误(不触达 serve)。
func TestGetReviewWaitParamValidation(t *testing.T) {
	s, _, _ := newTestSrv(t, "")
	for _, w := range []int{-1, 601, 100000} {
		text, isErr := s.callTool(context.Background(), "mockit_get_review", mustJSON(t, map[string]any{"id": "ab12cd", "wait": w}))
		if !isErr {
			t.Fatalf("wait=%d 应报参数错误,得成功文本: %s", w, text)
		}
		if !strings.Contains(text, "wait") || !strings.Contains(text, "600") {
			t.Fatalf("wait=%d 错误文本应说明 0~600 范围: %s", w, text)
		}
	}
	// 非整数(如小数)也是参数错误
	text, isErr := s.callTool(context.Background(), "mockit_get_review", json.RawMessage(`{"id":"ab12cd","wait":1.5}`))
	if !isErr {
		t.Fatalf("wait=1.5 应报参数错误,得成功文本: %s", text)
	}
}

// TestGetReviewWaitPassthrough wait 透传为查询参数;不传 wait 时与原
// 行为完全一致(无查询参数);边界值 0 与 600 合法且原样透传;返回
// 文本形状不变。
func TestGetReviewWaitPassthrough(t *testing.T) {
	var gotQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/submissions/ab12cd" {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ab12cd","title":"登录页","status":"pending","decision":"","chosen_variant":null,"comment":"","variants":[{"seq":1,"label":"A","kind":"html"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer backend.Close()

	s, _, _ := newTestSrv(t, backend.URL)
	port := backend.Listener.Addr().(*net.TCPAddr).Port

	// 不传 wait:无查询参数,行为与原一致
	text, isErr := s.callTool(context.Background(), "mockit_get_review", mustJSON(t, map[string]any{"id": "ab12cd"}))
	if isErr {
		t.Fatalf("不传 wait 不应失败: %s", text)
	}
	if gotQuery != "" {
		t.Fatalf("不传 wait 时查询串 = %q, want 空", gotQuery)
	}
	for _, want := range []string{"ab12cd", "登录页", "pending(待审)", "未裁决"} {
		if !strings.Contains(text, want) {
			t.Fatalf("不传 wait 的文本形状应不变,缺 %q:\n%s", want, text)
		}
	}

	// 带 wait:透传为 wait=N 查询参数,文本形状不变
	for _, w := range []int{600, 0, 50} {
		text, isErr = s.callTool(context.Background(), "mockit_get_review", mustJSON(t, map[string]any{"id": "ab12cd", "wait": w}))
		if isErr {
			t.Fatalf("wait=%d 不应失败: %s", w, text)
		}
		if want := "wait=" + strconv.Itoa(w); gotQuery != want {
			t.Fatalf("wait=%d 应透传查询串 %q,得 %q", w, want, gotQuery)
		}
		wantURL := fmt.Sprintf("审核链接: http://phone.test:%d/s/ab12cd", port)
		if !strings.Contains(text, "pending(待审)") || !strings.Contains(text, wantURL) {
			t.Fatalf("wait=%d 待审返回应附审核链接:\n%s", w, text)
		}
	}
}

// TestGetReviewWaitOutlivesBaseHTTPTimeout 带 wait 的调用不受共享 httpc
// 固定超时卡死:基础客户端 200ms 超时、serve 侧 600ms 才返回,带
// wait 的调用仍应成功拿到结果(超时由 wait + 余量决定)。
func TestGetReviewWaitOutlivesBaseHTTPTimeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/submissions/ab12cd" {
			time.Sleep(600 * time.Millisecond) // 模拟 serve 侧挂起(超过基础客户端超时)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"ab12cd","title":"登录页","status":"pending","decision":"","chosen_variant":null,"comment":"","variants":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer backend.Close()

	s, _, _ := newTestSrv(t, backend.URL)
	s.httpc = &http.Client{Timeout: 200 * time.Millisecond}

	text, isErr := s.callTool(context.Background(), "mockit_get_review", mustJSON(t, map[string]any{"id": "ab12cd", "wait": 5}))
	if isErr {
		t.Fatalf("带 wait 的调用不应被基础客户端 200ms 超时掐断: %s", text)
	}
	if !strings.Contains(text, "待审") {
		t.Fatalf("返回文本形状应不变:\n%s", text)
	}

	// 不带 wait 的调用仍走共享 httpc(固定超时语义不变)
	text, isErr = s.callTool(context.Background(), "mockit_get_review", mustJSON(t, map[string]any{"id": "ab12cd"}))
	if !isErr || !strings.Contains(text, "查询失败") || !strings.Contains(text, "deadline") {
		t.Fatalf("不带 wait 的调用应仍受基础客户端固定超时约束: isErr=%v\n%s", isErr, text)
	}
}
