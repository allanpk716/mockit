# 票 02 · 安装引导:INSTALL.md(agent 可照做、人肉可照做)

## What to build

新增仓根 `INSTALL.md`:安装执行者是**接入方 agent**——新机器上用户对 agent 说一句"按 mockit 最新 release 的 INSTALL.md 装",agent 照文档执行;人肉照做同样成立。内容按规格的 INSTALL 内容契约。

## 验收标准

- [ ] 开头一句入口话术(用户对 agent 说什么)与适用范围(自家受信网络机器;支持 MCP 的 agent)
- [ ] **判平台→下载**:Windows/macOS(Intel=amd64/Apple=arm64)/Linux 与 release 资产名映射(mockit-windows-amd64.exe / mockit-darwin-amd64 / mockit-darwin-arm64 / mockit-linux-amd64);给 gh release download 与 curl 两种取法
- [ ] **重命名(强制)**:Windows → `mockit.exe`;macOS/Linux → `mockit`;Unix 安装后 `chmod +x`
- [ ] **PATH**:Windows 用 PowerShell `[Environment]::GetEnvironmentVariable('Path','User')` 读用户级持久值、保留既有条目、追加安装目录、`SetEnvironmentVariable(...,'User')` 写回——明确禁止 setx(1024 截断)、禁止读进程合并值($env:Path);macOS/Linux 放 `~/bin`(不存在则建并写入 shell 配置)或 `/usr/local/bin`
- [ ] **MCP 配置(用户级 ~/.claude.json,强制合并纪律)**:写前备份 → 结构化读取 → 仅在 `mcpServers` 下合并/更新 `mockit` 一个键(值 `{"command":"mockit","args":["mcp"]}`)→ JSON 校验 → 写回;**明文禁止整文件模板替换**;给 PowerShell 与 python 两种合并示例
- [ ] **装技能**:把 release 附带的 skills/mockit/SKILL.md 放到 `~/.claude/skills/mockit/SKILL.md`(目录不存在则建)
- [ ] **两级验收**:立即验收 = 用下载文件**绝对路径**运行 `version`,输出必须等于 release tag(如 0.1.1),不依赖 PATH;最终验收 = **重启 Claude Code 进程**(仅开新会话不够)后新会话见 `mockit_submit`/`mockit_get_review`/`mockit_list` 三工具,可选冒烟一次 submit
- [ ] **安全事实句**:"服务默认监听 0.0.0.0 且无鉴权——安全边界是网络拓扑,只在受信网络(如 NetBird)运行,勿暴露公网"
- [ ] **排障**:NetBird 前提(手机与机器同网才可达);external_url 配置(裸主机名,不带端口/scheme;探测歧义报错时怎么办);端口漂移旧链接失效→重新提交;serve 日志位置
- [ ] 全文步骤编号可顺序执行,每步给具体命令(三大平台分别给);不自动创建 release、不假设仓存在
- [ ] 术语用 CONTEXT.md 词汇(接入方/用户/提交/候选/裁决)

## Blocked by

无,可立即开始。

## 涉及路径

- INSTALL.md(新增)

## 副作用声明

无(纯文档;不跑构建/测试)。

decision_refs: D4, D6, D9, D10, D11, D12(含 rev1 决策 7/8/11 的强制项)
review_blocks: 无
