# 票 09 · URL 基址与 external_url 语义【暂停:评审约束 F1】

> 状态:paused(F1)。解除条件见 `.xcheck/20261008-220508/FINDINGS.md` F1:修订定案 external_url 仅接受 host(校验拒绝带端口/scheme)、对外 URL=host+实际端口,并经复审确认。本票今夜不实施;在它解锁前,任何代码不得拼接对外 URL。

## What to build(解锁后)

基址解析三层(external_url 仅 host > 自动探测 NetBird 100.64.0.0/10 唯一命中 > 歧义/零命中时 submit 明确报错指引配置)、lock.base_host 承载、submit/get_review 返回 URL=基址+实际端口、探测时机随 lock 写入。

## 验收标准(解锁后)

- [ ] 三层解析与歧义报错测试;URL 拼接随端口漂移跟随

## Blocked by

票 03

## 涉及路径

- internal/server/
- internal/mcp/
- internal/lifecycle/
- internal/config/

## 副作用声明

- `go test ./internal/server/`

## decision_refs

D2、D8

## review_blocks

F1
