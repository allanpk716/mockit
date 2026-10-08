// Package store 提供 mockit 的 SQLite 存储(提交/候选/审核)。
//
// 单写者:连接池上限 1,所有读写经同一连接串行执行。
// 时间戳一律 Unix 秒。语义铁律:已审为终态、无覆盖更新;
// 决策记录清理锚点=审核完成时间,页面文件清理锚点=提交时间(F3 定案)。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动,注册名为 sqlite
)

// 状态与枚举常量(供调用方与测试使用,避免散落字符串字面量)。
const (
	StatusPending  = "pending"
	StatusReviewed = "reviewed"

	DecisionApprove = "approve"
	DecisionReject  = "reject"
	DecisionChoose  = "choose"

	KindHTML = "html"
	KindZip  = "zip"

	entryFile = "index.html" // 单 HTML 落盘名与 zip 根入口,规格定死
)

// 哨兵错误:调用方用 errors.Is 判别。
var (
	ErrNotFound        = errors.New("store: 提交不存在")
	ErrAlreadyReviewed = errors.New("store: 已审为终态,不可重复裁决")
	ErrInvalidVariant  = errors.New("store: choose 的候选序号不存在")
	ErrInvalidDecision = errors.New("store: 无效裁决类型(应为 approve|reject|choose)")
	ErrInvalidKind     = errors.New("store: 无效候选形态(应为 html|zip)")
)

// Store 是存储句柄。并发安全(database/sql 连接池,上限 1)。
type Store struct {
	db *sql.DB
}

// schema 建表迁移,全部 IF NOT EXISTS 保证幂等。
const schema = `
CREATE TABLE IF NOT EXISTS submissions (
	id             TEXT PRIMARY KEY,
	title          TEXT NOT NULL DEFAULT '',
	note           TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','reviewed')),
	decision       TEXT CHECK (decision IS NULL OR decision IN ('approve','reject','choose')),
	chosen_variant INTEGER,
	comment        TEXT NOT NULL DEFAULT '',
	created_at     INTEGER NOT NULL,
	reviewed_at    INTEGER,
	pinned         INTEGER NOT NULL DEFAULT 0,
	files_deleted  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_submissions_status_created ON submissions(status, created_at);
CREATE TABLE IF NOT EXISTS variants (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	submission_id TEXT NOT NULL REFERENCES submissions(id),
	seq           INTEGER NOT NULL,
	label         TEXT NOT NULL DEFAULT '',
	kind          TEXT NOT NULL CHECK (kind IN ('html','zip')),
	entry         TEXT NOT NULL DEFAULT 'index.html'
);
CREATE INDEX IF NOT EXISTS idx_variants_submission ON variants(submission_id, seq);
`

// Open 打开(必要时创建)数据目录下的 SQLite 库 <dataDir>/mockit.db,
// 并执行幂等建表迁移。文件不存在时自动创建,目录不存在时自动建立。
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: 创建数据目录: %w", err)
	}
	// foreign_keys(1):启用外键约束(modernc 驱动经 DSN 参数对每条连接生效)
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "mockit.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("store: 打开数据库: %w", err)
	}
	db.SetMaxOpenConns(1) // 单写者,防多连接写冲突(SQLITE_BUSY)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: 建表迁移: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: 数据库连接失败: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// CreateSubmission 新建一条待审提交,id 由调用方生成(6 位 base36),created_at=当前时间。
func (s *Store) CreateSubmission(id, title string) error {
	_, err := s.db.Exec(
		`INSERT INTO submissions (id, title, note, status, created_at) VALUES (?, ?, '', ?, ?)`,
		id, title, StatusPending, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: 创建提交 %s: %w", id, err)
	}
	return nil
}

// AddVariant 为提交追加一个候选(seq 从 1 起)。入口文件固定 index.html。
func (s *Store) AddVariant(subID string, seq int, label, kind string) error {
	if kind != KindHTML && kind != KindZip {
		return ErrInvalidKind
	}
	_, err := s.db.Exec(
		`INSERT INTO variants (submission_id, seq, label, kind, entry) VALUES (?, ?, ?, ?, ?)`,
		subID, seq, label, kind, entryFile,
	)
	if err != nil {
		return fmt.Errorf("store: 追加候选 %s/%d: %w", subID, seq, err)
	}
	return nil
}

