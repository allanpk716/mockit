// Command mockit 是 mock 页面审核闭环服务:agent 经 MCP 提交,手机(NetBird)对比拍板,结果回传。
//
// 双模式单二进制:
//
//	mockit serve  — 常驻 HTTP server(页面展示、审核记录、清理)
//	mockit mcp    — stdio MCP server(agent 唯一提交/查询通道;自动确保本机 serve 存活)
package main

import (
	"fmt"
	"os"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
	"mockit/internal/mcp"
	"mockit/internal/server"
)

// Version 是二进制版本;MCP 握手据此判断是否停旧起新。
const Version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cfg, err := config.Load(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
		os.Exit(server.Serve(cfg))
	case "mcp":
		cfg, err := config.Load(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
		os.Exit(mcp.Run(cfg))
	case "version":
		fmt.Println(Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, "用法: mockit serve | mockit mcp | mockit version\n")
}

// 确保 lifecycle 包进入编译(锁文件读写助手,serve/mcp 共用)。
var _ = lifecycle.LockPath
