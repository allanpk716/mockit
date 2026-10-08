// Package mcp 实现 stdio MCP server:agent 提交/查询的唯一通道。
package mcp

import (
	"fmt"
	"os"

	"mockit/internal/config"
)

// Run 启动 stdio MCP server,返回进程退出码(票 05 落地)。
func Run(cfg *config.Config) int {
	fmt.Fprintln(os.Stderr, "mockit mcp: 尚未实施(票 05)")
	return 1
}