// Get 按 id 取单条提交(含候选,按 seq 升序);不存在返回 ErrNotFound。
func (s *Store) Get(id string) (*Submission, error) {
	rows, err := s.db.Query(submissionJoinSQL+` WHERE s.id = ? ORDER BY v.seq ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("store: 查询提交 %s: %w", id, err)
	}
	subs, err := collectSubmissions(rows)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, ErrNotFound
	}
	return subs[0], nil
}

// List 按状态列提交:待审在前、已审在后,组内新在前;status 为空串则不过滤;
// limit>0 时限制返回最近 limit 条提交(候选数不受影响);limit<=0 不限。
func (s *Store) List(status string, limit int) ([]*Submission, error) {
	const listOrder = ` ORDER BY (s.status = 'reviewed') ASC, s.created_at DESC, s.id DESC, v.seq ASC`
	query := submissionJoinSQL
	var args []any
	if limit > 0 {
		// 先挑出目标提交(id 集),再 JOIN 取候选,保证 limit 按提交数截断
		query += ` WHERE s.id IN (
			SELECT id FROM submissions`
		if status != "" {
			query += ` WHERE status = ?`
			args = append(args, status)
		}
		query += ` ORDER BY (status = 'reviewed') ASC, created_at DESC, id DESC LIMIT ?`
		args = append(args, limit)
		query += `)`
	} else if status != "" {
		query += ` WHERE s.status = ?`
		args = append(args, status)
	}
	query += listOrder

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: 列提交: %w", err)
	}
	return collectSubmissions(rows)
}

// SaveReview 写裁决。仅 pending 可裁;decision=choose 时 chosen 必须是已存在候选的 seq;
// 成功则转 reviewed 并写 reviewed_at;已审再裁返回 ErrAlreadyReviewed(终态,不落任何痕迹)。
func (s *Store) SaveReview(id, decision string, chosen int, comment string) error {
	switch decision {
	case DecisionApprove, DecisionReject, DecisionChoose:
	default:
		return ErrInvalidDecision
	}
	var chosenVal any // approve/reject 时为 nil → 落 NULL
	if decision == DecisionChoose {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM variants WHERE submission_id = ? AND seq = ?`, id, chosen,
		).Scan(&n); err != nil {
			return fmt.Errorf("store: 校验候选 %s/%d: %w", id, chosen, err)
		}
		if n == 0 {
			return ErrInvalidVariant
		}
		chosenVal = chosen
	}
	// WHERE status='pending' 保证终态原子性:并发/重复裁决都不会覆盖
	res, err := s.db.Exec(
		`UPDATE submissions
		 SET status = ?, decision = ?, chosen_variant = ?, comment = ?, reviewed_at = ?
		 WHERE id = ? AND status = ?`,
		StatusReviewed, decision, chosenVal, comment, time.Now().Unix(), id, StatusPending,
	)
	if err != nil {
		return fmt.Errorf("store: 写裁决 %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: 写裁决 %s: %w", id, err)
	}
	if n == 0 {
		// 区分两种失败:提交不存在 vs 已审
		var one int
		err := s.db.QueryRow(`SELECT 1 FROM submissions WHERE id = ?`, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("store: 查提交 %s: %w", id, err)
		}
		return ErrAlreadyReviewed
	}
	return nil
}

// SetPinned 切换钉住状态(钉住豁免一切清理)。
func (s *Store) SetPinned(id string, pinned bool) error {
	return s.execByID(`UPDATE submissions SET pinned = ? WHERE id = ?`, id, boolToInt(pinned))
}

// MarkFilesDeleted 标记提交的页面文件已清(files_deleted=1),记录继续留档。
func (s *Store) MarkFilesDeleted(id string) error {
	return s.execByID(`UPDATE submissions SET files_deleted = 1 WHERE id = ?`, id, nil)
}

// DueFileCleanup 返回到期应清页面文件的提交(F3:锚点=提交时间):
//   - 未钉住的待审且 created_at 超期 → Whole=true(整条删:目录+记录)
//   - 未钉住的已审且 created_at 超期且文件未清过 → Whole=false(仅文件,记录留档至决策保留期满)
//
// 钉住的提交一律豁免。
func (s *Store) DueFileCleanup(now int64, pageDays int) ([]DueCleanup, error) {
	cutoff := now - int64(pageDays)*86400
	rows, err := s.db.Query(`
		SELECT id, 1 FROM submissions
		 WHERE pinned = 0 AND status = 'pending' AND created_at < ?
		UNION ALL
		SELECT id, 0 FROM submissions
		 WHERE pinned = 0 AND status = 'reviewed' AND files_deleted = 0 AND created_at < ?`,
		cutoff, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: 查文件清理: %w", err)
	}
	defer rows.Close()
	var due []DueCleanup
	for rows.Next() {
		var id string
		var whole int
		if err := rows.Scan(&id, &whole); err != nil {
			return nil, fmt.Errorf("store: 扫文件清理: %w", err)
		}
		due = append(due, DueCleanup{ID: id, Whole: whole == 1})
	}
	return due, rows.Err()
}

