package server

// 票 09:详情 API 的等待长轮询——挂起至提交被裁决或超时;裁决成功一侧在
// 进程内唤醒等待者(不轮询数据库);客户端断开(请求 ctx)即释放。

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"mockit/internal/store"
)

// maxWaitSeconds 详情 API wait 参数上限(秒),0~600。
const maxWaitSeconds = 600

// parseWaitSeconds 解析可选查询参数 wait(秒):缺省或空串为 0(立即返回,
// 与 limit 参数同口径);非 0~600 的整数返回 ok=false。
func parseWaitSeconds(r *http.Request) (int, bool) {
	ws := r.URL.Query().Get("wait")
	if ws == "" {
		return 0, true
	}
	n, err := strconv.Atoi(ws)
	if err != nil || n < 0 || n > maxWaitSeconds {
		return 0, false
	}
	return n, true
}

// reviewWaiters 按提交 id 登记详情长轮询等待者。每个等待者一个私有通道,
// wake 用 close 广播(订阅晚于唤醒也能从已关闭通道立即读到)。
type reviewWaiters struct {
	mu   sync.Mutex
	byID map[string]map[chan struct{}]struct{}
}

func newReviewWaiters() *reviewWaiters {
	return &reviewWaiters{byID: make(map[string]map[chan struct{}]struct{})}
}

// subscribe 登记一个等待者,返回其唤醒通道(被关闭即已裁决)。
func (w *reviewWaiters) subscribe(id string) chan struct{} {
	ch := make(chan struct{})
	w.mu.Lock()
	defer w.mu.Unlock()
	set, ok := w.byID[id]
	if !ok {
		set = make(map[chan struct{}]struct{})
		w.byID[id] = set
	}
	set[ch] = struct{}{}
	return ch
}

// unsubscribe 注销等待者(请求返回时);wake 已清空登记时为无害空操作。
func (w *reviewWaiters) unsubscribe(id string, ch chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	set, ok := w.byID[id]
	if !ok {
		return
	}
	delete(set, ch)
	if len(set) == 0 {
		delete(w.byID, id)
	}
}

// wake 唤醒该提交的全部等待者并清空登记(裁决成功路径调用)。
func (w *reviewWaiters) wake(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	set, ok := w.byID[id]
	if !ok {
		return
	}
	delete(w.byID, id)
	for ch := range set {
		close(ch)
	}
}

// count 当前登记的等待者总数(测试观察断开释放/无泄漏用)。
func (w *reviewWaiters) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, set := range w.byID {
		n += len(set)
	}
	return n
}

// awaitReview 挂起等待提交被裁决:先订阅再复查状态(裁决发生在「外层
// 取状态」与「订阅」之间也不漏);被裁决唤醒或超时即返回复查后的最新
// 提交,客户端断开返回 ctx.Err()(调用方不再写响应)。
func (s *Server) awaitReview(ctx context.Context, id string, waitSecs int) (*store.Submission, error) {
	ch := s.waiters.subscribe(id)
	defer s.waiters.unsubscribe(id, ch)
	sub, err := s.store.Get(id)
	if err != nil || sub.Status != store.StatusPending {
		return sub, err // 已审(或记录已清理):按当前事实立即返回
	}
	timer := time.NewTimer(time.Duration(waitSecs) * time.Second)
	defer timer.Stop()
	select {
	case <-ch: // 裁决成功路径在进程内唤醒
	case <-timer.C: // 超时:按票面返回当前(待审)状态
	case <-ctx.Done(): // 客户端断开:释放等待,响应无处可写
		return nil, ctx.Err()
	}
	return s.store.Get(id)
}
