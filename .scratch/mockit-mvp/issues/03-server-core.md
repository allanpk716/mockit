# 票 03 · HTTP 服务核心(serve)

## What to build

`internal/server` 的 `Serve(cfg)` 完整实现:

- 端口绑定循环:配置端口起,被占 +1,最多试 10 个,全占报错退出;绑定成功后 `lifecycle.WriteLock`(port=实际端口、pid、version=main.Version 常量"0.1.0"、随机 token、started_at、base_host 留空——基址语义属票 09,本票不做任何 URL 拼接)。
- 路由(Go 1.22+ ServeMux pattern):
  - `GET /` 列表页;`GET /s/{id}` 详情页;`GET /s/{id}/v{n}` 候选全屏壳页(本票先给占位 HTML,票 04 替换)
  - `GET /raw/{id}/{n}/{path...}` 候选静态文件(严格防目录穿越,仅限该候选目录内)
  - `POST /api/submissions` 提交:JSON `{title, note?, variants:[{label, kind:html|zip, content_b64}]}`;html 落 `<data>/{id}/v{n}/index.html`;zip≤20MB、必须含根 `index.html`、解包拒绝绝对路径与 `..` 条目;候选数 1~6;id=6 位 base36(服务端生成)
  - `GET /api/submissions?status=&limit=` 列表 JSON;`GET /api/submissions/{id}` 详情 JSON(含 variants 与裁决;files_deleted 时候选带 cleaned 标记)
  - `POST /api/review` `{id, decision:approve|reject|choose, variant?, comment?}`(复用 store 规则)
  - `POST /api/pin` `{id, pinned}`
  - `GET /ping` → `{version}`;`POST /shutdown`(Header 带 lock token,对才停)
- 页面/静态经 `//go:embed web/*` 内嵌(占位页);优雅退出(信号)。
- 日志写 `<data>/logs/serve.log`。

刻意不做:URL 拼接与基址(票 09)、进程拉起/互斥(票 08)。

## 验收标准

- [ ] html/zip 提交全链:落盘+detail 可读+raw 可取
- [ ] zip 缺根 index.html 拒;含 `../` 条目拒;>20MB 拒;候选 0/7 拒
- [ ] review:approve/reject/choose 各一路+choose 坏 seq 拒+重复裁决拒
- [ ] pin 切换生效;raw `../` 穿越拒
- [ ] ping 返回版本;shutdown token 对错两路
- [ ] 端口漂移:占住首选端口后启动,实际监听 +1 且 lock.port 为实际端口
- [ ] `go test ./internal/server/` 全过;`go build ./...` 绿

## Blocked by

票 01、票 02

## 涉及路径

- internal/server/(目录全部,含 web/ 占位与测试)

## 副作用声明

- 独占验证命令:`go test ./internal/server/`
- 测试会临时监听 127.0.0.1 高位随机端口(端口漂移用例需真绑定),不联网

## decision_refs

D4(内容形态)、D10(裁决规则)、D11(页面形态壳)、D12(无鉴权)、F6(详情 JSON 不含 URL)、D8(端口漂移与 lock 写入)

## review_blocks

无
