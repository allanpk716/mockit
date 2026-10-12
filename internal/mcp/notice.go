package mcp

// 每进程一次的附加提示(ADR 0001/票 13):工具结果末尾追加一段文字,
// 每类提示每个进程至多附一次;#11 的新版提醒票复用此机制。
//
// add 登记待发提示(kind 相同以最新文本为准,已发过则忽略);flush 取走
// 全部待发并标记已发。并发下多个工具调用同时收尾时,提示只落进最先
// flush 的那个结果——恰好一个结果携带提示。

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// noticeKindServeNewer 是"本会话 MCP 旧于 serve"提示的类别键。
const noticeKindServeNewer = "serve-newer"

// serveNewerNotice 生成"MCP 旧于 serve"提示文本(票 13 验收文案)。
func serveNewerNotice(mcpVer, serveVer string) string {
	return fmt.Sprintf("本会话 MCP 版本(%s)旧于 serve(%s),请完全退出并重启 Claude Code", mcpVer, serveVer)
}

// noticeBoard 收集每进程至多发一次的提示。
type noticeBoard struct {
	mu      sync.Mutex
	pending map[string]string // kind → 待随下一个工具结果发出的文本
	shown   map[string]bool   // kind → 已发出
}

// newNoticeBoard 建空提示板。
func newNoticeBoard() *noticeBoard {
	return &noticeBoard{pending: map[string]string{}, shown: map[string]bool{}}
}

// add 登记一类待发提示;该类已发过则忽略(每进程一次)。nil 板为无操作,
// 供未初始化提示板的测试构造兜底。
func (n *noticeBoard) add(kind, text string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.shown[kind] {
		return
	}
	if n.pending == nil {
		n.pending = map[string]string{}
	}
	n.pending[kind] = text
}

// flush 取走全部待发提示并标记已发;返回可直接追加到工具结果末尾的文本
// (无待发时为空串)。多类提示按文本排序拼接,输出确定。
func (n *noticeBoard) flush() string {
	if n == nil {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.pending) == 0 {
		return ""
	}
	parts := make([]string, 0, len(n.pending))
	for kind, text := range n.pending {
		parts = append(parts, text)
		n.shown[kind] = true
		delete(n.pending, kind)
	}
	sort.Strings(parts)
	return "\n\n" + strings.Join(parts, "\n")
}
