package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mockit/internal/store"
)

// maxZipBytes 单个 zip 候选解码后的字节上限(规格:20MB)。
const maxZipBytes = 20 << 20

// entryFileName 单 HTML 落盘名与 zip 根入口,规格定死(与 store 内 entryFile 同值)。
const entryFileName = "index.html"

// apiError 带 HTTP 状态码的业务错误。
type apiError struct {
	code int
	msg  string
}

func (e *apiError) Error() string { return e.msg }

// submitReq 是 POST /api/submissions 的请求体。
type submitReq struct {
	Title    string             `json:"title"`
	Note     string             `json:"note"` // 入库 submissions.note(随记录留档至决策保留期满)
	Variants []submitVariantReq `json:"variants"`
}

type submitVariantReq struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"` // html | zip
	ContentB64 string `json:"content_b64"`
}

// createSubmission 走完整提交管道:校验(id 占位)→ 落盘 → 入库;
// 任一步失败则清理已落盘目录,不留半截提交。
func (s *Server) createSubmission(req *submitReq) (*store.Submission, *apiError) {
	if n := len(req.Variants); n < 1 || n > 6 {
		return nil, &apiError{code: 400, msg: fmt.Sprintf("候选数应 1~6,得 %d", n)}
	}

	// 1) 预解码与校验(纯内存,未落盘前全拒)
	type prepared struct {
		label string
		kind  string
		data  []byte
		zr    *zip.Reader
	}
	ps := make([]prepared, 0, len(req.Variants))
	for i, v := range req.Variants {
		if v.Kind != store.KindHTML && v.Kind != store.KindZip {
			return nil, &apiError{code: 400, msg: fmt.Sprintf("候选 %d 形态无效: %q", i+1, v.Kind)}
		}
		data, err := base64.StdEncoding.DecodeString(v.ContentB64)
		if err != nil {
			return nil, &apiError{code: 400, msg: fmt.Sprintf("候选 %d base64 无效: %v", i+1, err)}
		}
		p := prepared{label: v.Label, kind: v.Kind, data: data}
		if v.Kind == store.KindZip {
			if len(data) > maxZipBytes {
				return nil, &apiError{code: 400, msg: fmt.Sprintf("候选 %d 超 20MB 上限(%d 字节)", i+1, len(data))}
			}
			zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return nil, &apiError{code: 400, msg: fmt.Sprintf("候选 %d 不是有效 zip: %v", i+1, err)}
			}
			if err := validateZipEntries(zr); err != nil {
				return nil, &apiError{code: 400, msg: fmt.Sprintf("候选 %d 被拒: %v", i+1, err)}
			}
			p.zr = zr
		}
		ps = append(ps, p)
	}

	// 2) 生成 id(Mkdir 独占创建做原子占位,撞 id 换一个)
	var id, dir string
	for attempt := 0; attempt < 5; attempt++ {
		cand, err := genID()
		if err != nil {
			return nil, &apiError{code: 500, msg: err.Error()}
		}
		d := filepath.Join(s.dataDir, cand)
		if err := os.Mkdir(d, 0o755); err != nil {
			if os.IsExist(err) {
				continue // 撞 id,重试
			}
			return nil, &apiError{code: 500, msg: "创建提交目录失败: " + err.Error()}
		}
		id, dir = cand, d
		break
	}
	if id == "" {
		return nil, &apiError{code: 500, msg: "无法生成唯一提交 id"}
	}

	fail := func(code int, msg string) (*store.Submission, *apiError) {
		_ = os.RemoveAll(dir)
		return nil, &apiError{code: code, msg: msg}
	}

	// 3) 落盘:逐候选写 <data>/{id}/v{n}/
	for i, p := range ps {
		vdir := filepath.Join(dir, "v"+strconv.Itoa(i+1))
		if p.kind == store.KindHTML {
			if err := os.MkdirAll(vdir, 0o755); err != nil {
				return fail(500, "落盘失败: "+err.Error())
			}
			if err := os.WriteFile(filepath.Join(vdir, entryFileName), p.data, 0o644); err != nil {
				return fail(500, "落盘失败: "+err.Error())
			}
		} else {
			if err := extractZip(vdir, p.zr); err != nil {
				return fail(500, fmt.Sprintf("候选 %d 解包失败: %v", i+1, err))
			}
		}
	}

	// 4) 入库(候选登记,note 一并入库——留档 90 天,不随 14 天文件清理丢失);
	//    失败回滚目录与记录
	if err := s.store.CreateSubmission(id, req.Title, req.Note); err != nil {
		return fail(500, "入库失败: "+err.Error())
	}
	for i, p := range ps {
		if err := s.store.AddVariant(id, i+1, p.label, p.kind); err != nil {
			_ = s.store.DeleteRecord(id)
			return fail(500, "登记候选失败: "+err.Error())
		}
	}
	sub, err := s.store.Get(id)
	if err != nil {
		return fail(500, "读回提交失败: "+err.Error())
	}
	return sub, nil
}

// validateZipEntries 解包前全量校验 zip 条目:拒绝绝对路径、.. 上跳段、
// 符号链接条目;必须含根 index.html。
func validateZipEntries(r *zip.Reader) error {
	hasRoot := false
	for _, f := range r.File {
		name := f.Name
		if name == entryFileName {
			hasRoot = true
		}
		if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
			return fmt.Errorf("条目 %q 为绝对路径", name)
		}
		// zip 规范以 / 分隔,但恶意条目可塞 \;两种分隔符都按段检查
		for _, seg := range strings.FieldsFunc(name, func(c rune) bool { return c == '/' || c == '\\' }) {
			if seg == ".." {
				return fmt.Errorf("条目 %q 含 .. 上跳段", name)
			}
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("条目 %q 为符号链接", name)
		}
	}
	if !hasRoot {
		return errors.New("zip 缺根 index.html 入口")
	}
	return nil
}

// extractZip 把已校验的 zip 解包到 dst。每条目再过一次 safeJoin(双重校验,
// 校验与解包之间的任何遗漏都被这层挡住)。解压总量上限未采纳(F4),不做。
func extractZip(dst string, r *zip.Reader) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range r.File {
		target, err := safeJoin(dst, f.Name)
		if err != nil {
			return fmt.Errorf("条目 %q: %w", f.Name, err)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeFileFromZip(target, f); err != nil {
			return err
		}
	}
	return nil
}

func writeFileFromZip(target string, f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// safeJoin 把 zip/URL 提供的名字安全拼进 root:filepath.Join 清洗后必须
// 仍严格位于 root 之下,否则视为目录穿越拒绝(防 ../ 上跳、绝对路径、
// Windows 盘符与反斜杠形态——Join+前缀校验对 \ 在 Windows 上同样生效)。
func safeJoin(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	if target == root || !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %q 越出候选目录", name)
	}
	return target, nil
}

// noteOf 读提交说明:优先库值(submissions.note,随记录留档 90 天,
// 不被 14 天文件清理带走);库值为空且存量边车 note.txt 存在时读文件,
// 兼容 note 入库改造前的老数据。
func (s *Server) noteOf(sub *store.Submission) string {
	if sub.Note != "" {
		return sub.Note
	}
	if !validID(sub.ID) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(s.dataDir, sub.ID, "note.txt"))
	if err != nil {
		return ""
	}
	return string(b)
}
