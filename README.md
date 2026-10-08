# mockit

mock 页面审核闭环服务:AI agent 经 MCP 提交 mock 页面(单 HTML 或 zip),用户在手机上全屏对比候选并拍板,agent 取回裁决,全程不出对话。

Go 单二进制,双模式,零外部依赖(SQLite 驱动已 vendor,离线可构建),任何一台机器放一个 exe + 一行 MCP 配置就能用。

## 核心闭环(四步)

1. **agent 提交** — agent 调 MCP 工具 `mockit_submit`,提交 1~6 个候选(单 HTML 或 zip,单文件 ≤20MB);
2. **贴地址** — 用户在手机浏览器打开审核页面(同一 NetBird 网络)。※ 此环节当前无自动 URL,见下文[暂未启用](#暂未启用);
3. **手机拍板** — 用户在列表页点开提交,逐个候选全屏对比,底部投票条裁决(approve / reject / choose+选中的候选),可附批注;
4. **取结果** — agent 调 `mockit_get_review` 拿到状态/裁决/选中候选/批注,继续干活。

## 快速开始

```bash
go build -o mockit.exe .   # 依赖已 vendor,离线可构建

mockit serve      # 模式一:常驻 HTTP server(页面展示、审核记录、自动清理)
mockit mcp        # 模式二:stdio MCP server(agent 提交/查询的唯一通道)
mockit version    # 打印版本,当前 0.1.0
```

`mockit serve` 启动后:

- 默认监听 `0.0.0.0:8321`;端口被占自动 +1 重试,最多尝试 10 个,全占报错退出。**实际端口以日志为准**(同时写 stderr 和 `<data>/logs/serve.log`),也写在 `server.lock` 里。
- 写 `<data>/server.lock`(JSON:port / pid / version / token / base_host / started_at),供 MCP 定位。
- Ctrl+C 或 `kill` 优雅退出;另支持 `POST /shutdown`(需 lock 内 token)。

### 典型工作流(当前形态)

```
终端 1:  mockit serve                    # 先手工起 serve(见"暂未启用")
终端 2:  mockit mcp                      # 或由 CC 经 .mcp.json 自动拉起
手机:    http://<本机 NetBird IP>:8321/  # 列表页,点开对应提交拍板
```

agent 调 `mockit_submit` 后收到的是"已提交 id=X 状态=pending(待审) 候选数=N(暂无链接)"——不含 URL,用户需按上面地址手工打开列表页点进去。

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
    └── note.txt       # 提交说明 note(非空时落盘)
```

## MCP 接入(CC / Claude Code)

项目根放 `.mcp.json`(或进 CC Switch 模板,见下节警告),可直接粘贴:

```json
{"mcpServers":{"mockit":{"command":"<mockit.exe 绝对路径>","args":["mcp"]}}}
```

`command` 按机器替换成 mockit.exe 的绝对路径,如 `"C:/WorkSpace/agent/mockit/mockit.exe"`(正斜杠即可)。

MCP 面为行分隔 JSON-RPC 2.0(stdio),支持 initialize / ping / tools/list / tools/call,日志走 stderr(不污染协议通道)。三个工具:

| 工具 | 参数 | 说明 |
|---|---|---|
| `mockit_submit` | `title`(必填)、`note`(可选)、`variants`(1~6 个,每个 `label` 必填 + `html`(内联)或 `path`(本地 `.html`/`.htm`/`.zip`)二选一) | 提交待审;返回 id 与状态,**不含 URL** |
| `mockit_get_review` | `id` | 取回状态/裁决/选中候选/批注 |
| `mockit_list` | `status`(`pending`/`reviewed`,可选)、`limit`(可选) | 提交简列 |

MCP 与 serve 必须同机:MCP 读 `<data>/server.lock` 定位端口并 ping 探活;serve 没跑时工具明确报"serve 未运行,请先手工运行 mockit serve"。

**dsh 侧**:dsh 的 MCP 配置格式等其 MCP 支持定案后在 NUC10 另配,此处不给样例。

### CC Switch 模板警告(必读)

CC Switch 切换供应商时会**全量覆盖** `~/.claude/settings.json`——历史事故多起,直接写进去的 MCP 配置会被抹掉。mockit 的 MCP 配置二选一:

- 进 CC Switch 的模板,让它每次覆盖时带上;
- 或放项目级 `.mcp.json`(不受 CC Switch 影响,推荐)。

## 配置

优先级:默认值 < `~/.mockit/config.json` < 环境变量。配置文件所有键均可省略;示例:

```json
{
  "port": 8321,
  "addr": "0.0.0.0",
  "data_dir": "~/.mockit",
  "page_retention_days": 14,
  "decision_retention_days": 90,
  "external_url": ""
}
```

| 配置键(config.json) | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `port` | `MOCKIT_PORT` | `8321` | 监听端口;被占自动 +1,最多尝试 10 个 |
| `addr` | `MOCKIT_ADDR` | `0.0.0.0` | 监听地址 |
| `data_dir` | `MOCKIT_DATA_DIR` | `~/.mockit` | 数据目录;支持 `~` 展开,最终解析为绝对路径 |
| `page_retention_days` | `MOCKIT_PAGE_RETENTION_D` | `14` | 页面文件保留天数(锚点=提交时间) |
| `decision_retention_days` | `MOCKIT_DECISION_RETENTION_D` | `90` | 决策记录保留天数(锚点=审核完成时间) |
| `external_url` | (无环境变量) | 空 | **当前仅透传存储,无任何语义**,待票 09(F1)定案 |

## HTTP 面(手机侧)

| 路由 | 用途 |
|---|---|
| `GET /` | 提交列表页 |
| `GET /s/{id}` | 提交详情页(候选一览、裁决状态) |
| `GET /s/{id}/v{n}` | 候选全屏对比页,底部悬浮投票条 |
| `GET /raw/{id}/{n}/...` | 候选静态文件(严格防目录穿越) |
| `POST /api/review` | 提交裁决(approve / reject / choose+variant+comment) |
| `POST /api/pin` | 切换钉住 |
| `GET /ping` | 存活探测(返回版本) |

页面全部内嵌在二进制里(`go:embed`),无外部静态文件。

## 状态与保留期

- 状态机:`pending`(待审)→ `reviewed`(已审,**终态**);裁决后不可改,重复裁决明确报错(409)。
- 裁决:`approve` / `reject` / `choose`(choose 必须带候选序号);可附批注。
- 清理节律:serve **启动立即扫一遍**,之后每 24 小时一扫。
- 页面文件 14 天(锚点=提交时间):未审超期**整条删**(目录+记录);已审超期只删页面文件,记录留档至决策保留期满,页面显示"已清理"禁用态。
- 决策记录 90 天(锚点=审核完成时间):到期删记录。
- **钉住豁免一切清理**。

## 安全边界

- **无鉴权**:所有页面与 API 不做任何登录/令牌校验(唯一例外:`/shutdown` 需 lock 内 token)。安全边界就是网络——只在 NetBird 网内可达,**不要暴露公网**。
- **零推送**:服务器不会通知用户;"有新提交待拍板"的提醒由 agent 在对话里以纯文字给出。
- **服务器不做构建**:agent 负责把页面做成最终形态(单 HTML 或 zip)再提交;serve 只存、只展示。

## 暂未启用(如实说明)

以下两项的底层约束在评审中,本夜**未实现**,README 不承诺其行为:

- **URL 自动拼接**(票 09 / 约束 F1):`mockit_submit` 只返回"已提交 id=X(暂无链接)",任何工具输出都不含 URL;`external_url` 配置键仅透传存储,无语义。用户需手工打开 `http://<本机 NetBird IP>:<实际端口>/` 拍板。
- **MCP 自动拉起 serve**(票 08 / 约束 F2,启动互斥/进程拉起协议):`mockit mcp` **不会**自动拉起 serve——使用前先在终端手工运行 `mockit serve`;serve 不在时工具报"serve 未运行,请先手工运行 mockit serve"。

两票待评审约束解除后恢复,届时本文档同步更新。
