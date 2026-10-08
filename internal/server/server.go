// Package server 实现 mockit serve:常驻 HTTP server。
package server

import (
	"fmt"
	"os"

	"mockit/internal/config"
)

// Serve 启动常驻 HTTP server,返回进程退出码(票 02/03/04 落地)。
func Serve(cfg *config.Config) int {
	fmt.Fprintln(os.Stderr, "mockit serve: 尚未实施(票 02)")
	return 1
}
