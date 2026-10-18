package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type projectInfo struct {
	Key  string
	Name string
}

// project 返回当前来源项目;生产中每进程只计算一次。测试可注入
// server.detectProject,避免依赖真实 git。
func (s *server) project() projectInfo {
	s.projectOnce.Do(func() {
		if s.detectProject != nil {
			s.projectValue.Key, s.projectValue.Name = s.detectProject()
		}
	})
	return s.projectValue
}

func detectProject() (string, string) {
	wd, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	root := wd
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = wd
	cmd.SysProcAttr = procAttr()
	if out, err := cmd.Output(); err == nil {
		if got := strings.TrimSpace(string(out)); got != "" {
			root = got
		}
	}
	return normalizeProjectKey(root), filepath.Base(filepath.Clean(root))
}

func normalizeProjectKey(path string) string {
	key := filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}
