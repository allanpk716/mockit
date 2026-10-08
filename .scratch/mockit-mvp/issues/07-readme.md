# 票 07 · README 与配置样例

## What to build

`README.md` 终稿(与实际行为一致):

- 定位一句话 + 核心闭环四步
- 快速开始:`mockit serve` / `mockit mcp` / `mockit version`;数据目录与配置表(端口默认 8321、监听 0.0.0.0、保留期 14/90、external_url 待票 09 注明暂无语义)
- CC 的 `.mcp.json` 样例(command/args 指向 mockit mcp,可直接粘贴);dsh 侧配置提示(NUC10 按 dsh 的 MCP 支持另配);**CC Switch 模板警告**(配置须进模板或项目 .mcp.json,防全量覆盖抹掉)
- 安全边界:无鉴权(NetBird 即门)、零推送(提醒归 agent)、服务器不做构建
- 夜链状态说明:票 08/09 暂停待评审约束解除

## 验收标准

- [ ] 文档与实现一致:命令、端口、配置键、保留期、状态语义逐一对照代码核实
- [ ] `.mcp.json` 样例 JSON 合法可粘贴
- [ ] 无承诺未实现的行为(URL 拼接/自动拉起须写明"待启用")

## Blocked by

票 03、票 04、票 06

## 涉及路径

- README.md

## 副作用声明

- 纯文档,默认只跑 `go build ./...` 冒烟

## decision_refs

D7、D9、D12、D13

## review_blocks

无
