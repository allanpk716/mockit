# 票 05 · 清理调度

## What to build

- `internal/cleanup`:`Start(st store.StoreAPI, pageDays, decisionDays int, nowFn func() time.Time) chan struct{}`——启动立即扫一遍,之后每 24 小时一扫,循环执行:
  1. `DueFileCleanup(now, pageDays)` 返回的整条删条目 → 删除 `<data>/{id}` 目录 + `DeleteRecord`
  2. 仅文件条目 → 删除目录 + `MarkFilesDeleted`
  3. `DueRecordCleanup(now, decisionDays)` → `DeleteRecord`
  (钉住豁免已在 store 查询层实现;nowFn 注入便于测试)
- `internal/server/wire_cleanup.go`:`Serve` 启动时 `go cleanup.Start(...)`(信号退出时停止)。

## 验收标准

- [ ] 注入时钟单测:未审超期→目录与记录全删;已审超期→只删文件(记录留、files_deleted=1);reviewed_at 超 90 天→记录删;钉住→全不动
- [ ] 启动即扫(不等 24h)被断言
- [ ] `go test ./internal/cleanup/ ./internal/server/` 全过;`go build ./...` 绿

## Blocked by

票 02、票 03

## 涉及路径

- internal/cleanup/(目录,含测试)
- internal/server/wire_cleanup.go(新文件;仅此一个 server 目录内文件)

## 副作用声明

- 独占验证命令:`go test ./internal/cleanup/`

## decision_refs

D5(定期清理)、F3 定案(锚点/整条删)、F7 定案(启动即扫+24h 间隔)

## review_blocks

无
