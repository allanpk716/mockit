# 票 01 · 技能文件:触发式接入技能(自包含一页纸)

## What to build

新增 `skills/mockit/SKILL.md`:给接入方(Claude Code 类)agent 的触发式技能。用户提出要做 mock 页面/多方案对比时,技能把压缩工作流与错误应对教给 agent。自包含——接入机器只有二进制和本技能文件,没有本仓,不得引用任何仓内路径。

## 验收标准

- [ ] YAML frontmatter:`name: mockit`、`description:` 一行(含触发词:mocik/mock 页面/方案对比/页面拍板/手机看效果等)
- [ ] 触发时机段:何时用 mockit(产出 1~6 个完整可看页面候选交用户拍板)
- [ ] 压缩工作流段:`mockit_submit` 提交 → **立刻**把返回的审核页 URL 贴给用户并提醒手机打开(NetBird 网络)→ 用户回话后 `mockit_get_review` 取裁决(approve/choose → 按选中候选继续;reject → 按批注改后重新提交,无覆盖更新)→ 需要翻旧账用 `mockit_list`
- [ ] 错误应对段(与 AGENT-GUIDE.md 口径一致):"无法确定手机可达地址→配 external_url(裸主机名)"、"疑似卡死→请用户手工处置"、"启动中退出→手工跑 mockit serve 看报错"、"超时→稍等重试"
- [ ] 约束提醒:候选 1~6、单文件 ≤20MB、zip 必含根 index.html、服务器不做构建、已审终态、页面 14 天清理(别承诺链接永久)、提醒用户去看是 agent 的职责(零推送)
- [ ] 全文自包含,不出现本仓路径/仓内文件引用;一页纸(≤120 行)
- [ ] 不涉及 MCP 配置方法(那是 INSTALL 的职责,一句话指回 release 附带的 INSTALL.md 即可,但不依赖它存在)

## Blocked by

无,可立即开始。

## 涉及路径

- skills/mockit/SKILL.md(新增)

## 副作用声明

无(纯文档;不跑构建/测试)。

decision_refs: D3, D5, D7
review_blocks: 无
