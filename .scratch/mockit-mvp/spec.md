# mockit MVP 规格

来源:评审链 20261009-081219(proposal.md = rev2 快照,收敛·2 轮修订)+ decisions.md(D1~D17;D16/D17=2026-10-09 用户定案)+ FINDINGS.md(F1~F12;F1/F2 已解除,F8~F12 非阻断新录)。
术语遵守 research_things/20261008_mock审核服务器_立项设计/CONTEXT.md 词汇表(提交/候选/审核/批注/待审/已审/钉住/清理)。

## Problem Statement

用户经常在手机上开发,需要 AI agent 反复产出 mock 页面(单 HTML 或 zip)供其查看效果;现状缺"多方案对比 + 记录选择 + 结果回传"的闭环——agent 交付 mock 后,用户的选择无法结构化地回到 agent 手里,复盘"当初为什么选这个方案"也没有记录。

## Solution(用户视角)

任何 agent 通过本机 MCP 工具提交 1~N 个候选 mock,立刻得到一条提交的链接并贴在对话里;用户在手机(NetBird)打开链接,全屏查看每个候选、底部悬浮条切换对比,投票或打回并可写一句话批注;用户回对话说一声,agent 调查询工具取回裁决继续干活。历史提交与裁决留在列表页,重要提交可钉住不被自动清理。

## User Stories

1. 作为 AI agent,我想要通过 MCP 一次提交 1~N 个候选(每个为单自包含 HTML 或 zip 包),以便用户对比拍板。(D3/D4)
2. 作为 AI agent,我想要提交后拿到 id 与候选 URL 并在对话里告知用户审核地址,以便用户直接点开;URL=主机名(external_url 裸主机名 > NetBird 自动探测)+实际端口,漂移自动跟随(D16;票 09)。
3. 作为用户,我想要在每个候选的全屏页用底部悬浮条切换候选、投票(单候选=通过/打回,多候选=选一个赢家)并写可选批注,以便不停留在页面里就完成拍板。(D10/D11)
4. 作为 AI agent,我想要调 get_review 取回裁决与批注,以便继续干活;已审为终态、不可改,要改就重新提交。(D10)
5. 作为用户,我想要列表页看到待审在前、已审折叠的历史,以便翻旧账与复盘。
6. 作为用户,我想要钉住某条提交,以便它不被自动清理删除。(D5)
7. 作为系统,我要按保留期自动清理:页面文件 14 天(锚点=提交时间)、决策记录 90 天(锚点=审核完成时间)、未钉住的待审到期整条删除、已审文件删后记录留档至 90 天,启动即扫 + 每 24 小时一扫。(D5/F3 定案/F7)
8. 作为部署者,我想要 Go 单二进制双模式(serve/mcp)零外部依赖,以便任何一台机器放一个 exe + 一行 MCP 配置就能用。(D7/D13)
9. 作为系统,我要保证每机唯一 serve 实例、MCP 与 serve 握手发现、端口被占自动 +1 漂移(最多 +10);进程互斥/拉起=锁分离协议(D17;票 08):start.lock 只由 MCP ensure-server 持有罩拉起临界区,server.lock 由 serve O_EXCL 创建并持 OS 锁至退出,四类并发场景为验收。
10. 作为用户,我不需要任何鉴权与服务器推送——NetBird 即门(ADR-0002),提醒由 agent 在对话里以纯文字给出(ADR-0003)。

## Implementation Decisions

