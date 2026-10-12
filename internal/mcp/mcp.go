// Package mcp 实现 stdio MCP server:agent 提交/查询的唯一通道(D3)。
//
// 协议:行分隔 JSON-RPC 2.0。stdout 只输出协议响应,一切日志走 stderr。
// 并发模型(规格 #4 票 08):工具调用各自在 goroutine 里执行、响应写出
// 串行化;notifications/cancelled 中断对应在飞的 HTTP 请求且不再回复。
// serve 定位:ensure-server(锁分离协议 D17,票 08)——读 server.lock
// 复用/停旧换新/冷启动拉起(经 start.lock 互斥,spawn 零闪窗)。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
	"mockit/internal/upgrade"
)

// Version 与 main 包的 Version 常量保持同步(main 不可导入,此处复制;改动需两处同改)。
const Version = "0.2.0"

// defaultProtocolVersion 是客户端 initialize 未携带 protocolVersion 时的回显缺省值。
const defaultProtocolVersion = "2024-11-05"

// JSON-RPC 标准错误码(坏 JSON 行无法定位 id,按跳过处理,不走 -32700)。
const (
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// server 承载一次 stdio MCP 会话;ensure 为 serve 定位/拉起注入点。
type server struct {
	cfg    *config.Config
	out    io.Writer // stdout:只写协议响应
	errOut io.Writer // stderr:只写日志

	// ensure 定位或拉起本机 serve,返回实例 lock;缺省闭包 ensureServer
	// + defaultSpawnServe,测试注入假实现。lock 端口拼内部 API 基址,
	// lock.base_host+端口拼对外 URL(票 09/D16)。
	ensure func() (*lifecycle.Lock, error)

	httpc *http.Client

	// notices 每进程一次的附加提示(票 13/ADR 0001):工具结果末尾追加,
	// 每类(kind)每进程至多附一次;#11 的新版提醒复用此机制。
	notices *noticeBoard

	// up 是 mockit_upgrade 工具与后台新版检查的依赖(票 #16);nil 时
	// 工具报未配置、后台检查不进行。生产由 Run 构造,测试注入假 release。
	up *upgradeDeps

	// upd 是后台新版检查的进程内状态(在飞/已升级标记,票 #16);
	// onCheckDone 是每次后台检查收尾的回调,仅为测试同步用,生产为 nil。
	upd        updateCheck
	onCheckDone func(upgrade.Result)

	// 并发与串行化(规格 #4 票 08):工具调用各自在 goroutine 里执行,
	// stdout 响应经 writeMu 串行化(一行一条完整 JSON,不交错);
	// stderr 日志同样加锁(race 检测下并发写同一 io.Writer 才合法)。
	writeMu sync.Mutex
	errMu   sync.Mutex

	// wg 记录在飞的工具调用;stdin 关闭后 loop 等它清零再退场,
	// 避免半途丢弃已产生的响应。
	wg sync.WaitGroup

	// 取消(规格 #4 票 08):mu 保护 inflight,key 为规范化后的请求 id(idKey)。
	// 每次调用各持一个 inflightCall(cancel + 不再回复标记);同 id 重入时
	// 旧调用收尾只清理自己名下的登记(按指针比对),不得删掉新调用的
	// cancel/标记——否则新调用变得不可取消、抑制标记被吞(评审修复)。
	mu       sync.Mutex
	inflight map[string]*inflightCall
}

// inflightCall 登记一次在飞工具调用:cancel 中断其正在进行的 HTTP 请求;
// cancelled 置位后共 goroutine 收尾不再回复(MCP 规范:已取消的请求不回应;
// 被同 id 重入顶替的旧调用同样置位,同一 id 至多由最新调用应答)。
type inflightCall struct {
	cancel    context.CancelFunc
	cancelled bool
}

// Run 启动 stdio MCP server,阻塞至 stdin EOF,返回进程退出码。
func Run(cfg *config.Config) int {
	if cfg == nil {
		cfg = config.Default()
	}
	s := &server{
		cfg:    cfg,
		out:    os.Stdout,
		errOut: os.Stderr,
		ensure: func() (*lifecycle.Lock, error) {
			return ensureServer(cfg, defaultSpawnServe)
		},
		httpc: &http.Client{Timeout: 60 * time.Second},
		notices: newNoticeBoard(),
		up:     prodUpgradeDeps(cfg),
	}
	s.logf("mockit mcp %s 就绪(dataDir=%q)", Version, cfg.DataDir)
	return s.loop(os.Stdin)
}

// rpcRequest 是行分隔 JSON-RPC 请求信封;ID 缺失即为通知。
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// loop 逐行读取并处理,直到 stdin EOF;返回退出码。工具调用并发执行
// (handleLine 内起 goroutine),读循环不被慢调用堵住;退出前等待在飞
// 调用收尾,响应不半途丢弃。
func (s *server) loop(in io.Reader) int {
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			s.wg.Wait()
			if err == io.EOF {
				return 0
			}
			s.logf("读 stdin 失败: %v", err)
			return 1
		}
	}
}

// handleLine 处理单行:坏 JSON 行跳过并 stderr 记录;通知一律不回应;请求按方法分发。
func (s *server) handleLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		s.logf("跳过坏 JSON 行: %v", err)
		return
	}
	if req.ID == nil {
		// 通知一律不回应;notifications/cancelled 特判:中断对应在飞请求。
		if req.Method == "notifications/cancelled" {
			s.handleCancelled(req.Params)
			return
		}
		if req.Method != "notifications/initialized" {
			s.logf("忽略通知 %q", req.Method)
		}
		return
	}
	if req.Method == "" {
		s.reply(req.ID, nil, &rpcError{Code: codeInvalidRequest, Message: "缺少 method"})
		return
	}
	switch req.Method {
	case "initialize":
		s.reply(req.ID, s.initializeResult(req.Params), nil)
	case "ping":
		s.reply(req.ID, struct{}{}, nil)
	case "tools/list":
		s.reply(req.ID, toolsListResult{Tools: toolDefs()}, nil)
	case "tools/call":
		s.handleToolCall(req.ID, req.Params)
	default:
		s.reply(req.ID, nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method})
	}
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
}

