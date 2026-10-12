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
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
	"mockit/internal/mcp"
	"mockit/internal/server"
	"mockit/internal/upgrade"
)

// Version 是二进制版本;MCP 握手据此判断是否停旧起新。
const Version = "0.2.0"

// skillMD 是仓内技能正本,构建时内嵌(票 #12):install-skill 与后续升级
// 共用这一份,确保二进制内技能与 skills/mockit/SKILL.md 不会分叉。
//
//go:embed skills/mockit/SKILL.md
var skillMD []byte

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	removeOldBinary()
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
	case "install-skill":
		root, err := upgrade.DefaultSkillsRoot()
		if err != nil {
			fmt.Fprintln(os.Stderr, "install-skill:", err)
			os.Exit(1)
		}
		path, err := upgrade.InstallSkill(root, skillMD)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(path)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, usageText())
}

func usageText() string {
	return "用法: mockit serve | mockit mcp | mockit version | mockit install-skill\n"
}

// removeOldBinary 尽力清理同目录下升级残留的 <可执行名>.old(票 #12):
// 任何子命令启动时调用,删不掉静默跳过。
func removeOldBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real // 与升级替换目标一致:解析软链之后的真实路径
	}
	upgrade.RemoveOldBinary(exe)
}

// 确保 lifecycle 包进入编译(锁文件读写助手,serve/mcp 共用)。
var _ = lifecycle.LockPath
