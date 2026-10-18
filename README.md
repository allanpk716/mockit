# mockit

mock 页面审核闭环服务:AI agent 经 MCP 提交 mock 页面(单 HTML 或 zip),用户在手机或电脑上全屏对比候选并拍板,agent 贴完链接挂起等待、裁决一落即取回,全程不出对话。

Go 单二进制,双模式,零外部依赖(SQLite 驱动已 vendor,离线可构建),任何一台机器放一个 exe + 一行 MCP 配置就能用。

## 核心闭环(四步)

1. **agent 提交** — agent 调 MCP 工具 `mockit_submit`,提交候选(推荐 3~5 个,上限 1~6;单 HTML 或 zip,单文件 ≤20MB);
2. **贴地址** — submit 直接返回审核链接(主机名自动探测本机 NetBird 网段,或用 `external_url` 指定),agent 贴在对话里,用户手机或电脑(同一 NetBird 网络)点开;get_review 待审时也附审核链接,agent 随时可重新贴;
3. **手机或电脑拍板** — 用户在列表页点开提交,候选页全屏对比(底栏圆点切换候选),点「选它/通过」或「打回」弹出底部批注面板裁决(approve / reject / choose+选中的候选);打回必写批注,批注上限 2000 字,写到一半自动存草稿、裁决后清除;
4. **取裁决** — agent 贴完链接立刻调 `mockit_get_review`(传 `wait` 挂起等待),用户一裁决即拿到状态/裁决/选中候选/批注,继续干活。

多项目共用一台机器上的 mockit 时,每条提交自动记下**来源项目**(git 工作区根目录,每个 worktree 各算一个):`mockit_list` 默认只列当前项目,`mockit_get_review` 取到别的项目的提交会警告,手机首页显示项目标签并可按项目筛选。

## 快速开始

```bash
go build -o mockit.exe .   # 依赖已 vendor,离线可构建

mockit serve           # 模式一：常驻 HTTP server（页面展示、审核记录、自动清理）
mockit mcp             # 模式二：stdio MCP server（agent 提交/查询的唯一通道）
mockit version         # 打印版本，当前 0.4.0
mockit upgrade         # 手动升级（--check 只看版本，--version vX.Y.Z 指定版本含降级）
mockit install-skill   # 写入内嵌技能到 ~/.claude/skills/mockit/SKILL.md
```

`mockit serve` 启动后:

- 默认监听 `0.0.0.0:8321`;端口被占自动 +1 重试,最多尝试 10 个,全占报错退出。**实际端口以日志为准**(同时写 stderr 和 `<data>/logs/serve.log`),也写在 `server.lock` 里。
- 写 `<data>/server.lock`(JSON:port / pid / version / token / base_host / started_at),供 MCP 定位。
- Ctrl+C 或 `kill` 优雅退出;另支持 `POST /shutdown`(需 lock 内 token)。

### 典型工作流

```
agent 所在机器:  mockit 已装好(用户级 ~/.claude.json,见下节)   # 或直接跑 mockit mcp
agent:           调 mockit_submit                      # serve 未起时 MCP 自动拉起
agent → 对话:    把 submit 返回的审核页 URL 贴给用户
手机:            点开 URL 拍板
agent:           紧接着 mockit_get_review(id, wait=600)   # 挂起等待,用户一裁决即返回(宿主 60 秒上限则 wait=50 循环)
```

`mockit_submit` 返回 id + 审核页 URL + 各候选 URL。URL = 主机名(自动探测本机 NetBird 网段 100.64.0.0/10 唯一命中 IPv4,或配置的 `external_url`)+ `server.lock` 记载的实际端口(端口漂移自动跟随,配置端口永不进入 URL)。探测歧义/零命中且未配置 `external_url` 时,submit 明确报错提示配置(server 与页面本身照常可用),不会默默退化成本机地址。

手工 `mockit serve` 仍然可用；serve 已在跑时 MCP 直接复用；serve 比 MCP 旧时自动停旧起新，serve 更新时直接复用并提示重启 Claude Code（ADR 0001，升级后新旧会话不互踢）。

### 数据目录

默认 `~/.mockit/`(可用 `data_dir` 改),布局:

```
~/.mockit/
├── config.json        # 可选配置文件(固定在家目录 .mockit 下,不随 data_dir 走)
├── mockit.db          # SQLite:提交/候选/裁决记录
├── server.lock        # serve 运行时写入,MCP 据此定位端口
├── logs/serve.log     # 运行日志(同时回显 stderr)
└── <提交id>/          # 6 位小写 base36 id
    ├── v1/index.html  # 候选 1(单 HTML 直存;zip 解包到同目录,入口固定 index.html)
    ├── v2/...
    └── note.txt       # (仅存量数据)旧版说明文件;现说明 note 入库,随决策记录留档 90 天
```

## MCP 接入(CC / Claude Code)

