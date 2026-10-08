package store

import (
	"errors"
	"testing"
	"time"
)

const day = 86400 // 一天的秒数

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCreate(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateSubmission(id, "标题 "+id); err != nil {
		t.Fatalf("CreateSubmission(%s): %v", id, err)
	}
}

func mustAddVariant(t *testing.T, s *Store, subID string, seq int, label, kind string) {
	t.Helper()
	if err := s.AddVariant(subID, seq, label, kind); err != nil {
		t.Fatalf("AddVariant(%s,%d): %v", subID, seq, err)
	}
}

// 白盒辅助:直改 created_at,模拟老数据(CreateSubmission 固定写当前时间,无注入口)。
func setCreatedAt(t *testing.T, s *Store, id string, ts int64) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE submissions SET created_at=? WHERE id=?`, ts, id); err != nil {
		t.Fatalf("setCreatedAt(%s): %v", id, err)
	}
}

// 白盒辅助:直改 reviewed_at,模拟老的审核完成时间。
func setReviewedAt(t *testing.T, s *Store, id string, ts int64) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE submissions SET reviewed_at=? WHERE id=?`, ts, id); err != nil {
		t.Fatalf("setReviewedAt(%s): %v", id, err)
	}
}

func TestOpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("第一次 Open: %v", err)
	}
	_ = s1.Close()
	s2, err := Open(dir) // 二次打开同一库,建表应幂等
	if err != nil {
		t.Fatalf("二次 Open(建表应幂等): %v", err)
	}
	_ = s2.Close()
}

func TestCreateAndGet(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	mustAddVariant(t, s, "abc123", 1, "方案A", "html")
	mustAddVariant(t, s, "abc123", 2, "方案B", "zip")

	got, err := s.Get("abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "abc123" || got.Title != "标题 abc123" {
		t.Errorf("ID/标题不符: %+v", got)
	}
	if got.Status != StatusPending {
		t.Errorf("新建应为 pending,得到 %q", got.Status)
	}
	if got.Decision != "" || got.ChosenVariant != 0 || got.ReviewedAt != 0 {
		t.Errorf("新建不应有裁决: decision=%q chosen=%d reviewed_at=%d", got.Decision, got.ChosenVariant, got.ReviewedAt)
	}
	if got.CreatedAt <= 0 {
		t.Errorf("created_at 应为正的 Unix 秒,得到 %d", got.CreatedAt)
	}
	if got.Pinned || got.FilesDeleted {
		t.Errorf("新建不应钉住/已清文件: pinned=%v files_deleted=%v", got.Pinned, got.FilesDeleted)
	}
	if len(got.Variants) != 2 {
		t.Fatalf("候选数应为 2,得到 %d", len(got.Variants))
	}
	v1, v2 := got.Variants[0], got.Variants[1]
	if v1.Seq != 1 || v1.Label != "方案A" || v1.Kind != "html" {
		t.Errorf("候选1不符: %+v", v1)
	}
	if v2.Seq != 2 || v2.Label != "方案B" || v2.Kind != "zip" {
		t.Errorf("候选2不符: %+v", v2)
	}
}

func TestAddVariantInvalidKind(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	if err := s.AddVariant("abc123", 1, "x", "pdf"); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("kind=pdf 应报 ErrInvalidKind,得到 %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	s := testStore(t)
	if _, err := s.Get("zzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 id 应报 ErrNotFound,得到 %v", err)
	}
}

func TestSaveReviewApprove(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	mustAddVariant(t, s, "abc123", 1, "A", "html")

	if err := s.SaveReview("abc123", "approve", 0, "可以"); err != nil {
		t.Fatalf("SaveReview(approve): %v", err)
	}
	got, err := s.Get("abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusReviewed {
		t.Errorf("裁决后应为 reviewed,得到 %q", got.Status)
	}
	if got.Decision != "approve" || got.Comment != "可以" {
		t.Errorf("裁决/批注不符: %+v", got)
	}
	if got.ChosenVariant != 0 {
		t.Errorf("approve 不应带选中候选,得到 %d", got.ChosenVariant)
	}
	if got.ReviewedAt <= 0 {
		t.Errorf("裁决后应写 reviewed_at,得到 %d", got.ReviewedAt)
	}
}

