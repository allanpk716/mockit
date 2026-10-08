# mockit

mock 页面审核闭环服务:AI agent 经 MCP 提交 mock(单 HTML / zip),用户在手机(NetBird)上全屏对比候选并拍板,agent 经 MCP 取回结果。

- 设计文档与决策记录:`research_things/20261008_mock审核服务器_立项设计/`
- 规格与任务票(本夜链):`.scratch/mockit-mvp/`
- 双模式:`mockit serve`(常驻 HTTP server)/ `mockit mcp`(stdio MCP)
- 无鉴权(NetBird 即门)、零推送(提醒归 agent)、服务器不做构建

> 状态:夜链分支 `xcheck-night-*` 上施工中;启动互斥协议与 URL 基址语义两票暂停待评审约束解除(见晨报)。