- Go 单二进制:子命令 `serve`(常驻 HTTP)/ `mcp`(stdio);前端 `//go:embed` 内嵌;存储 SQLite(纯 Go 驱动 modernc.org/sqlite,已 vendor,泳道离线可构建)。
- 数据模型:submissions(id/标题/说明/状态/裁决/选中候选/批注/created_at/reviewed_at/pinned/文件是否已清)与 variants(提交 id/序号/标签/形态 html|zip/入口文件);状态机:待审→已审(终态);无覆盖更新。
- 内容管道:提交 API 收 base64;单 HTML 落为 `<data>/提交id/v{n}/index.html`;zip≤20MB、包内必须含根 `index.html`、解包拒绝路径穿越(拒绝绝对路径与 `..`),解包到同目录;单提交候选 1~6 个。服务器不做构建。
- HTTP 面:`/`(列表页)、`/s/{id}`(详情页)、`/s/{id}/v{n}`(候选全屏壳页,iframe 指向 `/raw/{id}/{n}/index.html`,底部悬浮条投票)、`/raw/{id}/{n}/...`(候选静态文件)、`/api/submissions`(GET 列表 / POST 提交)、`/api/submissions/{id}`(GET 详情含裁决)、`/api/review`(POST 裁决:approve|reject|choose+variant+comment)、`/api/pin`(POST 切换钉住)、`/ping`(GET,返回版本)、`/shutdown`(POST,lock token 校验)。
- 裁决规则:仅待审可裁;choose 必须带存在候选序号;裁决后写 reviewed_at;重复裁决返回明确错误。
- MCP(stdio,行分隔 JSON-RPC):initialize(回显协议版本)/tools.list/tools.call;三工具 `mockit_submit`、`mockit_get_review`、`mockit_list`,参数与返回见 rev2「MCP 工具面」;URL 仅出现在 submit 结果:三层主机名(external_url 裸主机名 > NetBird 100.64.0.0/10 唯一命中 > 歧义/零命中仅 submit 明确报错)+实例锁实际端口,配置端口永不进入 URL(D16);IPv6 字面量按 `http://[address]:port` 序列化(F8);get_review/list 不含 URL(F6 定案)。
- MCP 与 serve 的进程关系=锁分离协议(D17,票 08):ensure-server——读 server.lock:活持有 ping 通→版本一致复用;不一致→token 停旧并等确认退出后才清残骸(停旧失败不清理活持有);冷启动→取 start.lock(默认 30s 超时,超时改锁外轮询,轮询也带超时)→复查 ping→spawn serve(HideWindow)→等实例锁就绪(默认 30s 超时)→释放 start.lock→超时按被拉起进程死活分别处理;serve 活但 ping 不通→报错返回提示人工处置(F9);停旧等待与锁外轮询补默认超时常量(F10)。当前过渡实现(读不到 lock 即报错提示手工 serve)由票 08 替换。
- 清理:启动即扫 + 24 小时 ticker;文件级锚点=提交时间,记录级锚点=审核完成时间;未钉住待审到期→整条删(目录+记录);已审:14 天删文件(详情页候选入口转"页面已清理"禁用态),90 天删记录;钉住豁免一切。
- 配置:`~/.mockit/config.json` + 环境变量覆盖(port/addr/data_dir/页面保留期/决策保留期);`external_url` 只接受裸主机名(域名或 IP 字面量,含 IPv6),带端口或 scheme 启动即报配置错误,不接受也不静默剥离(D16;票 09 校验)。
- 两把锁(D17,票 08):`<data>/start.lock`=启动互斥锁,OS 级排他(Windows LockFileEx/POSIX flock),只由 MCP ensure-server 持有,serve 永不接触;`<data>/server.lock`=实例占有锁兼记录,JSON{port,pid,version,token,base_host,started_at},serve 绑定成功后 O_CREATE|O_EXCL 创建+立即取 OS 级排他锁持有至进程退出(进程死=OS 自动释放),记录写全后 fsync;O_EXCL 失败=他人已建→复查后输家自行退出;写锁失败→关监听退出非零。活持有判据=OS 锁获取失败或 PID 存活(任一即按活持有,保守优先);清理三条件=OS 锁可获取且 PID 死且 ping 不通(同时成立才清);PID 复用致无法自动接管=接受的保守降级(F12 留底)。
- 端口漂移:配置端口起试,被占 +1,最多 +10,全占报错退出。
- Windows 拉起任何子进程必须 `SysProcAttr{HideWindow: true}`(零闪窗铁律;票 08 落地时执行,票面已注明)。
- 无鉴权;无推送;日志写 `<data>/logs/serve.log`。

## Testing Decisions

- 只测外部行为,不测内部实现细节;新仓无既有先例,测试与被测包同目录 `_test.go`。
- store:状态机(待审→已审、终态拒绝重复裁决)、清理查询锚点(文件=提交时间/记录=审核完成时间/未审整条删/钉住豁免)、pin 切换。
- server(httptest):提交管道(html 直落、zip 解包含 index.html、zip 缺 index.html 拒绝、路径穿越条目拒绝、20MB 上限、候选 1~6 边界)、裁决 API(choose 校验/重复裁决拒绝)、raw 静态服务(目录穿越拒绝)、列表/详情形状、ping/shutdown(token 对错)。
- mcp:行协议 round-trip(initialize/tools.list/tools.call)、三工具对 fake HTTP server 的 marshaling、lock 缺失时的错误路径。
- 端口漂移:起两个 httptest 监听冲突场景用真实端口绑定测试绑定循环(小号集成测试)。
- 启动互斥四场景(票 08 验收):双 MCP 同时冷启动只出一个 serve / serve 启动中崩溃干净报错无残骸 / 锁半写按无持有者残留接管不误清活持有 / 绑定成功写锁失败关监听退出非零。
- URL 基址与拼接(票 09 验收):三层主机名解析(external_url 校验拒绝端口与 scheme、NetBird 唯一命中、歧义/零命中仅 submit 报错)、漂移跟随、IPv6 bracket 序列化用例(F8)。

## Out of Scope

- 共识清单"明确不做":跨机汇总、服务器构建、覆盖更新、鉴权、推送、集中部署、用户体系。
- 票 08(启动互斥/拉起协议)与票 09(URL 基址/external_url 语义)已解锁实施(2026-10-09 D16/D17 定案+round2 复审解除)。
- F11(端口漂移后旧 submit URL 失效=固有取舍,不做重定向)与 F12(PID 复用保守降级,不加进程身份校验):留底不实施。
- zip 解压总量上限(F4 非阻断建议):未获采纳,不实施。
- 基址惰性重探(F5):不实施,留 Further Notes。

## Further Notes

- 约束解除记录:F1/F2 经用户定案(D16/D17)+rev2 修订+round2 双家复审确认解除(环 20261009-081219,收敛·2 轮修订);F8(IPv6 bracket)/F9(ping 不通分支)/F10(超时常量)已并入票 09/08 验收;F4/F5/F11/F12 非阻断留底。
- 评审残留非阻断参考:F4(zip 解压总量)、F5(NetBird 晚于 serve 启动时基址停零命中,重启恢复;票 08 实施时可顺带惰性重探)。
- dsh 侧 MCP 配置格式实施完成后在 NUC10 另配;CC 侧 MCP 配置必须进 CC Switch 模板或项目 `.mcp.json`(CC Switch 全量覆盖 settings.json 的坑,Ferryman/orca 同款)。
- 手机访问形态:`http://<主机名>:<实际端口>/s/{id}`,地址由 agent 贴出(票 09 落地后 submit 自动拼接)。