func TestSaveReviewChoose(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	for i := 1; i <= 3; i++ {
		mustAddVariant(t, s, "abc123", i, "方案", "html")
	}
	if err := s.SaveReview("abc123", "choose", 2, "B 更好"); err != nil {
		t.Fatalf("SaveReview(choose,2): %v", err)
	}
	got, err := s.Get("abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Decision != "choose" || got.ChosenVariant != 2 {
		t.Errorf("choose 裁决不符: decision=%q chosen=%d", got.Decision, got.ChosenVariant)
	}
	if got.Status != StatusReviewed || got.ReviewedAt <= 0 {
		t.Errorf("choose 后应转已审并写时间: status=%q reviewed_at=%d", got.Status, got.ReviewedAt)
	}
}

func TestSaveReviewChooseInvalidSeq(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	mustAddVariant(t, s, "abc123", 1, "A", "html")
	if err := s.SaveReview("abc123", "choose", 99, ""); !errors.Is(err, ErrInvalidVariant) {
		t.Errorf("choose 不存在的 seq 应报 ErrInvalidVariant,得到 %v", err)
	}
	got, err := s.Get("abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("裁决失败后应保持 pending,得到 %q", got.Status)
	}
}

func TestSaveReviewDuplicateRejected(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	mustAddVariant(t, s, "abc123", 1, "A", "html")
	if err := s.SaveReview("abc123", "approve", 0, "第一刀"); err != nil {
		t.Fatalf("第一次裁决: %v", err)
	}
	if err := s.SaveReview("abc123", "choose", 1, "改判"); !errors.Is(err, ErrAlreadyReviewed) {
		t.Errorf("已审再裁应报 ErrAlreadyReviewed,得到 %v", err)
	}
	got, err := s.Get("abc123") // 终态:第二次裁决不得留下痕迹
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Decision != "approve" || got.Comment != "第一刀" || got.ChosenVariant != 0 {
		t.Errorf("终态被覆盖: %+v", got)
	}
}

func TestSaveReviewInvalidDecision(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	if err := s.SaveReview("abc123", "maybe", 0, ""); !errors.Is(err, ErrInvalidDecision) {
		t.Errorf("无效 decision 应报 ErrInvalidDecision,得到 %v", err)
	}
}

func TestSaveReviewNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.SaveReview("zzzzzz", "approve", 0, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("对不存在提交裁决应报 ErrNotFound,得到 %v", err)
	}
}

func TestListOrderAndFilter(t *testing.T) {
	s := testStore(t)
	now := time.Now().Unix()
	// p1/p2 待审(p2 新),r1/r2 已审(r1 新);期望全量顺序 p2,p1,r1,r2
	for _, id := range []string{"p1", "p2", "r1", "r2"} {
		mustCreate(t, s, id)
		mustAddVariant(t, s, id, 1, "A", "html")
	}
	setCreatedAt(t, s, "p1", now-200)
	setCreatedAt(t, s, "p2", now-100)
	setCreatedAt(t, s, "r1", now-300)
	setCreatedAt(t, s, "r2", now-400)
	if err := s.SaveReview("r1", "approve", 0, ""); err != nil {
		t.Fatalf("SaveReview(r1): %v", err)
	}
	if err := s.SaveReview("r2", "reject", 0, ""); err != nil {
		t.Fatalf("SaveReview(r2): %v", err)
	}

	wantIDs := func(got []*Submission) []string {
		t.Helper()
		ids := make([]string, 0, len(got))
		for _, sub := range got {
			ids = append(ids, sub.ID)
		}
		return ids
	}
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	all, err := s.List("", 0)
	if err != nil {
		t.Fatalf("List 全量: %v", err)
	}
	if got := wantIDs(all); !eq(got, []string{"p2", "p1", "r1", "r2"}) {
		t.Errorf("全量顺序应为 待审在前新在前、已审在后新在前,得到 %v", got)
	}
	pending, err := s.List("pending", 0)
	if err != nil {
		t.Fatalf("List(pending): %v", err)
	}
	if got := wantIDs(pending); !eq(got, []string{"p2", "p1"}) {
		t.Errorf("pending 过滤应得 [p2 p1],得到 %v", got)
	}
	reviewed, err := s.List("reviewed", 0)
	if err != nil {
		t.Fatalf("List(reviewed): %v", err)
	}
	if got := wantIDs(reviewed); !eq(got, []string{"r1", "r2"}) {
		t.Errorf("reviewed 过滤应得 [r1 r2],得到 %v", got)
	}
}

