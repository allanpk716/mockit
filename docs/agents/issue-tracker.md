# Issue 跟踪器：GitHub

本仓的 issue 与规格放在 GitHub Issues（`allanpk716/mockit`）。所有操作用 `gh` CLI。

存量票（`.scratch/mockit-mvp/`、`.scratch/mockit-distribution/`）保留为历史记录，不迁移；新工作走 GitHub Issues。

## 约定

- **新建**：`gh issue create --title "..." --body "..."`，多行正文用 heredoc。
- **读取**：`gh issue view <number> --json number,title,body,labels,comments`。
- **列表**：`gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'`，按需加 `--label`、`--state`。
- **设为子 issue**：`gh issue create --parent <parent> ...`，或事后 `gh issue edit <parent> --add-sub-issue <child>`（`gh` 2.94+）。旧版 `gh`：`gh api --method POST repos/<owner>/<repo>/issues/<parent>/sub_issues -F sub_issue_id=<child-db-id>`（数据库 id，非 `#号`）。不用子 issue 时，在子票正文顶部写 `Part of #<parent>`。
- **评论**：`gh issue comment <number> --body "..."`
- **加/去标签**：`gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **关闭**：`gh issue close <number> --comment "..."`

在仓内运行时 `gh` 自动从 `git remote -v` 推断仓库。

## PR 作为请求入口

**PRs as a request surface: no.** _（若本仓把外部 PR 当作功能请求，改为 `yes`；`/triage` 读此标志。）_

## 当 skill 说「publish to the issue tracker」

新建一条 GitHub issue。

## 当 skill 说「fetch the relevant ticket」

按上面「读取」操作。

## Wayfinding 操作

供 `/wayfinder` 使用。**地图**是一条 issue，**子票**是其子 issue。

- **地图**：一条带 `wayfinder:map` 标签的 issue，正文含 Notes / Decisions-so-far / Fog。`gh issue create --label wayfinder:map`。
- **子票**：以 GitHub 子 issue 关联到地图（见上）。未启用子 issue 时，把子票加进地图正文任务列表，并在子票正文顶部写 `Part of #<map>`。标签 `wayfinder:<type>`（`research`/`prototype`/`grilling`/`task`）。认领后指派给执行的开发者。
- **阻塞**：用 GitHub 原生 issue dependencies：`gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`，`<blocker-db-id>` 是阻塞票的数字**数据库 id**（`gh api repos/<owner>/<repo>/issues/<n> --jq .id`）。不可用时退回子票正文顶部 `Blocked by: #<n>, #<n>`。所有阻塞票关闭即解除阻塞。
- **前沿查询**：列出地图的未关闭子票，剔除有未关闭阻塞或已有指派的，按地图顺序取第一条。
- **认领**：`gh issue edit <n> --add-assignee @me`，会话的第一个写操作。
- **完成**：`gh issue comment <n> --body "<答案>"` → `gh issue close <n>` → 在地图的 Decisions-so-far 追加上下文指针（gist + 链接）。
