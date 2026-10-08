package store

// Submission 一条提交及其裁决与候选清单。
type Submission struct {
	ID            string
	Title         string
	Note          string
	Status        string // pending | reviewed;reviewed 为终态
	Decision      string // approve | reject | choose;""=未裁
	ChosenVariant int    // decision=choose 时被选中的候选 seq;0=无
	Comment       string // 用户批注
	CreatedAt     int64  // Unix 秒;页面文件清理锚点(F3 定案)
	ReviewedAt    int64  // Unix 秒;0=未审;决策记录清理锚点(F3 定案)
	Pinned        bool   // 钉住豁免一切清理
	FilesDeleted  bool   // 页面文件是否已清(详情页据此转"页面已清理"禁用态)
	Variants      []Variant
}

// Variant 一个候选(一个可切换查看的 mock 方案)。
type Variant struct {
	ID           int64
	SubmissionID string
	Seq          int    // 候选序号,1 起
	Label        string // 展示标签
	Kind         string // html | zip
	Entry        string // 包内入口文件,固定 index.html
}

// DueCleanup 一条到期清理项:Whole=true 整条删(未钉待审超期,目录+记录);
// Whole=false 仅清页面文件(未钉已审超期,记录留档至决策保留期满)。
type DueCleanup struct {
	ID    string
	Whole bool
}