func TestListLimitCountsSubmissions(t *testing.T) {
	s := testStore(t)
	now := time.Now().Unix()
	// p2 每条 2 个候选:limit=1 应返回 1 条提交且候选齐全(limit 按提交数而非 JOIN 行数)
	for _, id := range []string{"p1", "p2"} {
		mustCreate(t, s, id)
		mustAddVariant(t, s, id, 1, "A", "html")
		mustAddVariant(t, s, id, 2, "B", "html")
	}
	setCreatedAt(t, s, "p1", now-200)
	setCreatedAt(t, s, "p2", now-100)

	got, err := s.List("", 1)
	if err != nil {
		t.Fatalf("List limit=1: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("limit=1 应返回 1 条提交,得到 %d 条", len(got))
	}
	if got[0].ID != "p2" {
		t.Errorf("limit 应取最新提交 p2,得到 %s", got[0].ID)
	}
	if len(got[0].Variants) != 2 {
		t.Errorf("候选不应被 limit 挤掉,得到 %d 个", len(got[0].Variants))
	}
}

func TestSetPinned(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	if err := s.SetPinned("abc123", true); err != nil {
		t.Fatalf("SetPinned(true): %v", err)
	}
	got, _ := s.Get("abc123")
	if !got.Pinned {
		t.Error("SetPinned(true) 后 Pinned 应为 true")
	}
	if err := s.SetPinned("abc123", false); err != nil {
		t.Fatalf("SetPinned(false): %v", err)
	}
	got, _ = s.Get("abc123")
	if got.Pinned {
		t.Error("SetPinned(false) 后 Pinned 应为 false")
	}
	if err := s.SetPinned("zzzzzz", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("对不存在提交钉住应报 ErrNotFound,得到 %v", err)
	}
}

func TestDueFileCleanup(t *testing.T) {
	s := testStore(t)
	now := time.Now().Unix()
	pageDays := 14
	old := now - int64(pageDays)*day - 100 // 超期
	young := now - day                     // 未超期

	// pOld:未钉待审超期 → 整条删(Whole=true)
	// pYoung:待审未超期 → 不清
	// pPinned:待审超期但钉住 → 豁免
	// rOld:未钉已审超期 → 仅文件(Whole=false)
	// rYoung:已审未超期 → 不清
	// rPinned:已审超期但钉住 → 豁免
	// rDone:已审超期但文件已清过 → 不重复清
	type seed struct {
		id      string
		created int64
		review  bool
		pin     bool
		markDel bool
	}
	seeds := []seed{
		{"pOld", old, false, false, false},
		{"pYoung", young, false, false, false},
		{"pPinned", old, false, true, false},
		{"rOld", old, true, false, false},
		{"rYoung", young, true, false, false},
		{"rPinned", old, true, true, false},
		{"rDone", old, true, false, true},
	}
	for _, sd := range seeds {
		mustCreate(t, s, sd.id)
		mustAddVariant(t, s, sd.id, 1, "A", "html")
		setCreatedAt(t, s, sd.id, sd.created)
		if sd.review {
			if err := s.SaveReview(sd.id, "approve", 0, ""); err != nil {
				t.Fatalf("SaveReview(%s): %v", sd.id, err)
			}
		}
		if sd.pin {
			if err := s.SetPinned(sd.id, true); err != nil {
				t.Fatalf("SetPinned(%s): %v", sd.id, err)
			}
		}
		if sd.markDel {
			if err := s.MarkFilesDeleted(sd.id); err != nil {
				t.Fatalf("MarkFilesDeleted(%s): %v", sd.id, err)
			}
		}
	}

	due, err := s.DueFileCleanup(now, pageDays)
	if err != nil {
		t.Fatalf("DueFileCleanup: %v", err)
	}
	got := map[string]bool{}
	for _, d := range due {
		got[d.ID] = d.Whole
	}
	if len(due) != 2 {
		t.Errorf("应恰好 2 条到期项,得到 %d:%v", len(due), due)
	}
	if whole, ok := got["pOld"]; !ok || !whole {
		t.Errorf("pOld 应整条删(Whole=true),得到 存在=%v whole=%v", ok, whole)
	}
	if whole, ok := got["rOld"]; !ok || whole {
		t.Errorf("rOld 应仅清文件(Whole=false),得到 存在=%v whole=%v", ok, whole)
	}
	for _, absent := range []string{"pYoung", "pPinned", "rYoung", "rPinned", "rDone"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s 不应出现在文件清理清单", absent)
		}
	}
}