// DueRecordCleanup 返回到期应删决策记录的已审提交 id(F3:锚点=审核完成时间)。
// 未钉住、status=reviewed 且 reviewed_at 超期才算到期;钉住豁免。
func (s *Store) DueRecordCleanup(now int64, decisionDays int) ([]string, error) {
	cutoff := now - int64(decisionDays)*86400
	rows, err := s.db.Query(`
		SELECT id FROM submissions
		 WHERE pinned = 0 AND status = 'reviewed' AND reviewed_at IS NOT NULL AND reviewed_at < ?`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: 查记录清理: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: 扫记录清理: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteRecord 删除提交及其全部候选行(整条删的收尾动作);不存在返回 ErrNotFound。
func (s *Store) DeleteRecord(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: 开事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // Commit 后 Rollback 是无害的 ErrTxDone
	if _, err := tx.Exec(`DELETE FROM variants WHERE submission_id = ?`, id); err != nil {
		return fmt.Errorf("store: 删候选 %s: %w", id, err)
	}
	res, err := tx.Exec(`DELETE FROM submissions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: 删提交 %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: 删提交 %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// submissionJoinSQL 提交左联候选的公共查询体(v.* 为 NULL 表示无候选)。
const submissionJoinSQL = `
SELECT s.id, s.title, s.note, s.status, s.decision, s.chosen_variant, s.comment,
       s.created_at, s.reviewed_at, s.pinned, s.files_deleted,
       v.id, v.submission_id, v.seq, v.label, v.kind, v.entry
FROM submissions s
LEFT JOIN variants v ON v.submission_id = s.id`

// collectSubmissions 把 JOIN 行按提交分组组装(NULL 候选行跳过)。
func collectSubmissions(rows *sql.Rows) ([]*Submission, error) {
	defer rows.Close()
	var (
		subs []*Submission
		byID = map[string]*Submission{}
	)
	for rows.Next() {
		var (
			sub        Submission
			decision   sql.NullString
			chosen     sql.NullInt64
			reviewedAt sql.NullInt64
			pinned     int
			filesDel   int
			vID        sql.NullInt64
			vSubID     sql.NullString
			vSeq       sql.NullInt64
			vLabel     sql.NullString
			vKind      sql.NullString
			vEntry     sql.NullString
		)
		if err := rows.Scan(
			&sub.ID, &sub.Title, &sub.Note, &sub.Status, &decision, &chosen, &sub.Comment,
			&sub.CreatedAt, &reviewedAt, &pinned, &filesDel,
			&vID, &vSubID, &vSeq, &vLabel, &vKind, &vEntry,
		); err != nil {
			return nil, fmt.Errorf("store: 扫提交行: %w", err)
		}
		sub.Decision = decision.String
		sub.ChosenVariant = int(chosen.Int64)
		sub.ReviewedAt = reviewedAt.Int64
		sub.Pinned = pinned == 1
		sub.FilesDeleted = filesDel == 1

		existing, ok := byID[sub.ID]
		if !ok {
			sub.Variants = make([]Variant, 0, 2)
			subs = append(subs, &sub)
			byID[sub.ID] = &sub
			existing = &sub
		}
		if vID.Valid {
			existing.Variants = append(existing.Variants, Variant{
				ID:           vID.Int64,
				SubmissionID: vSubID.String,
				Seq:          int(vSeq.Int64),
				Label:        vLabel.String,
				Kind:         vKind.String,
				Entry:        vEntry.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: 迭代提交行: %w", err)
	}
	return subs, nil
}

// execByID 执行带 id 条件的更新,0 行受影响视为 ErrNotFound。
func (s *Store) execByID(query, id string, arg any) error {
	args := []any{id}
	if arg != nil {
		args = []any{arg, id}
	}
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("store: 更新提交 %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: 更新提交 %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
