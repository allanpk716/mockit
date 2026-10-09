# 域文档（Domain Docs）

工程类 skills 探索本仓代码时，如何消费域文档。

## 探索前先读

- 仓根的 `CONTEXT.md`（若存在）
- 仓根的 `docs/adr/`：读与本次工作区域相关的 ADR

这些文件还不存在时**静默继续**：不提示缺失、不建议先建。`/domain-modeling` skill（经 `/grill-with-docs`、`/improve-codebase-architecture` 触达）会在术语或决策真正落定时惰性创建它们。

## 文件布局

本仓为单上下文（single-context）布局：

```
/                     ← 仓根
├── CONTEXT.md        ← 域词汇表（glossary，待建）
├── docs/adr/         ← 架构决策记录（待建）
├── internal/
├── web/
└── main.go
```

## 用词汇表的话说

输出（票标题、重构提案、假设、测试名）命名域概念时，用 `CONTEXT.md` 定义的术语，不用词汇表明确回避的同义词。仓内 CONTEXT.md 建立之前，以 spec 引用的既有词汇为准：提交/候选/审核/批注/待审/已审/钉住/清理（`.scratch/mockit-mvp/spec.md` 开头）。

需要的概念不在词汇表里：要么是在发明项目不用的语言（重新考虑），要么是真实缺口（记给 /domain-modeling）。

## ADR 冲突要显式标注

输出与既有 ADR 矛盾时，明说而不是悄悄覆盖：
「与 ADR-000N（……）矛盾，但值得重开，因为……」
