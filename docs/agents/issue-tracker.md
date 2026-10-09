# Issue 跟踪器：本地 markdown

本仓的 issue 与规格以 markdown 文件形式放在 `.scratch/` 下。

## 约定

- 一个 feature 一个目录：`.scratch/<feature-slug>/`（如 `.scratch/mockit-mvp/`）
- 规格文件：`.scratch/<feature-slug>/spec.md`
- 实施票每票一个文件：`.scratch/<feature-slug>/issues/<NN>-<slug>.md`，从 `01` 起编号，不写合并的大票文件
- 状态记录在票文件顶部：新票用 `Status:` 行（分诊角色词表见 `triage-labels.md`）；存量票用 `> 状态:` 行（如 `> 状态:ready(...)`），两种都认
- 评论与过程记录追加到票文件底部 `## Comments` 标题下（存量票的「验收标准 / Blocked by / 涉及路径」等小节保持原样）

## 当 skill 说「publish to the issue tracker」

在 `.scratch/<feature-slug>/` 下新建文件（目录不存在就创建）。

## 当 skill 说「fetch the relevant ticket」

读对应路径的文件。用户通常直接给路径或票号。

## Wayfinding 操作（/wayfinder 用）

map 是一个文件，每个 ticket 一个子文件。

- **Map**：`.scratch/<effort>/map.md`（Notes / Decisions-so-far / Fog 正文）
- **子票**：`.scratch/<effort>/issues/NN-<slug>.md`，从 `01` 起编号，正文写问题。`Type:` 行记录票类型（`research`/`prototype`/`grilling`/`task`）；`Status:` 行记录 `claimed`/`resolved`
- **Blocking**：顶部 `Blocked by: NN, NN` 行。所列票全部 `resolved` 才算解锁
- **Frontier**：扫 `.scratch/<effort>/issues/`，取开着、无阻塞、无人认领的票，编号小者优先
- **Claim**：动手前先置 `Status: claimed` 并保存
- **Resolve**：在 `## Answer` 标题下追加答案，置 `Status: resolved`，再把上下文指针（要点 + 链接）追加进 `map.md` 的 Decisions-so-far
