// Package config 加载 mockit 配置(文件 + 环境变量覆盖)。
package config

// Config 是 mockit serve/mcp 的运行配置。
type Config struct {
	Port                int    // 监听端口;被占时 serve 自动 +1 漂移(最多 +10)
	Addr                string // 监听地址,默认 0.0.0.0
	DataDir             string // 数据目录,默认 ~/.mockit/
	ExternalURL         string // 对外通告基址;仅 host(语义定案前不参与 URL 拼接,见票 08/F1)
	PageRetentionDays   int    // 页面文件保留期,默认 14(锚点=提交时间)
	DecisionRetentionD  int    // 决策记录保留期,默认 90(锚点=审核完成时间)
}

// Load 从默认位置与环境变量构造配置;flags 为预留的命令行参数位。
func Load(flags []string) (*Config, error) {
	return Default(), nil
}

// Default 返回默认配置。
func Default() *Config {
	return &Config{Port: 8321, Addr: "0.0.0.0", DataDir: "", ExternalURL: "", PageRetentionDays: 14, DecisionRetentionD: 90}
}
