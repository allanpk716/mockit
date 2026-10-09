# Triage 标签（Triage Labels）

skills 用五个规范分诊角色说话。本文件把这些角色映射到本仓 issue 跟踪器实际使用的标签字符串。

| mattpocock/skills 里的角色 | 本仓跟踪器标签       | 含义                        |
| -------------------------- | -------------------- | --------------------------- |
| `needs-triage`             | `needs-triage`       | 维护者需要评估这条 issue    |
| `needs-info`               | `needs-info`         | 等报告者补充信息            |
| `ready-for-agent`          | `ready-for-agent`    | 规格完整，可交给 AFK agent  |
| `ready-for-human`          | `ready-for-human`    | 需要人来做                  |
| `wontfix`                  | `wontfix`            | 不做                        |

当 skill 提到某个角色（如「apply the AFK-ready triage label」）时，用本表对应的标签字符串。

两个**分类**角色同样按默认词用：`bug`（坏了）/ `enhancement`（新功能或改进）。每条分诊过的 issue 恰好带一个分类角色 + 一个状态角色。

本地 markdown 跟踪器下，这些角色写在票文件顶部的 `Status:` 行（约定见 `issue-tracker.md`）。想换叫法就改表的右列，改了记得和存量票保持一致。
