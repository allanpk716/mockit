package server

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"mockit/internal/store"
)

//go:embed web
var webFiles embed.FS

// idRe 合法提交 id:6 位小写 base36(服务端生成,拒绝 URL 注入的其他形态)。
var idRe = regexp.MustCompile(`^[0-9a-z]{6}$`)

// Server 聚合 HTTP 面的全部依赖;路由在 newServer 注册。
type Server struct {
	dataDir  string
	store    *store.Store
	token    string // shutdown 校验用,与 lock.token 同源
	lg       *log.Logger
	stop     chan struct{} // shutdown 命中后关闭,Serve 据此优雅退出
	stopOnce sync.Once
	mux      *http.ServeMux
}

// newServer 构造 HTTP 面(不含端口绑定/lock/信号,那些属 Serve)。
// lg 为 nil 时静默(测试用)。
func newServer(dataDir string, st *store.Store, token string, lg *log.Logger) *Server {
	if lg == nil {
		lg = log.New(io.Discard, "", log.LstdFlags)
	}
	s := &Server{
		dataDir: dataDir,
		store:   st,
		token:   token,
		lg:      lg,
		stop:    make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleListPage)
	mux.HandleFunc("GET /s/{id}", s.handleDetailPage)
	// 注:pattern wildcard 须占满整段,故壳页捕获整段 {v} 再校验形如 v数字
	mux.HandleFunc("GET /s/{id}/{v}", s.handleVariantPage)
	mux.HandleFunc("GET /raw/{id}/{n}/{path...}", s.handleRaw)
	mux.HandleFunc("POST /api/submissions", s.handleSubmit)
	mux.HandleFunc("GET /api/submissions", s.handleListAPI)
	mux.HandleFunc("GET /api/submissions/{id}", s.handleDetailAPI)
	mux.HandleFunc("POST /api/review", s.handleReview)
	mux.HandleFunc("POST /api/pin", s.handlePin)
	mux.HandleFunc("GET /ping", s.handlePing)
	mux.HandleFunc("POST /shutdown", s.handleShutdown)
	s.mux = mux
	return s
}

// Handler 返回带请求日志的根 handler。
func (s *Server) Handler() http.Handler { return s.withLog(s.mux) }

// statusWriter 记录响应状态码(请求日志用)。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.lg.Printf("%s %s %d %s", r.Method, r.URL.RequestURI(), sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// ---- 通用输出 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// storeStatus 把 store 哨兵错误映射为 HTTP 状态码。
func storeStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrAlreadyReviewed):
		return http.StatusConflict // 已审终态,明确 409
	case errors.Is(err, store.ErrInvalidVariant),
		errors.Is(err, store.ErrInvalidDecision),
		errors.Is(err, store.ErrInvalidKind):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// ---- 页面(占位,票 04 整体替换) ----

func (s *Server) servePage(w http.ResponseWriter, name string) {
	b, err := webFiles.ReadFile("web/" + name)
	if err != nil {
		http.Error(w, "页面缺失", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) handleListPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "index.html")
}

func (s *Server) handleDetailPage(w http.ResponseWriter, r *http.Request) {
	if !validID(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, "提交不存在")
		return
	}
	s.servePage(w, "detail.html")
}

func (s *Server) handleVariantPage(w http.ResponseWriter, r *http.Request) {
	v := r.PathValue("v")
	// 壳页段形如 v{n}(n 为候选序号);非该形态一律 404
	if !validID(r.PathValue("id")) || len(v) < 2 || v[0] != 'v' || !isDigits(v[1:]) {
		writeErr(w, http.StatusNotFound, "提交不存在")
		return
	}
	s.servePage(w, "variant.html")
}

// ---- 静态文件 ----

