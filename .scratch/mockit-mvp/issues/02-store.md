# 票 02 · SQLite 存储与数据模型

## What to build

`internal/store` 完整实现(modernc.org/sqlite 已 vendor,离线可用):

- `Open(dataDir)`:`<data>/mockit.db`,建表幂等迁移。
- 表:`submissions(id TEXT PK, title, note, status TEXT[pending|reviewed], decision TEXT[approve|reject|choose,NULL], chosen_variant INTEGER, comment TEXT, created_at INT, reviewed_at INT, pinned INT DEFAULT 0, files_deleted INT DEFAULT 0)`;`variants(id INTEGER PK AUTOINC, submission_id TEXT, seq INT, label TEXT, kind TEXT[html|zip], entry TEXT, FOREIGN KEY submission_id)`。
- 方法:`CreateSubmission(id,title,note)`(id 由调用方生成,6 位 base36)、`AddVariant(subID,seq,label,kind)`、`Get(id)`(含 variants)、`List(status?,limit)`(待审在前、已审在后,组内新在前)、`SaveReview(id,decision,chosen,comment)`(仅 pending 可裁;decision=choose 时 chosen 必须是存在候选的 seq;写 reviewed_at;已审再裁返回明确错误)、`SetPinned(id,bool)`、`DueFileCleanup(now,pageDays)`(返回该清文件/整条删的提交:未钉住的待审且 created_at 超期→整条删标记;未钉住的已审且 created_at 超期→仅文件)、`MarkFilesDeleted(id)`、`DueRecordCleanup(now,decisionDays)`(已审且 reviewed_at 超期)、`DeleteRecord(id)`。
- 语义铁律:已审为终态、无覆盖更新;决策记录锚点=审核完成时间,页面文件锚点=提交时间(F3 定案)。

## 验收标准

- [ ] 状态机:pending→reviewed;重复裁决报错;choose 带不存在 seq 报错
- [ ] 清理查询:未审超期=整条;已审超期=仅文件;记录 90 天(按 reviewed_at);钉住全部豁免
- [ ] List 排序与 status 过滤;pin 切换
- [ ] `go test ./internal/store/` 全过;`go build ./...` 绿

## Blocked by

无,可立即开始

## 涉及路径

- internal/store/(目录,含测试)

## 副作用声明

- 独占验证命令:`go test ./internal/store/`
- 测试用临时目录数据库,不联网

## decision_refs

D10(数据模型/终态/改了重交)、D5(清理参数)、F3 定案语义

## review_blocks

无
