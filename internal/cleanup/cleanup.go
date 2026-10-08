// Package cleanup 实现 mockit 的定期清理(D5/F3/F7)。
//
// 语义(锚点判定都在 store 查询层,钉住豁免亦然):
//   - 页面文件到期(F3:锚点=提交时间):未审超期整条删(目录+记录),
//     已审超期只删页面文件、记录留档至决策保留期满;
//   - 决策记录到期(F3:锚点=审核完成时间):整条删记录。
//
// 节律(F7 定案):启动立即扫一遍,之后每 24 小时一扫。
package cleanup

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"mockit/internal/store"
)

// DefaultInterval 生产默认扫描间隔(F7:24 小时)。
const DefaultInterval = 24 * time.Hour

// Start 启动清理循环:启动立即扫一遍,之后每 interval 扫一次
// (interval<=0 取 DefaultInterval;nowFn 为 nil 用 time.Now,供测试注入时钟)。
// 不阻塞,循环在后台 goroutine 执行;返回的 stop 阻塞至循环退出,可安全多次调用。
func Start(st *store.Store, dataDir string, pageDays, decisionDays int, lg *log.Logger, nowFn func() time.Time, interval time.Duration) (stop func()) {
	if nowFn == nil {
		nowFn = time.Now
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	stopCh := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(done)
		runOnce(st, dataDir, pageDays, decisionDays, nowFn(), lg) // F7:启动即扫,不等第一个 tick
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				runOnce(st, dataDir, pageDays, decisionDays, nowFn(), lg)
			}
		}
	}()

	return func() {
		once.Do(func() { close(stopCh) })
		<-done
	}
}

// runOnce 执行一轮清理,返回清理的条目数。单项失败记日志并跳过,不中断整轮。
func runOnce(st *store.Store, dataDir string, pageDays, decisionDays int, now time.Time, lg *log.Logger) int {
	cleaned := 0

	// 1) 页面文件到期:Whole=true 未审整条删;Whole=false 已审只删文件留记录。
	dues, err := st.DueFileCleanup(now.Unix(), pageDays)
	if err != nil {
		logf(lg, "cleanup: 查文件清理失败: %v", err)
	}
	for _, d := range dues {
		if err := os.RemoveAll(filepath.Join(dataDir, d.ID)); err != nil {
			logf(lg, "cleanup: 删目录 %s 失败: %v", d.ID, err)
			continue // 文件还在,记录同步动作(删/标记)一并跳过,下轮重试
		}
		if d.Whole {
			err = st.DeleteRecord(d.ID)
		} else {
			err = st.MarkFilesDeleted(d.ID)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			logf(lg, "cleanup: 更新记录 %s 失败: %v", d.ID, err)
			continue
		}
		cleaned++
	}

	// 2) 决策记录到期:此时页面文件按默认保留期(14<90)应已先一步清掉。
	ids, err := st.DueRecordCleanup(now.Unix(), decisionDays)
	if err != nil {
		logf(lg, "cleanup: 查记录清理失败: %v", err)
	}
	for _, id := range ids {
		if err := st.DeleteRecord(id); err != nil && !errors.Is(err, store.ErrNotFound) {
			logf(lg, "cleanup: 删记录 %s 失败: %v", id, err)
			continue
		}
		cleaned++
	}

	if cleaned > 0 {
		logf(lg, "cleanup: 本轮清理 %d 条", cleaned)
	}
	return cleaned
}

// logf 在 lg 非 nil 时输出日志(logger 为清理循环的可选依赖)。
func logf(lg *log.Logger, format string, args ...any) {
	if lg != nil {
		lg.Printf(format, args...)
	}
}
