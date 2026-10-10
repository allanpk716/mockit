// Command mockit 是 mock 页面审核闭环服务:agent 经 MCP 提交,手机(NetBird)对比拍板,结果回传。
//
// 双模式单二进制:
//
//	mockit serve  — 常驻 HTTP server(页面展示、审核记录、清理);每机唯一
//	                实例(实例锁 D17:O_EXCL 建锁+OS 锁持有至退出)
//	mockit mcp    — stdio MCP server(agent 唯一提交/查询通道);工具调用时
//	                自动定位或拉起本机 serve(ensure-server 锁分离协议:同版本
//	                复用、版本不符停旧换新、冷启动经 start.lock 互斥拉起)
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
const Version = "0.1.2"

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
