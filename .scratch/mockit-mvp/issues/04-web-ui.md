# 票 04 · 前端三页(手机优先)

## What to build

替换 `internal/server/web/` 为最终页面(原生 HTML/CSS/JS,无外部 CDN,手机优先 viewport):

- `list.html`:拉 `/api/submissions`;待审组在前(卡片:标题/候选数/时间/pin 按钮),已审组折叠(标题/裁决徽标/时间);点卡片进 `/s/{id}`;空态文案。
- `detail.html`:从路径解析 id;标题/说明/状态徽标;已审显示裁决(approve=通过/reject=打回/choose=选中"候选 N·标签")+批注;候选入口卡(标签+进入按钮→`/s/{id}/v{n}`;候选 cleaned 时按钮禁用显"页面已清理");pin 开关(POST /api/pin 后刷新)。
- `variant.html`:路径解析 id/n;全屏 iframe 指 `/raw/{id}/{n}/index.html`;底部悬浮条(fixed bottom):候选 pill 1..N(链接 `/s/{id}/v{k}`,当前高亮)、批注输入框、按钮组——多候选:「选它」(choose+variant=n)+「打回」;单候选:「通过」+「打回」;提交 POST /api/review 后跳 `/s/{id}`;提交失败(如已审)就地显示错误。
- 样式:暗色或浅色自选,保证手机可读;悬浮条不遮挡时给 iframe 底部留白。

## 验收标准

- [ ] 三页 GET 200 且含上述骨架元素(自动化:起临时服务断言 HTML 关键标记与 JS 引用)
- [ ] variant 页对 `/api/submissions/{id}` 的取数与按钮分支(单/多候选)逻辑正确(自动化:断言 JS 内分支标记或用最小 DOM 断言;不要求浏览器级测试)
- [ ] 无任何外网资源引用(grep 无 http://cdn 等)
- [ ] `go build ./...` 绿(embed 生效)
- [ ] 手机真实观感(悬浮条/切换/投票动线)列入晨报"需你测试评估"

## Blocked by

票 03

## 涉及路径

- internal/server/web/(仅页面文件,不改 .go)

## 副作用声明

- 验证命令:`go test ./internal/server/ -run TestWeb`(新增用例)

## decision_refs

D11(全屏+底部悬浮条,不用 iframe 排对比)

## review_blocks

无
