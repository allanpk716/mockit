# 票 06 · MCP stdio 协议与三工具

## What to build

`internal/mcp` 的 `Run(cfg)` 完整实现:

- stdio 行分隔 JSON-RPC:`initialize`(回显请求的 protocolVersion,缺省 "2024-11-05";capabilities.tools;serverInfo {name:"mockit",version})、`notifications/initialized` 不回应、`ping`→`{}`、`tools/list`、`tools/call`;未知方法标准错误码;stdout 只走协议,日志一律 stderr。
- 工具(参数 schema 按 spec「MCP 工具面」):
  - `mockit_submit {title, note?, variants:[{label, html?|path?}]}`:html 内联直用;path 读本地文件(`.html/.htm`→html;`.zip`→zip),单文件≤20MB,base64 后 POST `http://127.0.0.1:{port}/api/submissions`;成功结果文本 = 提交 id、候选数、状态 + 一句"URL 功能待评审约束 F1 定案后启用(票 09),暂无链接"——**本工具不得拼接/返回任何 URL**
  - `mockit_get_review {id}` → GET `/api/submissions/{id}`,结果含状态/裁决/选中候选/批注
  - `mockit_list {status?, limit?}` → GET `/api/submissions`
- serve 定位:`lifecycle.ReadLock(cfg.DataDir)` 取端口,`127.0.0.1:{port}`;lock 缺失或 `Ping` 不通 → 工具返回明确错误:"serve 未运行,请先手工运行 mockit serve"(进程拉起/互斥/版本换新属票 08,本票绝不 spawn 进程)。

## 验收标准

- [ ] 协议测试:initialize→tools/list→tools/call 三步 round-trip;notifications 无响应;坏 JSON 行不崩(跳过并 stderr 记录)
- [ ] submit/get_review/list 对 fake httptest server 全通(含 b64 提交落库断言)
- [ ] lock 缺失/ping 不通两路错误文案
- [ ] `go test ./internal/mcp/` 全过;`go build ./...` 绿

## Blocked by

票 01

## 涉及路径

- internal/mcp/(目录,含测试)

## 副作用声明

- 独占验证命令:`go test ./internal/mcp/`
- 测试用 httptest 与临时 lock 文件,不 spawn 进程,不联网

## decision_refs

D3(MCP 唯一通道)、D6(零推送,纯文字结果)、F6(get_review/list 不含 URL)

## review_blocks

无
