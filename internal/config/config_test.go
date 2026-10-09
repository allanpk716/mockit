package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setTestHome 把家目录指向临时目录,并把全部 MOCKIT_* 环境变量清为空
// (空值在本实现里等同未设置),保证用例互不干扰、不受开发机环境影响。
func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)        // Unix
	t.Setenv("USERPROFILE", home) // Windows
	for _, k := range []string{
		"MOCKIT_PORT", "MOCKIT_ADDR", "MOCKIT_DATA_DIR",
		"MOCKIT_PAGE_RETENTION_D", "MOCKIT_DECISION_RETENTION_D",
	} {
		t.Setenv(k, "")
	}
	return home
}

func writeTestConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".mockit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建 .mockit 目录失败: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写 config.json 失败: %v", err)
	}
}

func TestDefault(t *testing.T) {
	home := setTestHome(t)
	c := Default()
	if c.Port != 8321 {
		t.Errorf("Port = %d, want 8321", c.Port)
	}
	if c.Addr != "0.0.0.0" {
		t.Errorf("Addr = %q, want 0.0.0.0", c.Addr)
	}
	if c.PageRetentionDays != 14 {
		t.Errorf("PageRetentionDays = %d, want 14", c.PageRetentionDays)
	}
	if c.DecisionRetentionD != 90 {
		t.Errorf("DecisionRetentionD = %d, want 90", c.DecisionRetentionD)
	}
	if c.ExternalURL != "" {
		t.Errorf("ExternalURL = %q, want 空", c.ExternalURL)
	}
	if !filepath.IsAbs(c.DataDir) {
		t.Errorf("DataDir 应解析为绝对路径, got %q", c.DataDir)
	}
	if want := filepath.Join(home, ".mockit"); c.DataDir != want {
		t.Errorf("DataDir = %q, want %q", c.DataDir, want)
	}
}

func TestLoadDefaults(t *testing.T) {
	home := setTestHome(t)
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load(nil) 报错: %v", err)
	}
	if c.Port != 8321 || c.Addr != "0.0.0.0" || c.PageRetentionDays != 14 || c.DecisionRetentionD != 90 {
		t.Errorf("默认值不对: %+v", c)
	}
	if want := filepath.Join(home, ".mockit"); c.DataDir != want {
		t.Errorf("DataDir = %q, want %q", c.DataDir, want)
	}
	// flags 是预留位:传了不该影响结果。
	c2, err := Load([]string{"--port", "9999"})
	if err != nil {
		t.Fatalf("Load(flags) 报错: %v", err)
	}
	if c2.Port != c.Port || c2.DataDir != c.DataDir {
		t.Errorf("flags 应被忽略: %+v vs %+v", c, c2)
	}
}

func TestLoadFromFile(t *testing.T) {
	home := setTestHome(t)
	writeTestConfig(t, home, `{
		"port": 9000,
		"addr": "127.0.0.1",
		"data_dir": "~/custom",
		"page_retention_days": 7,
		"decision_retention_days": 30,
		"external_url": "https://mock.example.com"
	}`)
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if c.Port != 9000 {
		t.Errorf("Port = %d, want 9000", c.Port)
	}
	if c.Addr != "127.0.0.1" {
		t.Errorf("Addr = %q, want 127.0.0.1", c.Addr)
	}
	if want := filepath.Join(home, "custom"); c.DataDir != want {
		t.Errorf("DataDir = %q, want %q(家目录应展开)", c.DataDir, want)
	}
	if c.PageRetentionDays != 7 {
		t.Errorf("PageRetentionDays = %d, want 7", c.PageRetentionDays)
	}
	if c.DecisionRetentionD != 30 {
		t.Errorf("DecisionRetentionD = %d, want 30", c.DecisionRetentionD)
	}
	if c.ExternalURL != "https://mock.example.com" {
		t.Errorf("ExternalURL = %q, want 原样透传", c.ExternalURL)
	}
}

func TestLoadFromFilePartialOnlyOverridesPresentKeys(t *testing.T) {
	home := setTestHome(t)
	writeTestConfig(t, home, `{"decision_retention_days": 45}`)
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if c.DecisionRetentionD != 45 {
		t.Errorf("DecisionRetentionD = %d, want 45", c.DecisionRetentionD)
	}
	if c.Port != 8321 {
		t.Errorf("未写 port 时应保持默认 8321, got %d", c.Port)
	}
	if c.PageRetentionDays != 14 {
		t.Errorf("未写 page_retention_days 时应保持默认 14, got %d", c.PageRetentionDays)
	}
	if want := filepath.Join(home, ".mockit"); c.DataDir != want {
		t.Errorf("未写 data_dir 时应保持默认, got %q, want %q", c.DataDir, want)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	home := setTestHome(t)
	writeTestConfig(t, home, `{
		"port": 9000,
		"addr": "127.0.0.1",
		"data_dir": "~/fromfile",
		"page_retention_days": 7,
		"decision_retention_days": 30
	}`)
	t.Setenv("MOCKIT_PORT", "9100")
	t.Setenv("MOCKIT_ADDR", "10.1.2.3")
	t.Setenv("MOCKIT_DATA_DIR", "~/fromenv")
	t.Setenv("MOCKIT_PAGE_RETENTION_D", "3")
	t.Setenv("MOCKIT_DECISION_RETENTION_D", "60")
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if c.Port != 9100 {
		t.Errorf("env 应覆盖文件: Port = %d, want 9100", c.Port)
	}
	if c.Addr != "10.1.2.3" {
		t.Errorf("env 应覆盖文件: Addr = %q, want 10.1.2.3", c.Addr)
	}
	if want := filepath.Join(home, "fromenv"); c.DataDir != want {
		t.Errorf("env 应覆盖文件: DataDir = %q, want %q", c.DataDir, want)
	}
	if c.PageRetentionDays != 3 {
		t.Errorf("env 应覆盖文件: PageRetentionDays = %d, want 3", c.PageRetentionDays)
	}
	if c.DecisionRetentionD != 60 {
		t.Errorf("env 应覆盖文件: DecisionRetentionD = %d, want 60", c.DecisionRetentionD)
	}
}

func TestLoadEnvOnly(t *testing.T) {
	home := setTestHome(t)
	t.Setenv("MOCKIT_PORT", "8080")
	t.Setenv("MOCKIT_DATA_DIR", "~/envonly")
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d, want 8080", c.Port)
	}
	if c.Addr != "0.0.0.0" {
		t.Errorf("未设置的 Addr 应保持默认, got %q", c.Addr)
	}
	if want := filepath.Join(home, "envonly"); c.DataDir != want {
		t.Errorf("DataDir = %q, want %q", c.DataDir, want)
	}
}