**给 agent 的使用指南**:[AGENT-GUIDE.md](AGENT-GUIDE.md) —— 提交/取结果的完整工作流、错误应对与约束,直接交给接入方的 agent 读,不用读源码。

按 [INSTALL.md](INSTALL.md) 装完后,二进制已进 PATH 并按平台重命名(Windows `mockit.exe` / macOS·Linux `mockit`),用户级 `~/.claude.json` 已合并 mockit——所有项目零逐项目配置,配置行四平台统一:

```json
{"mcpServers":{"mockit":{"command":"mockit","args":["mcp"]}}}
```

例外手段:需在项目内钉死版本时,项目根放 `.mcp.json`(`command` 写 mockit 可执行文件的绝对路径),或进 CC Switch 模板(见下节警告)。

MCP 面为行分隔 JSON-RPC 2.0（stdio），支持 initialize / ping / tools/list / tools/call，日志走 stderr（不污染协议通道）。工具调用并发处理（长等待不堵 ping 与其他请求），支持 `notifications/cancelled` 中断等待中的调用。四个工具：

| 工具 | 参数 | 说明 |
|---|---|---|
| `mockit_submit` | `title`（必填）、`note`（可选）、`variants`（1~6 个，每个 `label` 必填 + `html`（内联）或 `path`（本地 `.html`/`.htm`/`.zip`）二选一） | 提交待审；返回 id、状态、审核页 URL 与各候选 URL |
| `mockit_get_review` | `id`、`wait`（可选，整数秒 0~600） | 取回状态/裁决/选中候选/批注；传 `wait` 且待审时挂起至裁决或超时，超时返回待审属正常，可再调 |
| `mockit_list` | `status`（`pending`/`reviewed`，可选）、`limit`（可选）、`all`（可选，默认只列当前项目） | 提交简列（`all` 时标出来源项目） |
| `mockit_upgrade` | `version`（可选，目标版本 vX.Y.Z，含降级）、`check_only`（可选） | 升级或查看版本：从 GitHub release 下载校验替换二进制并写配套技能；仅用户明确要求时由 agent 调用 |

后台新版提醒：MCP 每 24 小时至多后台查一次最新 release，发现新版在工具结果末尾提醒一次（每进程至多一次，只提醒不自动装，失败静默）；`update_check` 配置可关。

MCP 与 serve 必须同机:MCP 读 `<data>/server.lock` 定位端口并 ping 探活。serve 未起时 **MCP 自动拉起**(两把锁分离协议:启动互斥锁保证并发冷启动只拉一个实例,实例锁由 serve 持有至退出;serve 活但无响应时工具明确报错提示人工处置)。

**dsh 侧**:dsh 的 MCP 配置格式等其 MCP 支持定案后在 NUC10 另配,此处不给样例。

### CC Switch 模板警告(必读)

CC Switch 切换供应商时会**全量覆盖** `~/.claude/settings.json`——历史事故多起,直接写进去的 MCP 配置会被抹掉。

mockit 不受影响:安装引导([INSTALL.md](INSTALL.md))把 MCP 配置写进**用户级 `~/.claude.json`**——与 CC Switch 覆盖的 `settings.json` 是不同文件,实测共存不冲突。

例外手段:若需项目内钉死版本,可放项目级 `.mcp.json`(不受 CC Switch 影响);或进 CC Switch 的模板,让它每次覆盖时带上。

## 分发到其他机器

一句话入口：让目标机器的 agent 按 release 附带的 [INSTALL.md](INSTALL.md) 装即可，人肉照做同样成立。装好以后，升级不用再手动：让 agent 调 `mockit_upgrade` 或运行 `mockit upgrade`，然后重启 Claude Code（0.2.0 及更早版本须先手动装一次 0.3.0，见 INSTALL.md「重装 / 升级」）。

