// Package mcp 实现 stdio MCP server:agent 提交/查询的唯一通道(D3)。
//
// 协议:行分隔 JSON-RPC 2.0。stdout 只输出协议响应,一切日志走 stderr。
// serve 定位:读 <data>/server.lock 取端口并 ping 探活;失败即明确报
// "serve 未运行,请先手工运行 mockit serve"(进程拉起/互斥/版本换新属票 08,
// 本包绝不 spawn 进程)。
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

// Version 与 main 包的 Version 常量保持同步(main 不可导入,此处复制;改动需两处同改)。
const Version = "0.1.0"

// defaultProtocolVersion 是客户端 initialize 未携带 protocolVersion 时的回显缺省值。
const defaultProtocolVersion = "2024-11-05"

// JSON-RPC 标准错误码(坏 JSON 行无法定位 id,按跳过处理,不走 -32700)。
const (
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// server 承载一次 stdio MCP 会话;readLock/ping/baseOverride 为测试注入点。
type server struct {
	cfg    *config.Config
	out    io.Writer // stdout:只写协议响应
	errOut io.Writer // stderr:只写日志

	// readLock 缺省 lifecycle.ReadLock;测试注入自造 lock 夹具,不依赖票 01 实现。
	readLock func(dataDir string) (*lifecycle.Lock, error)
	// ping 缺省本包 pingServe(500ms 探活)。lifecycle.Ping 属票 01 交付,
	// 为不阻塞编译本包自带等价实现;票 01 落地后可整体切换。
	ping func(port int) error

	httpc *http.Client

	// baseOverride 非""时直接作为 API 基址(仅测试注入,绕过 lock/ping 定位)。
	baseOverride string
}

// Run 启动 stdio MCP server,阻塞至 stdin EOF,返回进程退出码。
func Run(cfg *config.Config) int {
	if cfg == nil {
		cfg = config.Default()
	}
	s := &server{
		cfg:      cfg,
		out:      os.Stdout,
		errOut:   os.Stderr,
		readLock: lifecycle.ReadLock,
		ping:     pingServe,
		httpc:    &http.Client{Timeout: 60 * time.Second},
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

// loop 逐行读取并处理,直到 stdin EOF;返回退出码。
func (s *server) loop(in io.Reader) int {
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
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
		// 通知(notifications/initialized 等)不回应;其余通知记 stderr 便于排障。
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
	text, isErr := s.safeCallTool(p.Name, p.Arguments)
	s.reply(id, toolCallResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: isErr,
	}, nil)
}

// safeCallTool 兜底 panic,保证单个工具故障不杀死协议循环。
func (s *server) safeCallTool(name string, args json.RawMessage) (text string, isErr bool) {
	defer func() {
		if r := recover(); r != nil {
			text = fmt.Sprintf("工具内部错误: %v", r)
			isErr = true
		}
	}()
	return s.callTool(name, args)
}

// reply 写一行协议响应到 stdout。
func (s *server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	b, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		s.logf("序列化响应失败: %v", err)
		return
	}
	if _, err := fmt.Fprintf(s.out, "%s\n", b); err != nil {
		s.logf("写 stdout 失败: %v", err)
	}
}

func (s *server) logf(format string, args ...any) {
	fmt.Fprintf(s.errOut, "mcp: "+format+"\n", args...)
}
