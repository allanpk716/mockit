# get_review 待审时附审核链接(修订 F6)

MVP 定案 F6 规定 URL 只出现在 `mockit_submit` 结果里,`mockit_get_review` / `mockit_list` 一律不含。实际使用中出现过 agent 请用户去审核却没给地址的情况;而一旦 submit 结果被上下文压缩掉,或用户隔几轮才问"在哪审",agent 手里已经没有审核链接可贴。故修订 F6:**get_review 在待审时附审核链接**,让接入方凭提交 id 随时能重新拿到它。

## Considered Options

- 只在技能里强化"必须贴链接":解决不了 agent 已丢失链接的情形。
- get_review 无论状态都附链接:已审提交无需再审,且页面可能已清理,附链接只会误导。
- list 也附链接:list 用于翻旧账,不是请用户审核的入口,不加。

## Consequences

- 链接由 MCP 层按 submit 同一拼接规则(实例锁 base_host + 实际端口)自行生成;serve 的详情/列表 JSON 仍不含 URL。
- 对外基址确定不了(未配 external_url 且 NetBird 探测歧义/零命中)时,get_review 照常返回、不附链接、不报错——查询裁决不能因地址问题失败。
- 端口漂移后 get_review 给出的是按当前端口拼的新链接,比 submit 时的旧链接更可靠。
