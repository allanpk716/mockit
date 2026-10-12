// 后台新版检查(票 #16):MCP 处理工具调用时,若缓存已过期,在后台
// goroutine 里查一次最新 release,不阻塞本次调用;已知有新版(非开发版)
// 后,经每进程一次附加提示机制(notice.go)在下一个工具结果末尾附一句
// 新版提醒,只提醒、不自动装。检查失败静默;update_check=false 时完全不
// 检查、不提醒。同一进程内不并发重复检查。

package mcp

import (
	"context"
	"sync"

	"mockit/internal/upgrade"
)

// noticeKindUpdate 是新版提醒的类别键(noticeBoard 按 kind 每进程一次)。
const noticeKindUpdate = "update-available"

// updateCheck 承载后台新版检查的进程内状态:
//   - checking:有检查在飞,同进程不并发重复检查;
//   - upgraded:本进程已执行过升级,不再检查/提醒(版本常量已过时)。
type updateCheck struct {
	mu       sync.Mutex
	checking bool
	upgraded bool
}

// setUpgraded 标记本进程已完成升级(mockit_upgrade 成功路径调用)。
func (u *updateCheck) setUpgraded() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.upgraded = true
}

// maybeCheckUpdate 在工具调用处理点同步判断并按需起后台检查:开关关、
// 依赖缺失、已有检查在飞或本进程已升级时直接返回。检查本身在独立
// goroutine 里进行(Checker 缓存有效时不发网络请求),不阻塞调用。
func (s *server) maybeCheckUpdate() {
	if s.cfg == nil || !s.cfg.UpdateCheck {
		return
	}
	if s.updeps == nil || s.updeps.checker == nil {
		return
	}
	s.updateState.mu.Lock()
	if s.updateState.checking || s.updateState.upgraded {
		s.updateState.mu.Unlock()
		return
	}
	s.updateState.checking = true
	s.updateState.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logf("新版检查内部错误: %v", r)
			}
			s.updateState.mu.Lock()
			s.updateState.checking = false
			s.updateState.mu.Unlock()
		}()
		res := s.updeps.checker.Check(context.Background(), false)
		if res.State == upgrade.StateUpgradable {
			s.updateState.mu.Lock()
			upgraded := s.updateState.upgraded
			s.updateState.mu.Unlock()
			if !upgraded {
				// 开发版/已是最新不提醒;失败静默(状态非 upgradable)。
				s.notices.add(noticeKindUpdate, updateNotice(res.Latest, s.updeps.checker.Current))
			}
		}
		if s.onCheckDone != nil {
			s.onCheckDone(res)
		}
	}()
}
