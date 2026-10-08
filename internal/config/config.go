// Package config 加载 mockit 配置(文件 + 环境变量覆盖)。
//
// 优先级:默认值 < ~/.mockit/config.json < 环境变量。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 是 mockit serve/mcp 的运行配置。
type Config struct {
	Port               int    // 监听端口;被占时 serve 自动 +1 漂移(最多 +10)
	Addr               string // 监听地址,默认 0.0.0.0
	DataDir            string // 数据目录,默认 ~/.mockit/
	ExternalURL        string // 对外通告基址;仅 host(语义定案前不参与 URL 拼接,见票 08/F1)
	PageRetentionDays  int    // 页面文件保留期,默认 14(锚点=提交时间)
	DecisionRetentionD int    // 决策记录保留期,默认 90(锚点=审核完成时间)
}

// fileConfig 是 config.json 的键,与 Config 字段一一对应。
// 用指针区分"键未出现"(nil,保持现值)与"键出现"(覆盖),避免零值歧义。
type fileConfig struct {
	Port                  *int    `json:"port"`
	Addr                  *string `json:"addr"`
	DataDir               *string `json:"data_dir"`
	PageRetentionDays     *int    `json:"page_retention_days"`
	DecisionRetentionDays *int    `json:"decision_retention_days"`
	ExternalURL           *string `json:"external_url"`
}

// envOverride 把单个环境变量应用到配置;空值视为未设置。
type envOverride struct {
	name  string
	apply func(c *Config, v string) error
}

// Load 从默认位置(~/.mockit/config.json)与环境变量构造配置。
// flags 为预留的命令行参数位,当前忽略。
func Load(flags []string) (*Config, error) {
	_ = flags
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("config: 无法确定家目录: %w", err)
	}

	cfg := Default()

	// 1) 配置文件:不存在用默认;存在但坏 JSON 报错(静默忽略坏配置更危险)。
	path := filepath.Join(home, ".mockit", "config.json")
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		var fc fileConfig
		if err := json.Unmarshal(b, &fc); err != nil {
			return nil, fmt.Errorf("config: 解析 %s 失败: %w", path, err)
		}
		if fc.Port != nil {
			cfg.Port = *fc.Port
		}
		if fc.Addr != nil {
			cfg.Addr = *fc.Addr
		}
		if fc.DataDir != nil {
			cfg.DataDir = *fc.DataDir
		}
		if fc.PageRetentionDays != nil {
			cfg.PageRetentionDays = *fc.PageRetentionDays
		}
		if fc.DecisionRetentionDays != nil {
			cfg.DecisionRetentionD = *fc.DecisionRetentionDays
		}
		if fc.ExternalURL != nil {
			cfg.ExternalURL = *fc.ExternalURL // 仅透传,校验属票 08/F1
		}
	case os.IsNotExist(err):
		// 没有配置文件,直接用默认值。
	default:
		return nil, fmt.Errorf("config: 读取 %s 失败: %w", path, err)
	}

	// 2) 环境变量覆盖。
	for _, o := range []envOverride{
		{"MOCKIT_PORT", func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("config: 环境变量 MOCKIT_PORT 不是有效整数: %q", v)
			}
			c.Port = n
			return nil
		}},
		{"MOCKIT_ADDR", func(c *Config, v string) error { c.Addr = v; return nil }},
		{"MOCKIT_DATA_DIR", func(c *Config, v string) error { c.DataDir = v; return nil }},
		{"MOCKIT_PAGE_RETENTION_D", func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("config: 环境变量 MOCKIT_PAGE_RETENTION_D 不是有效整数: %q", v)
			}
			c.PageRetentionDays = n
			return nil
		}},
		{"MOCKIT_DECISION_RETENTION_D", func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("config: 环境变量 MOCKIT_DECISION_RETENTION_D 不是有效整数: %q", v)
			}
			c.DecisionRetentionD = n
			return nil
		}},
	} {
		if v := os.Getenv(o.name); v != "" {
			if err := o.apply(cfg, v); err != nil {
				return nil, err
			}
		}
	}

	// 3) DataDir 统一家目录展开 + 解析为绝对路径。
	cfg.DataDir, err = resolveDataDir(cfg.DataDir, home)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// Default 返回默认配置(DataDir 已按当前家目录展开为绝对路径;
// 家目录不可得时退回相对目录 ".mockit",由 Load 再做绝对化)。
func Default() *Config {
	dataDir := ".mockit"
	if home, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(home, ".mockit")
	}
	return &Config{
		Port:               8321,
		Addr:               "0.0.0.0",
		DataDir:            dataDir,
		ExternalURL:        "",
		PageRetentionDays:  14,
		DecisionRetentionD: 90,
	}
}

// resolveDataDir 展开 ~ 前缀并把相对路径解析为绝对路径。
func resolveDataDir(p, home string) (string, error) {
	p = expandHome(p, home)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("config: 解析数据目录 %q 为绝对路径失败: %w", p, err)
		}
		p = abs
	}
	return p, nil
}

// expandHome 把开头的 ~ 或 ~/、~\ 替换为家目录(不支持 ~user 形式)。
// home 为空时原样返回,由调用方决定是否报错。
func expandHome(p, home string) string {
	if home == "" {
		return p
	}
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\"):
		return filepath.Join(home, p[2:])
	default:
		return p
	}
}
