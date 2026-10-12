package server

// 票 09:详情 API 的 wait 长轮询参数——校验、超时返回待审、裁决成功唤醒、
// 客户端断开释放。测试同步一律 channel/轮询 + deadline,不用固定 sleep。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"mockit/internal/store"
)

// eventually 在 deadline 内轮询条件就位(观察服务端内部状态,不用固定
// sleep 假设同步)。
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s: 5s 内未就绪", what)
}

// TestDetailWaitParamValidation wait 非法 → 400;wait=0 与缺省同义,立即返回。
func TestDetailWaitParamValidation(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	for _, q := range []string{"wait=abc", "wait=-1", "wait=601", "wait=1.5"} {
		if resp := get(t, ts, "/api/submissions/"+id+"?"+q); resp.StatusCode != 400 {
			t.Fatalf("?%s 应 400,得 %d: %s", q, resp.StatusCode, drain(t, resp))
		} else {
			drain(t, resp)
		}
	}

	// wait=0 与缺省同义:立即返回待审
	var d detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+id+"?wait=0"), &d)
	if d.Status != store.StatusPending {
		t.Fatalf("wait=0 应立即返回待审,得 %q", d.Status)
	}
	decodeJSON(t, get(t, ts, "/api/submissions/"+id), &d)
	if d.Status != store.StatusPending {
		t.Fatalf("缺省 wait 应立即返回待审,得 %q", d.Status)
	}
}

// TestDetailWaitTimesOutAsPending 无人裁决:挂满 wait 秒后返回当前待审
// 状态(而非报错)。耗时下界证明真的挂起了(timer 不会提前发火)。
func TestDetailWaitTimesOutAsPending(t *testing.T) {
	ts, _, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	start := time.Now()
	var d detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+id+"?wait=1"), &d)
	elapsed := time.Since(start)
	if d.Status != store.StatusPending || d.Decision != "" {
		t.Fatalf("超时应返回待审状态,得 %+v", d)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("应挂起约 1s 再返回,实际 %v", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("返回过慢: %v", elapsed)
	}
}

// TestDetailWaitWakesOnReview 等待中另一请求完成裁决 → 等待方立即
// (而非等满 wait)拿到已审结果与批注;同提交多个等待者一起被唤醒,
// 唤醒后登记清零。
func TestDetailWaitWakesOnReview(t *testing.T) {
	ts, s, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	// 请求 ctx 挂到测试收尾:失败路径也能立刻释放等待者,不拖慢清理
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 2
	type res struct {
		d   detailOut
		err error
	}
	resCh := make(chan res, n)
	for i := 0; i < n; i++ {
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/submissions/"+id+"?wait=30", nil)
			if err != nil {
				resCh <- res{err: err}
				return
			}
			resp, err := ts.Client().Do(req)
			if err != nil {
				resCh <- res{err: err}
				return
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			var d detailOut
			if err := json.Unmarshal(b, &d); err != nil {
				resCh <- res{err: err}
				return
			}
			resCh <- res{d: d}
		}()
	}
	eventually(t, "两个等待者登记", func() bool { return s.waiters.count() == n })

	// 另一请求完成裁决(带批注)
	resp := postJSON(t, ts, "/api/review", reviewIn{ID: id, Decision: store.DecisionApprove, Comment: "就这个"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("裁决应 200,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	for i := 0; i < n; i++ {
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("等待方请求失败: %v", r.err)
			}
			if r.d.Status != store.StatusReviewed || r.d.Decision != store.DecisionApprove || r.d.Comment != "就这个" {
				t.Fatalf("等待方应拿到裁决唤醒的已审结果,得 %+v", r.d)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("裁决后 5s 内等待方未返回(应由裁决成功路径唤醒,而非等满 30s)")
		}
	}
	eventually(t, "唤醒后登记清零", func() bool { return s.waiters.count() == 0 })
}

// TestDetailWaitClientDisconnectReleasesWaiter 客户端提前断开 → 服务端
// 等待被及时释放(登记清零,不泄漏),后续裁决照常。
func TestDetailWaitClientDisconnectReleasesWaiter(t *testing.T) {
	ts, s, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/submissions/"+id+"?wait=3", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, err := ts.Client().Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	eventually(t, "等待者登记", func() bool { return s.waiters.count() == 1 })

	cancel() // 客户端断开
	// 释放必须及时(远小于 wait=3s 的超时路径),否则是泄漏
	released := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(released) {
		if s.waiters.count() == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if s.waiters.count() != 0 {
		t.Fatalf("断开后 1.5s 内等待者应被释放,仍登记 %d 个", s.waiters.count())
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("客户端取消后请求应失败返回")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("断开后请求未及时收尾")
	}

	// 后续照常:裁决成功,详情可取
	resp := postJSON(t, ts, "/api/review", reviewIn{ID: id, Decision: store.DecisionReject, Comment: "改改改"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("断开释放后续裁决应 200,得 %d: %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	var d detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+id), &d)
	if d.Status != store.StatusReviewed {
		t.Fatalf("后续详情应已审,得 %q", d.Status)
	}
}

// TestDetailWaitReviewedAndMissing 已审提交带 wait → 立即返回已审结果
// (不登记等待);不存在 id 带 wait → 立即 404,不挂起。
func TestDetailWaitReviewedAndMissing(t *testing.T) {
	ts, s, _ := newTestServer(t)
	id := mustSubmit(t, ts, htmlVariant("A", "a"))
	two := 2
	id2 := mustSubmit(t, ts, htmlVariant("A", "a"), htmlVariant("B", "b"))
	resp := postJSON(t, ts, "/api/review", reviewIn{ID: id, Decision: store.DecisionApprove})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("裁决应 200,得 %d", resp.StatusCode)
	}
	drain(t, resp)
	// choose 带批注:唤醒路径回读的正是这份完整结果
	resp = postJSON(t, ts, "/api/review", reviewIn{ID: id2, Decision: store.DecisionChoose, Variant: &two, Comment: "选 B 但改配色"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("裁决应 200,得 %d", resp.StatusCode)
	}
	drain(t, resp)

	start := time.Now()
	var d detailOut
	decodeJSON(t, get(t, ts, "/api/submissions/"+id+"?wait=600"), &d)
	if d.Status != store.StatusReviewed || d.Decision != store.DecisionApprove {
		t.Fatalf("已审 + wait 应立即返回已审结果,得 %+v", d)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("已审 + wait 应立即返回,耗时 %v", elapsed)
	}
	decodeJSON(t, get(t, ts, "/api/submissions/"+id2+"?wait=600"), &d)
	if d.Decision != store.DecisionChoose || d.ChosenVariant != 2 || d.Comment != "选 B 但改配色" {
		t.Fatalf("已审 choose 形状错: %+v", d)
	}
	if n := s.waiters.count(); n != 0 {
		t.Fatalf("已审提交不应登记等待者,登记 %d 个", n)
	}

	// 不存在 id:立即 404,不挂起
	start = time.Now()
	if resp := get(t, ts, "/api/submissions/zzzzzz?wait=30"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在 id 带 wait 应 404,得 %d: %s", resp.StatusCode, drain(t, resp))
	} else {
		drain(t, resp)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("不存在 id 应立即 404,耗时 %v", elapsed)
	}
}