func TestLoadDataDirRelativeMadeAbsolute(t *testing.T) {
	setTestHome(t)
	t.Setenv("MOCKIT_DATA_DIR", filepath.Join("relative", "data"))
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	want, err := filepath.Abs(filepath.Join("relative", "data"))
	if err != nil {
		t.Fatalf("filepath.Abs 报错: %v", err)
	}
	if c.DataDir != want {
		t.Errorf("DataDir = %q, want 绝对路径 %q", c.DataDir, want)
	}
}

func TestLoadBadJSONFileErrors(t *testing.T) {
	home := setTestHome(t)
	writeTestConfig(t, home, `{oops`)
	_, err := Load(nil)
	if err == nil {
		t.Fatal("坏 JSON 的 config.json 应报错")
	}
	if !strings.Contains(err.Error(), "config.json") {
		t.Errorf("错误信息应包含 config.json 路径, got: %v", err)
	}
}

func TestLoadBadEnvIntErrors(t *testing.T) {
	setTestHome(t)
	t.Setenv("MOCKIT_PORT", "abc")
	_, err := Load(nil)
	if err == nil || !strings.Contains(err.Error(), "MOCKIT_PORT") {
		t.Errorf("MOCKIT_PORT=abc 应报包含变量名的错误, got: %v", err)
	}
	setTestHome(t)
	t.Setenv("MOCKIT_PAGE_RETENTION_D", "1.5")
	_, err = Load(nil)
	if err == nil || !strings.Contains(err.Error(), "MOCKIT_PAGE_RETENTION_D") {
		t.Errorf("MOCKIT_PAGE_RETENTION_D=1.5 应报包含变量名的错误, got: %v", err)
	}
}

func TestLoadExternalURLPassthroughNoValidation(t *testing.T) {
	home := setTestHome(t)
	weird := "http://bad host with spaces:9999/x"
	writeTestConfig(t, home, `{"external_url": "http://bad host with spaces:9999/x"}`)
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("external_url 不应在 Load 时校验(校验属 serve 启动前置,票 09), 却报错: %v", err)
	}
	if c.ExternalURL != weird {
		t.Errorf("ExternalURL = %q, want 原样透传 %q", c.ExternalURL, weird)
	}
}

// external_url 语义校验(D16/票 09):只接受裸主机名——域名或 IP 字面量
// (含 IPv6);带 scheme 或端口是配置错误,不接受也不静默剥离。
func TestValidateExternalURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool // true=通过
	}{
		{"空值=未配置合法", "", true},
		{"裸域名", "example.com", true},
		{"裸单标签主机名", "my-nuc", true},
		{"大写域名", "My-Host.Example.COM", true},
		{"裸IPv4", "192.168.1.10", true},
		{"NetBird段IPv4", "100.100.1.5", true},
		{"裸IPv6", "fd00::1", true},
		{"完整IPv6", "2001:db8:0::9", true},
		{"带scheme http", "http://host", false},
		{"带scheme https", "https://host", false},
		{"scheme加端口", "http://host:8321", false},
		{"带端口", "host:8321", false},
		{"IPv6带端口", "[fd00::1]:8321", false},
		{"方括号IPv6", "[fd00::1]", false},
		{"含空格", "bad host", false},
		{"下划线", "under_score", false},
		{"标签连字符开头", "-lead.example.com", false},
		{"标签连字符结尾", "trail-.example.com", false},
		{"连续点", "host..com", false},
		{"结尾点", "host.com.", false},
		{"斜杠路径", "example.com/mockit", false},
		{"末组形似端口的合法IPv6字面量(0x8321是十六进制组,非端口)", "fd00::1:8321", true},
	}
	for _, c := range cases {
		err := ValidateExternalURL(c.raw)
		if c.want && err != nil {
			t.Errorf("%s: ValidateExternalURL(%q) 应通过, 得 %v", c.name, c.raw, err)
		}
		if !c.want && err == nil {
			t.Errorf("%s: ValidateExternalURL(%q) 应报配置错误", c.name, c.raw)
		}
	}
}

// Config.Validate 是 serve 启动的前置校验入口,透传 external_url 结论。
func TestConfigValidateCoversExternalURL(t *testing.T) {
	c := Default()
	c.ExternalURL = "host:8321"
	if err := c.Validate(); err == nil {
		t.Fatal("Config.Validate 应拒绝带端口的 external_url")
	}
	c.ExternalURL = "review.example.com"
	if err := c.Validate(); err != nil {
		t.Fatalf("合法裸域名应通过: %v", err)
	}
}