release 地址:[github.com/allanpk716/mockit/releases](https://github.com/allanpk716/mockit/releases),自 v0.1.1 起,公开仓公开下载。每个 release 附四件套、同 tag 版本对齐:四平台二进制(`mockit-windows-amd64.exe` / `mockit-darwin-amd64` / `mockit-darwin-arm64` / `mockit-linux-amd64`)+ INSTALL.md + AGENT-GUIDE.md + 技能文件(`skills/mockit/SKILL.md`)。

公开下载不改变安全姿态:mockit 服务无鉴权,仍只在受信网络(NetBird 网内)运行,勿暴露公网(见下文"安全边界"节)。

## 发版(自动化)

推送 `v*` tag 触发 GitHub Actions([goreleaser](.github/workflows/release.yml)):跑全仓测试 → 版本守门 → 四平台交叉构建 → 自动发布 release(四件套 + `SHA256SUMS.txt`)。

- **打 tag 前必须把三处 `Version` 常量同步改成 tag 版本**(`main.go` / `internal/mcp/mcp.go` / `internal/server/server.go`,版本是 const,构建期注入不进去)——CI 与本地 `scripts/release.sh` 都有守门,不一致直接失败。
- 本地不想发版只想组装核对:`./scripts/release.sh`(干跑,打印完整 `gh release create` 命令不执行)。

```bash
git tag v0.4.0 && git push origin v0.4.0   # CI 接管，出 release
```

## 配置

优先级:默认值 < `~/.mockit/config.json` < 环境变量。配置文件所有键均可省略;示例:

```json
{
  "port": 8321,
  "addr": "0.0.0.0",
  "data_dir": "~/.mockit",
  "page_retention_days": 14,
  "decision_retention_days": 90,
  "external_url": "",
  "update_check": true
}
```

| 配置键(config.json) | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `port` | `MOCKIT_PORT` | `8321` | 监听端口;被占自动 +1,最多尝试 10 个 |
| `addr` | `MOCKIT_ADDR` | `0.0.0.0` | 监听地址 |
| `data_dir` | `MOCKIT_DATA_DIR` | `~/.mockit` | 数据目录;支持 `~` 展开,最终解析为绝对路径 |
| `page_retention_days` | `MOCKIT_PAGE_RETENTION_D` | `14` | 页面文件保留天数(锚点=提交时间) |
| `decision_retention_days` | `MOCKIT_DECISION_RETENTION_D` | `90` | 决策记录保留天数(锚点=审核完成时间) |
| `external_url` | (无环境变量) | 空 | 对外 URL 的主机名，**只接受裸主机名**（域名或 IP 字面量，含 IPv6；带端口或 `http://` 前缀启动即报配置错误）；留空=自动探测本机 NetBird 网段（100.64.0.0/10）唯一命中 IPv4，歧义/零命中时 submit 报错提示配置 |
| `update_check` | (无环境变量) | `true` | 新版检查开关；`false` 时 MCP 不做后台新版检查、不发新版提醒（`mockit_upgrade` 手动查询/升级仍可用） |

## HTTP 面

| 路由 | 用途 |
|---|---|
| `GET /` | 提交列表页 |
| `GET /s/{id}` | 提交详情页(候选一览、裁决状态) |
| `GET /s/{id}/v{n}` | 候选全屏对比页,底栏候选圆点 +「选它/通过」「打回」按钮,点按弹出批注面板 |
| `GET /raw/{id}/{n}/...` | 候选静态文件(严格防目录穿越) |
| `GET /api/submissions/{id}` | 提交详情 JSON(状态/裁决/批注);可选查询参数 `wait`(整数秒 0~600):待审时挂起至裁决或超时才返回,超时返回的仍是待审,客户端断开即释放等待 |
| `POST /api/review` | 提交裁决(approve / reject / choose+variant+comment);**打回必须带非空批注**,批注上限 2000 字(按字符计),违反返回 400 |
| `POST /api/pin` | 切换钉住 |
| `GET /ping` | 存活探测(返回版本) |

页面全部内嵌在二进制里(`go:embed`),无外部静态文件。

## 状态与保留期

- 状态机:`pending`(待审)→ `reviewed`(已审,**终态**);裁决后不可改,重复裁决明确报错(409)。
- 裁决:`approve` / `reject` / `choose`(choose 必须带候选序号);打回必须带非空批注,通过/选中的批注可选,批注上限 2000 字(按字符计,违反返回 400)。
- 清理节律:serve **启动立即扫一遍**,之后每 24 小时一扫。
- 页面文件 14 天(锚点=提交时间):未审超期**整条删**(目录+记录);已审超期只删页面文件,记录留档至决策保留期满,页面显示"已清理"禁用态。
- 决策记录 90 天(锚点=审核完成时间):到期删记录。
- **钉住豁免一切清理**。

## 安全边界

- **无鉴权**:所有页面与 API 不做任何登录/令牌校验(唯一例外:`/shutdown` 需 lock 内 token)。安全边界就是网络——只在 NetBird 网内可达,**不要暴露公网**。
- **零推送**:服务器不会通知用户;"有新提交待拍板"的提醒由 agent 在对话里以纯文字给出。
- **服务器不做构建**:agent 负责把页面做成最终形态(单 HTML 或 zip)再提交;serve 只存、只展示。

## 已知取舍(如实说明)

- **端口漂移后旧 URL 失效**:server 重启若发生端口漂移,此前贴出的未审链接会 404(提交数据仍在);agent 重新提交取新 URL,或用户从根路径(列表页)进入。
- **进程号复用的极端场景**:serve 异常退出后,若其进程号恰好被系统复用给无关进程,残留的 `server.lock` 不会被自动清理,需手工删除该文件——宁可保守不误清活实例。
- **MCP 拉起的 serve 因配置错误退出时**,工具报错不含 serve 的原始报错文本;手工运行 `mockit serve` 可看到具体原因。