// handleRaw 服务候选目录内文件:严格防目录穿越(Join 清洗后必须仍在
// <data>/{id}/v{n} 内,双保险防 ..、反斜杠、绝对路径形态),目录不列。
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	id, n, path := r.PathValue("id"), r.PathValue("n"), r.PathValue("path")
	if !validID(id) || !isDigits(n) || path == "" {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	root := filepath.Join(s.dataDir, id, "v"+n)
	target, err := safeJoin(root, path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	// 不用 http.ServeFile:它对 URL 以 /index.html 结尾的请求强制 301 到 ./,
	// 而候选入口恰恰固定叫 index.html。ServeContent 无此劫持,Range 等照常。
	f, err := os.Open(target)
	if err != nil {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	defer f.Close()
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// ---- API ----

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	// 宽松护栏:6×20MB zip 的 base64(约 27MB/个)+ 余量;防误用,非业务规则
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	var req submitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	sub, aerr := s.createSubmission(&req)
	if aerr != nil {
		writeErr(w, aerr.code, aerr.msg)
		return
	}
	writeJSON(w, http.StatusCreated, subToJSON(sub, s.noteOf(sub.ID)))
}

func (s *Server) handleListAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 0
	if ls := q.Get("limit"); ls != "" {
		n, err := strconv.Atoi(ls)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "limit 无效")
			return
		}
		limit = n
	}
	subs, err := s.store.List(q.Get("status"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]submissionJSON, 0, len(subs))
	for _, sub := range subs {
		out = append(out, subToJSON(sub, s.noteOf(sub.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDetailAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		writeErr(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	sub, err := s.store.Get(id)
	if err != nil {
		writeErr(w, storeStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subToJSON(sub, s.noteOf(id)))
}

type reviewReq struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // approve | reject | choose
	Variant  *int   `json:"variant"`  // choose 时必填(指针区分未携带与 0)
	Comment  string `json:"comment"`
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var req reviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	var chosen int
	if req.Decision == store.DecisionChoose && req.Variant == nil {
		writeErr(w, http.StatusBadRequest, "choose 必须携带 variant")
		return
	}
	if req.Variant != nil {
		chosen = *req.Variant
	}
	if err := s.store.SaveReview(req.ID, req.Decision, chosen, req.Comment); err != nil {
		writeErr(w, storeStatus(err), err.Error())
		return
	}
	sub, err := s.store.Get(req.ID)
	if err != nil {
		writeErr(w, storeStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subToJSON(sub, s.noteOf(sub.ID)))
}

type pinReq struct {
	ID     string `json:"id"`
	Pinned bool   `json:"pinned"`
}

func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	var req pinReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	if err := s.store.SetPinned(req.ID, req.Pinned); err != nil {
		writeErr(w, storeStatus(err), err.Error())
		return
	}
	sub, err := s.store.Get(req.ID)
	if err != nil {
		writeErr(w, storeStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, subToJSON(sub, s.noteOf(sub.ID)))
}

// ---- 线上契约端点(lifecycle 票 01 定义;mcp 据此探测/关停) ----

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": Version})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Mockit-Token") != s.token {
		writeErr(w, http.StatusForbidden, "token 不对")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	s.stopOnce.Do(func() { close(s.stop) })
}

// ---- JSON 形状(F6 定案:详情/列表一律不含 URL) ----

type variantJSON struct {
	Seq     int    `json:"seq"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Entry   string `json:"entry"`
	Cleaned bool   `json:"cleaned"` // files_deleted 时候选带"页面已清理"标记
}

type submissionJSON struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Note          string        `json:"note"`
	Status        string        `json:"status"`
	Decision      string        `json:"decision"`
	ChosenVariant int           `json:"chosen_variant"`
	Comment       string        `json:"comment"`
	CreatedAt     int64         `json:"created_at"`
	ReviewedAt    int64         `json:"reviewed_at"`
	Pinned        bool          `json:"pinned"`
	FilesDeleted  bool          `json:"files_deleted"`
	Variants      []variantJSON `json:"variants"`
}

func subToJSON(sub *store.Submission, note string) submissionJSON {
	out := submissionJSON{
		ID:            sub.ID,
		Title:         sub.Title,
		Note:          note,
		Status:        sub.Status,
		Decision:      sub.Decision,
		ChosenVariant: sub.ChosenVariant,
		Comment:       sub.Comment,
		CreatedAt:     sub.CreatedAt,
		ReviewedAt:    sub.ReviewedAt,
		Pinned:        sub.Pinned,
		FilesDeleted:  sub.FilesDeleted,
		Variants:      make([]variantJSON, 0, len(sub.Variants)),
	}
	for _, v := range sub.Variants {
		out.Variants = append(out.Variants, variantJSON{
			Seq:     v.Seq,
			Label:   v.Label,
			Kind:    v.Kind,
			Entry:   v.Entry,
			Cleaned: sub.FilesDeleted,
		})
	}
	return out
}

// ---- 小工具 ----

func validID(id string) bool { return idRe.MatchString(id) }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// genID 生成 6 位 base36 提交 id(crypto/rand 均匀取 [0,36^6),前导补零)。
func genID() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(2176782336)) // 36^6
	if err != nil {
		return "", fmt.Errorf("生成 id: %w", err)
	}
	s := n.Text(36)
	for len(s) < 6 {
		s = "0" + s
	}
	return s, nil
}

// genToken 生成 shutdown 校验用的随机 token(hex)。
func genToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