// initializeResult 回显客户端请求的 protocolVersion(缺省 "2024-11-05")。
func (s *server) initializeResult(params json.RawMessage) initializeResult {
	var p initializeParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	pv := p.ProtocolVersion
	if pv == "" {
		pv = defaultProtocolVersion
	}
	return initializeResult{
		ProtocolVersion: pv,
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      serverInfo{Name: "mockit", Version: Version},
	}
}

// idKey 把请求 id 规范化为 map key:JSON 值解码后再编码(数字 3 与 3.0
// 同键),字符串保留引号;无法解析时退回原文。取消通知的 requestId 与
// 请求自身的 id 经此对齐。
func idKey(id json.RawMessage) string {
	if len(id) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(id, &v); err != nil {
		return string(id)
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return string(id)
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *server) handleToolCall(id json.RawMessage, params json.RawMessage) {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		s.reply(id, nil, &rpcError{Code: codeInvalidParams, Message: "tools/call 缺少 name 或参数非法"})
		return
	}
	// 后台新版检查(票 #16):在处理点同步判活、异步执行,不阻塞本次
	// 调用;mockit_upgrade 自身即查询/升级入口,不另起后台检查,避免
	// 同请求并发重复查与升完又提醒。
	if p.Name != "mockit_upgrade" {
		s.maybeCheckUpdate()
	}
	// 规格 #4 票 08:工具调用在独立 goroutine 里执行,慢调用不再堵住读循环
	// (ping / notifications/cancelled 随时可进);响应经 reply 串行写出,
	// 完成顺序允许与请求顺序不同(JSON-RPC 按 id 对账)。ctx 挂进在飞表,
	// 收到针对本 id 的取消通知即被中断。
	key := idKey(id)
	ctx, cancel := context.WithCancel(context.Background())
	call := &inflightCall{cancel: cancel}
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]*inflightCall{}
	}
	if old, ok := s.inflight[key]; ok {
		// 防御:MCP 不允许同 id 重入,真发生时先取消旧调用避免泄漏;
		// 旧调用被顶替,同样不再回复(同一 id 只由新调用应答)。
		old.cancelled = true
		old.cancel()
	}
	s.inflight[key] = call
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		text, isErr := s.safeCallTool(ctx, p.Name, p.Arguments)
		cancel() // 调用已结束,释放 ctx
		s.mu.Lock()
		suppress := call.cancelled
		// 只清理自己名下的登记:key 已归重入后的新调用时,老调用的
		// 收尾不得删掉新调用的 cancel/标记(否则新调用不可取消)。
		if s.inflight[key] == call {
			delete(s.inflight, key)
		}
		s.mu.Unlock()
		if suppress {
			// MCP 规范:已取消的请求不再回复(通知本身也无回应)。提示不
			// 消耗——留给下一个真正发出的结果(票 13)。
			return
		}
		// 每进程一次的附加提示只随真正发出的结果消耗(票 13):取消/被顶替
		// 的调用不携带,下一个发出的工具结果末尾再附。
		if suffix := s.notices.flush(); suffix != "" {
			text += suffix
		}
		s.reply(id, toolCallResult{
			Content: []toolContent{{Type: "text", Text: text}},
			IsError: isErr,
		}, nil)
	}()
}

// cancelParams 承接 notifications/cancelled(MCP 规范:requestId + 可选 reason)。
type cancelParams struct {
	RequestID json.RawMessage `json:"requestId"`
	Reason    string          `json:"reason"`
}

// handleCancelled 按 MCP 规范处理取消通知:请求仍在飞则中断其 HTTP 调用
// 并标记不再回复;已完成的 id 静默忽略;通知本身永不回应。
func (s *server) handleCancelled(params json.RawMessage) {
	var p cancelParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	key := idKey(p.RequestID)
	if key == "" {
		s.logf("取消通知缺 requestId,忽略")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	call, ok := s.inflight[key]
	if !ok {
		return // 已完成或从未存在:按规范忽略
	}
	call.cancelled = true
	call.cancel()
	s.logf("请求 %s 被取消(reason=%q):中断其在飞调用且不再回复", key, p.Reason)
}

// safeCallTool 兜底 panic,保证单个工具故障不杀死协议循环;ctx 随调用
// 下发,取消即中断其在飞的 HTTP 请求。
func (s *server) safeCallTool(ctx context.Context, name string, args json.RawMessage) (text string, isErr bool) {
	defer func() {
		if r := recover(); r != nil {
			text = fmt.Sprintf("工具内部错误: %v", r)
			isErr = true
		}
	}()
	return s.callTool(ctx, name, args)
}

// reply 写一行协议响应到 stdout;writeMu 保证并发调用下每条响应独占
// 一行完整 JSON,不交错。
func (s *server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	b, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		s.logf("序列化响应失败: %v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := fmt.Fprintf(s.out, "%s\n", b); err != nil {
		s.logf("写 stdout 失败: %v", err)
	}
}

func (s *server) logf(format string, args ...any) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	fmt.Fprintf(s.errOut, "mcp: "+format+"\n", args...)
}