func TestMarkFilesDeleted(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	mustAddVariant(t, s, "abc123", 1, "A", "html")
	if err := s.SaveReview("abc123", "approve", 0, ""); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	if err := s.MarkFilesDeleted("abc123"); err != nil {
		t.Fatalf("MarkFilesDeleted: %v", err)
	}
	got, _ := s.Get("abc123")
	if !got.FilesDeleted {
		t.Error("MarkFilesDeleted 后 FilesDeleted 应为 true")
	}
	if err := s.MarkFilesDeleted("zzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("对不存在提交标记应报 ErrNotFound,得到 %v", err)
	}
}

func TestDueRecordCleanup(t *testing.T) {
	s := testStore(t)
	now := time.Now().Unix()
	decisionDays := 90
	old := now - int64(decisionDays)*day - 100 // 记录超期
	young := now - day                         // 未超期

	// rOld:已审 reviewed_at 超期 → 该清
	// rYoung:已审未超期 → 留
	// rPinned:超期但钉住 → 豁免
	// pAncient:待审 created_at 再老也不进记录清理(锚点=reviewed_at)
	for _, id := range []string{"rOld", "rYoung", "rPinned", "pAncient"} {
		mustCreate(t, s, id)
		mustAddVariant(t, s, id, 1, "A", "html")
	}
	setCreatedAt(t, s, "pAncient", now-365*day)
	for _, id := range []string{"rOld", "rYoung", "rPinned"} {
		if err := s.SaveReview(id, "approve", 0, ""); err != nil {
			t.Fatalf("SaveReview(%s): %v", id, err)
		}
	}
	setReviewedAt(t, s, "rOld", old)
	setReviewedAt(t, s, "rYoung", young)
	setReviewedAt(t, s, "rPinned", old)
	if err := s.SetPinned("rPinned", true); err != nil {
		t.Fatalf("SetPinned(rPinned): %v", err)
	}

	got, err := s.DueRecordCleanup(now, decisionDays)
	if err != nil {
		t.Fatalf("DueRecordCleanup: %v", err)
	}
	if len(got) != 1 || got[0] != "rOld" {
		t.Errorf("记录清理应只含 [rOld],得到 %v", got)
	}
}

func TestDeleteRecord(t *testing.T) {
	s := testStore(t)
	mustCreate(t, s, "abc123")
	mustAddVariant(t, s, "abc123", 1, "A", "html")
	mustAddVariant(t, s, "abc123", 2, "B", "html")

	if err := s.DeleteRecord("abc123"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if _, err := s.Get("abc123"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后 Get 应报 ErrNotFound,得到 %v", err)
	}
	list, err := s.List("", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("删除后 List 应为空,得到 %d 条", len(list))
	}
	// 白盒:候选行必须一并清掉
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM variants WHERE submission_id=?`, "abc123").Scan(&n); err != nil {
		t.Fatalf("查候选残留: %v", err)
	}
	if n != 0 {
		t.Errorf("候选行应随提交一并删除,残留 %d 行", n)
	}
	if err := s.DeleteRecord("zzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除不存在提交应报 ErrNotFound,得到 %v", err)
	}
}
