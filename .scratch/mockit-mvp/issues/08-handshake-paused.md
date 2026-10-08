# 票 08 · 启动互斥与拉起协议【暂停:评审约束 F2】

> 状态:paused(F2)。解除条件见 `.xcheck/20261008-220508/FINDINGS.md` F2:按评审收敛骨架定案完整协议(锁角色分离或 spawn 后放锁+锁外轮询;清理接管区分"无持有者残留"与"活进程持有";启动占位带 PID)并经复审确认,或用户明确批准协议定案。本票今夜不实施。

## What to build(解锁后)

serve 启动互斥与 MCP ensure-server:原子锁临界区、serve 抢锁失败探测退出、MCP 拉起 detached serve(Windows 必须 `SysProcAttr{HideWindow:true}`)、版本不符停旧起新、并发首启/启动中崩溃/lock 半写/绑定成功写 lock 失败四类验收场景。

## 验收标准(解锁后)

- [ ] 四类场景测试全过;双 MCP 同时冷启动只产生一个 serve

## Blocked by

票 03

## 涉及路径

- internal/lifecycle/
- internal/mcp/
- internal/server/

## 副作用声明

- `go test ./...`(握手场景)

## decision_refs

D7、D8

## review_blocks

F2
