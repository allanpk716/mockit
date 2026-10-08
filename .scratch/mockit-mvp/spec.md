# mockit MVP 规格

来源:评审链 20261008-220508(proposal.md = rev1 快照)+ decisions.md(D1~D15)+ FINDINGS.md(F1~F7)。
术语遵守 research_things/20261008_mock审核服务器_立项设计/CONTEXT.md 词汇表(提交/候选/审核/批注/待审/已审/钉住/清理)。

## Problem Statement

用户经常在手机上开发,需要 AI agent 反复产出 mock 页面(单 HTML 或 zip)供其查看效果;现状缺"多方案对比 + 记录选择 + 结果回传"的闭环——agent 交付 mock 后,用户的选择无法结构化地回到 agent 手里,复盘"当初为什么选这个方案"也没有记录。

## Solution(用户视角)

任何 agent 通过本机 MCP 工具提交 1~N 个候选 mock,立刻得到一条提交的链接并贴在对话里;用户在手机(NetBird)打开链接,全屏查看每个候选、底部悬浮条切换对比,投票或打回并可写一句话批注;用户回对话说一声,agent 调查询工具取回裁决继续干活。历史提交与裁决留在列表页,重要提交可钉住不被自动清理。

## User Stories

1. 作为 AI agent,我想要通过 MCP 一次提交 1~N 个候选(每个为单自包含 HTML 或 zip 包),以便用户对比拍板。(D3/D4)
2. 作为 AI agent,我想要拿到提交 id 与候选清单并在对话里告知用户审核地址,以便用户直接点开——**受约束 F1:URL 拼接基址语义暂停,本夜 submit 返回不含 URL**(票 08)。
3. 作为用户,我想要在每个候选的全屏页用底部悬浮条切换候选、投票(单候选=通过/打回,多候选=选一个赢家)并写可选批注,以便不停留在页面里就完成拍板。(D10/D11)
4. 作为 AI agent,我想要调 get_review 取回裁决与批注,以便继续干活;已审为终态、不可改,要改就重新提交。(D10)
5. 作为用户,我想要列表页看到待审在前、已审折叠的历史,以便翻旧账与复盘。
6. 作为用户,我想要钉住某条提交,以便它不被自动清理删除。(D5)
7. 作为系统,我要按保留期自动清理:页面文件 14 天(锚点=提交时间)、决策记录 90 天(锚点=审核完成时间)、未钉住的待审到期整条删除、已审文件删后记录留档至 90 天,启动即扫 + 每 24 小时一扫。(D5/F3 定案/F7)
8. 作为部署者,我想要 Go 单二进制双模式(serve/mcp)零外部依赖,以便任何一台机器放一个 exe + 一行 MCP 配置就能用。(D7/D13)
9. 作为系统,我要保证每机唯一 serve 实例、MCP 与 serve 握手发现、端口被占自动 +1 漂移(最多 +10)——**受约束 F2:进程互斥/拉起协议暂停(票 07)**;本夜交付其无争议部分:lock 文件读写(端口/PID/版本/token)、ping、shutdown(token)、端口漂移绑定。
10. 作为用户,我不需要任何鉴权与服务器推送——NetBird 即门(ADR-0002),提醒由 agent 在对话里以纯文字给出(ADR-0003)。

## Implementation Decisions

