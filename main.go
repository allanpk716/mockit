// Command mockit 是 mock 页面审核闭环服务:agent 经 MCP 提交,手机(NetBird)对比拍板,结果回传。
//
// 双模式单二进制:
//
//	mockit serve  — 常驻 HTTP server(页面展示、审核记录、清理);每机唯一
//	                实例(实例锁 D17:O_EXCL 建锁+OS 锁持有至退出)
//	mockit mcp    — stdio MCP server(agent 唯一提交/查询通道);工具调用时
//	                自动定位或拉起本机 serve(ensure-server 锁分离协议:只替换
//	                比自己旧的 serve、更新的直接复用,ADR 0001;冷启动经
//	                start.lock 互斥拉起)
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
	"mockit/internal/mcp"
	"mockit/internal/server"
	"mockit/internal/upgrade"
)

// Version 是二进制版本;MCP 握手据此判断是否停旧起新。
const Version = "0.3.0"

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
	case "upgrade":
		os.Exit(runUpgrade(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, usageText())
}

func usageText() string {
	return "用法: mockit serve | mockit mcp | mockit version | mockit install-skill | mockit upgrade [--version vX.Y.Z] [--check]\n"
}

// runUpgrade 是 `mockit upgrade` 的薄转发:--check 只查最新版本(强制刷新);
// 默认(或带 --version vX.Y.Z,含降级)执行完整升级,依赖取生产默认值,
// 升级逻辑与测试都在 internal/upgrade。
func runUpgrade(args []string) int {
	check := false
	version := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			check = true
		case "--version":
			i++
			if i >= len(args) || args[i] == "" {
				fmt.Fprintln(os.Stderr, "--version 需要版本号,如 --version v0.3.0")
				return 2
			}
			version = args[i]
		default:
			fmt.Fprintf(os.Stderr, "未知参数: %s\n", args[i])
			usage()
			return 2
		}
	}
	cfg, err := config.Load(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	if check { // 只查不装(--check 与 --version 同时给时以 --check 为准)
		res := upgrade.NewChecker(cfg.DataDir, Version).Check(context.Background(), true)
		fmt.Println(res)
		if res.State == upgrade.StateFailed {
			return 1
		}
		return 0
	}
	u, err := upgrade.ProdUpgrader(cfg.DataDir, Version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade:", err)
		return 1
	}
	rep, err := u.Upgrade(context.Background(), version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade:", err)
		return 1
	}
	fmt.Println(rep)
	return 0
}

// removeOldBinary 尽力清理同目录下升级残留的 <可执行名>.old(票 #12):
// 任何子命令启动时调用,删不掉静默跳过。
func removeOldBinary() {
	exe, err := upgrade.CurrentExePath()
	if err != nil {
		return
	}
	upgrade.RemoveOldBinary(exe)
}

// 确保 lifecycle 包进入编译(锁文件读写助手,serve/mcp 共用)。
var _ = lifecycle.LockPath
