# 票 08 · 启动互斥与拉起协议(锁分离)

> 状态:ready(2026-10-09 解锁——用户定案 D17 + round2 复审确认 F2 解除,环 20261009-081219)。

## What to build

按锁分离协议(D17,spec「两把锁」「MCP 与 serve 的进程关系」两节)实现 serve 启动互斥与 MCP ensure-server:

1. **start.lock(启动互斥锁)**:OS 级排他文件锁(Windows LockFileEx / POSIX flock),只由 MCP ensure-server 持有,罩"复查 ping→spawn serve→等实例锁就绪"临界区,默认 30s 超时;超时拿不到→锁外轮询等 server.lock 出现(轮询带超时常量,F10)。serve 进程永不接触此锁。
2. **server.lock(实例占有锁)**:serve 绑定成功后 O_CREATE|O_EXCL 创建 + 立即取 OS 级排他锁持有至进程退出(进程死=OS 自动释放);记录 {port,pid,version,token,base_host,started_at} 写全后 fsync;O_EXCL 失败=他人已建→复查后输家自行退出(打印"已有实例在端口 X");写锁失败→关监听退出非零,不留孤儿端口。现行"临时文件+rename 原子写"实现替换为本协议。
3. **serve 启动序列**:查实例锁——活持有且 ping 通→自行退出;活持有且 ping 不通→短暂退避后退出(不清理);无持有者残留(OS 锁可获取 且 PID 死 且 ping 不通三条件同立)→清残骸继续→绑端口+探测 base_host→建锁写记录。
4. **MCP ensure-server**:读 server.lock——活持有 ping 通→版本一致复用;不一致→带 token 调 /shutdown 停旧并等确认退出(带超时常量,F10)后才清残骸,停旧失败不清理活持有;冷启动→取 start.lock→复查 ping→spawn serve(detached + Windows 必须 `SysProcAttr{HideWindow:true}` 零闪窗)→等实例锁就绪→释放 start.lock→超时按被拉起进程死活分别处理;**serve 活但 ping 不通→报错返回提示人工处置(F9)**。
5. **错误文本红线**:停旧 /shutdown 路径的错误文本必须洗 `url.Error`(剥壳,不得把 `http://127.0.0.1:端口` 泄漏进工具输出,踩 F1 红线;lifecycle.Shutdown 现存未洗点一并修)。
6. 顺带修 main.go 注释("自动确保本机 serve 存活"与现实现不符→按本票实现后语义改写)。

## 验收标准

- [ ] 双 MCP 同时冷启动:只产生一个 serve(第二个在 start.lock 排队,拿到后复查即复用)
- [ ] serve 启动中崩溃(spawn 后写锁前死):MCP 等待超时+检出进程死→干净报错,无残骸、无双实例
- [ ] 锁半写(创建后写全前崩溃):下一轮按无持有者残留清理接管;活持有(锁被活进程持有或 PID 活)不被误清
- [ ] 绑定成功写锁失败:serve 关监听退出非零,不留孤儿端口
- [ ] 手工 serve 遇已活实例:打印已有端口后干净退出
- [ ] 版本不符:停旧→等确认退出→清残→拉新;停旧失败报错不清活持有
- [ ] 错误文本无内部 URL 泄漏(serve-down 断言带 !contains("http"))
- [ ] spawn 无闪窗(HideWindow),全仓 go test ./... 全绿

## Blocked by

票 03(已完成)

## 涉及路径

- internal/lifecycle/
- internal/mcp/
- internal/server/
- main.go(仅注释修正)

## 副作用声明

- `go test ./...`(握手四场景+lifecycle/mcp 全量;独占)

## decision_refs

D7、D8、D17

## review_blocks

F2(已解除)、F9、F10(随本票落地)
