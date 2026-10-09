# 票 09 · URL 基址与 external_url 语义

> 状态:ready(2026-10-09 解锁——用户定案 D16 + round2 复审确认 F1 解除,环 20261009-081219)。

## What to build

按 D16(spec「MCP」URL 段与「配置」节)实现对外 URL 的主机名三层解析与拼接:

1. **external_url 校验**:只接受裸主机名(域名或 IP 字面量,含 IPv6);带端口或带 scheme(http:// 等)启动即报配置错误,不接受也不静默剥离(校验在 serve 启动时前置执行)。
2. **三层主机名解析**:external_url(裸主机名)> 自动探测本机网卡 100.64.0.0/10 唯一命中 IPv4 > 歧义/零命中。探测时机=serve 绑端口写实例锁时一并写 base_host(票 08 已承载);MCP 只读锁不自探。
3. **URL 拼接唯一规则**:`http://` + 主机名 + `:` + 实例锁记载实际端口;配置端口永不进入 URL;端口漂移自动跟随;**IPv6 字面量序列化为 `http://[address]:port`(F8,RFC 3986)**。
4. **submit 返回**:id + url + variant_urls;基址不可定(歧义/零命中且未配置)时 submit 返回明确错误("无法确定手机可达地址,请在 config 配 external_url(只填域名或 IP,不带端口)"),不得默默退化 127.0.0.1/机器名;get_review/list 不含 URL(F6)。
5. 顺带修 config.go 两处注释笔误(票号引用错、漂移数"+10"应为"最多尝试 10 个含起始")。

## 验收标准

- [ ] external_url 校验:裸域名/裸 IPv4/裸 IPv6 通过;host:8321、http://host、https://host 启动报配置错误
- [ ] 自动探测唯一命中 NetBird 段 IPv4→用作主机名;多命中/零命中且未配置→仅 submit 报错,server 照常启动、页面照常可审
- [ ] 拼接:URL 端口=实例锁实际端口,端口漂移后 submit 新 URL 跟随
- [ ] IPv6 用例:配 `fd00::1` 时输出 `http://[fd00::1]:<实际端口>`(F8)
- [ ] 错误文本不泄漏内部 URL
- [ ] 全仓 go test ./... 全绿

## Blocked by

票 03(已完成)、票 08(base_host 由实例锁承载,接口先定)

## 涉及路径

- internal/server/
- internal/mcp/
- internal/lifecycle/(base_host 字段消费)
- internal/config/

## 副作用声明

- `go test ./internal/server/ ./internal/config/`(与票 08 的全量测试串行执行)

## decision_refs

D2、D8、D16

## review_blocks

F1(已解除)、F6、F8(随本票落地)