- Go 单二进制:子命令 `serve`(常驻 HTTP)/ `mcp`(stdio);前端 `//go:embed` 内嵌;存储 SQLite(纯 Go 驱动 modernc.org/sqlite,已 vendor,泳道离线可构建)。
- 数据模型:submissions(id/标题/说明/状态/裁决/选中候选/批注/created_at/reviewed_at/pinned/文件是否已清)与 variants(提交 id/序号/标签/形态 html|zip/入口文件);状态机:待审→已审(终态);无覆盖更新。
- 内容管道:提交 API 收 base64;单 HTML 落为 `<data>/提交id/v{n}/index.html`;zip≤20MB、包内必须含根 `index.html`、解包拒绝路径穿越(拒绝绝对路径与 `..`),解包到同目录;单提交候选 1~6 个。服务器不做构建。
- HTTP 面:`/`(列表页)、`/s/{id}`(详情页)、`/s/{id}/v{n}`(候选全屏壳页,iframe 指向 `/raw/{id}/{n}/index.html`,底部悬浮条投票)、`/raw/{id}/{n}/...`(候选静态文件)、`/api/submissions`(GET 列表 / POST 提交)、`/api/submissions/{id}`(GET 详情含裁决)、`/api/review`(POST 裁决:approve|reject|choose+variant+comment)、`/api/pin`(POST 切换钉住)、`/ping`(GET,返回版本)、`/shutdown`(POST,lock token 校验)。
- 裁决规则:仅待审可裁;choose 必须带存在候选序号;裁决后写 reviewed_at;重复裁决返回明确错误。
- MCP(stdio,行分隔 JSON-RPC):initialize(回显协议版本)/tools.list/tools.call;三工具 `mockit_submit`、`mockit_get_review`、`mockit_list`,参数与返回见 rev1「MCP 工具面」;**URL 仅出现在 submit 结果且属票 08(F1),本夜 submit 结果只有 id/候选数/状态与一句"URL 功能待 F1 定案"**;get_review/list 不含 URL(F6 定案)。
- MCP 与 serve 的进程关系(拉起/互斥/版本换新)属票 07(F2 暂停);本夜 MCP 读 lock 取端口,读不到或 ping 不通时返回明确错误并提示手工运行 `mockit serve`。
- 清理:启动即扫 + 24 小时 ticker;文件级锚点=提交时间,记录级锚点=审核完成时间;未钉住待审到期→整条删(目录+记录);已审:14 天删文件(详情页候选入口转"页面已清理"禁用态),90 天删记录;钉住豁免一切。
- 配置:`~/.mockit/config.json` + 环境变量覆盖(port/addr/data_dir/页面保留期/决策保留期);`external_url` 字段本夜仅透传存储,语义(仅 host、校验拒绝带端口)属票 08。
- 锁文件 `<data>/server.lock`:JSON{port,pid,version,token,base_host,started_at};serve 绑定成功后写入(含端口漂移结果);原子写(临时文件+rename)。
- 端口漂移:配置端口起试,被占 +1,最多 +10,全占报错退出。
- Windows 拉起任何子进程必须 `SysProcAttr{HideWindow: true}`(零闪窗铁律;票 07 落地时执行,票面已注明)。
- 无鉴权;无推送;日志写 `<data>/logs/serve.log`。

## Testing Decisions

- 只测外部行为,不测内部实现细节;新仓无既有先例,测试与被测包同目录 `_test.go`。
- store:状态机(待审→已审、终态拒绝重复裁决)、清理查询锚点(文件=提交时间/记录=审核完成时间/未审整条删/钉住豁免)、pin 切换。
- server(httptest):提交管道(html 直落、zip 解包含 index.html、zip 缺 index.html 拒绝、路径穿越条目拒绝、20MB 上限、候选 1~6 边界)、裁决 API(choose 校验/重复裁决拒绝)、raw 静态服务(目录穿越拒绝)、列表/详情形状、ping/shutdown(token 对错)。
- mcp:行协议 round-trip(initialize/tools.list/tools.call)、三工具对 fake HTTP server 的 marshaling、lock 缺失时的错误路径。
- 端口漂移:起两个 httptest 监听冲突场景用真实端口绑定测试绑定循环(小号集成测试)。
- 并发首启/启动互斥/崩溃接管:属票 07 验收,解锁后补。

## Out of Scope

- 共识清单"明确不做":跨机汇总、服务器构建、覆盖更新、鉴权、推送、集中部署、用户体系。
- 票 07(启动互斥/拉起协议,F2)与票 08(URL 基址/external_url 语义,F1):暂停,不实施。
- zip 解压总量上限(F4 非阻断建议):未获采纳,不实施。
- 基址惰性重探(F5):不实施,留 Further Notes。

## Further Notes

- 活动约束与受影响行为:F1 → 票 08(URL 拼接/基址探测/external_url 校验),解除条件=FINDINGS F1;F2 → 票 07(serve 拉起互斥/版本换新/HideWindow spawn/并发首启验收),解除条件=FINDINGS F2。两票解锁路径:明早交互模式跑一轮修订+复审(`/xcheck` 续链),或用户直接批准协议定案后再续夜链。
- 评审残留非阻断参考:F4(zip 解压总量)、F5(NetBird 晚于 serve 启动时基址停零命中,重启恢复;票 08 实施时可顺带惰性重探)。
- dsh 侧 MCP 配置格式实施完成后在 NUC10 另配;CC 侧 MCP 配置必须进 CC Switch 模板或项目 `.mcp.json`(CC Switch 全量覆盖 settings.json 的坑,Ferryman/orca 同款)。
- 手机访问形态:`http://<该机 NetBird IP>:<实际端口>/s/{id}`,地址由 agent 贴出(票 08 解锁后自动拼接)。
