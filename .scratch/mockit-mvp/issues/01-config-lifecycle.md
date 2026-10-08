# 票 01 · 配置加载与锁文件助手

## What to build

- `internal/config` 完整实现:`Load(flags)` 读 `~/.mockit/config.json`(不存在用默认)+ 环境变量覆盖 `MOCKIT_PORT / MOCKIT_ADDR / MOCKIT_DATA_DIR / MOCKIT_PAGE_RETENTION_D / MOCKIT_DECISION_RETENTION_D`;`DataDir` 默认 `~/.mockit` 并解析为绝对路径(含家目录展开);配置文件键:port/addr/data_dir/page_retention_days/decision_retention_days/external_url(external_url 仅透传存储,语义属票 09,本票不做校验)。
- `internal/lifecycle` 实现:`ReadLock`(坏 JSON/缺失返回明确错误)、`WriteLock`(原子写:临时文件+rename)、`Ping(dataDir)`(GET `127.0.0.1:{lock.port}/ping`,500ms 超时,返回是否活着与版本)、`Shutdown(dataDir)`(POST `/shutdown` 带 lock token)。
- 术语:lock 文件 = `<data>/server.lock`,JSON 结构见 `internal/lifecycle/lifecycle.go` 既有注释。

## 验收标准

- [ ] config:默认值、文件值、env 覆盖优先级、家目录展开,单测全覆盖
- [ ] lifecycle:lock 读写 round-trip、坏 JSON 报错、原子写(不留半个文件);Ping 通/不通两路(httptest);Shutdown token 携带正确(httptest 断言)
- [ ] `go build ./...` 与 `go vet ./...` 绿;`go test ./internal/config/ ./internal/lifecycle/` 全过
- [ ] 不改动涉及路径外任何文件

## Blocked by

无,可立即开始

## 涉及路径

- internal/config/(目录,含新增测试文件)
- internal/lifecycle/(目录,含新增测试文件)

## 副作用声明

- 独占验证命令:`go test ./internal/config/ ./internal/lifecycle/`
- 测试仅用 httptest/临时目录,不监听固定端口,不联网

## decision_refs

D13(零外部依赖)、D8(lock 承载握手信息)

## review_blocks

无
