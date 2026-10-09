# 票 03 · README 更新:分发节 + CC Switch 节改措辞 + 版本对齐

## What to build

更新 `README.md` 三处:新增"分发到其他机器"节;"CC Switch 模板警告"节按用户级为主改措辞;全文与 INSTALL.md/版本号口径对齐。其余各节(核心闭环/数据目录/HTTP 面/状态保留/安全边界/已知取舍)不动。

## 验收标准

- [ ] 新增"分发到其他机器"节(建议放在"MCP 接入"节之后):一句话入口(让目标机器的 agent 按 release 附带的 INSTALL.md 装)+ release 地址(github.com/allanpk716/mockit/releases)+ 四件套清单一句(四平台二进制/INSTALL/AGENT-GUIDE/skill)
- [ ] "CC Switch 模板警告"节改措辞:安装引导走**用户级** `~/.claude.json`(与 CC Switch 覆盖的 settings.json 是不同文件,实测共存);项目级 `.mcp.json` 降为"例外手段"(如需项目内钉死版本时)
- [ ] "MCP 接入(CC / Claude Code)"节与 INSTALL 口径一致:配置行统一 `{"command":"mockit","args":["mcp"]}`(二进制已进 PATH 并按平台重命名);保留"或进 CC Switch 模板"的旧选项不删
- [ ] 版本口径对齐:`mockit version` 示例输出从"当前 0.1.0"改为 0.1.1(与发版对齐);发版相关新句不承诺公网支持
- [ ] 安全边界节保持事实句口径(默认监听 0.0.0.0、无鉴权、网络即边界)——已有表述与新 INSTALL 不矛盾,矛盾处以事实句修正
- [ ] "已知取舍"节内容不动;"dsh 侧"挂账句保留
- [ ] 不改动 README 之外的文件

## Blocked by

无,可立即开始。

## 涉及路径

- README.md

## 副作用声明

无(纯文档;不跑构建/测试)。

decision_refs: D11, D12
review_blocks: 无
