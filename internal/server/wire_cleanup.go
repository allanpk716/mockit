package server

import (
	"log"

	"mockit/internal/cleanup"
	"mockit/internal/config"
	"mockit/internal/store"
)

// startCleanup 拉起清理循环(D5/F7):启动立即扫一遍,之后每 24 小时一扫;
// 页面文件/决策记录保留期取自配置。返回的 stop 在优雅退出时调用(阻塞至循环退出)。
//
// 接线说明:本函数供 Serve(server.go,非本票路径)以 go startCleanup(...) 起用、
// 退出前调 stop;该一行接线由协调者在票落地时补齐,此处保持签名自洽、开箱即调。
func startCleanup(cfg *config.Config, st *store.Store, lg *log.Logger) (stop func()) {
	return cleanup.Start(st, cfg.DataDir, cfg.PageRetentionDays, cfg.DecisionRetentionD, lg, nil, 0)
}
