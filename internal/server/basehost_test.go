package server

// 对外基址主机(票 09/D16)测试:
//   - pickNetBirdHost:100.64.0.0/10 唯一命中/零命中/多命中歧义/只认 IPv4;
//   - Serve 级:external_url 优先于探测、探测唯一命中写入 lock、歧义/零命中
//     留空但 server 照常启动、非法 external_url 启动即退出码 1。
// 单测不依赖真实网卡:枚举函数 interfaceAddrs 注入(包级变量,串行测试安全)。

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mockit/internal/config"
	"mockit/internal/lifecycle"
)

func TestPickNetBirdHost(t *testing.T) {
	cases := []struct {
		name  string
		addrs []string
		want  string
	}{
		{"唯一命中忽略段外地址", []string{"192.168.1.10", "100.100.1.5"}, "100.100.1.5"},
		{"零命中", []string{"192.168.1.10", "10.0.0.2"}, ""},
		{"多命中即歧义", []string{"100.100.1.5", "100.100.2.9"}, ""},
		{"空清单", nil, ""},
		{"只认IPv4不认IPv6", []string{"fd00::1", "100.100.1.5"}, "100.100.1.5"},
		{"段下界外100.63", []string{"100.63.255.255"}, ""},
		{"段下界内100.64", []string{"100.64.0.1"}, "100.64.0.1"},
		{"段上界内100.127", []string{"100.127.255.255"}, "100.127.255.255"},
		{"段上界外100.128", []string{"100.128.0.1"}, ""},
	}
	for _, c := range cases {
		var ips []net.IP
		for _, s := range c.addrs {
			ips = append(ips, net.ParseIP(s))
		}
		if got := pickNetBirdHost(ips); got != c.want {
			t.Errorf("%s: pickNetBirdHost(%v) = %q, want %q", c.name, c.addrs, got, c.want)
		}
	}
}

// 枚举失败(如系统调用报错)视为零命中,不惊动启动。
func TestDetectBaseHostEnumFailure(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.IP, error) { return nil, errors.New("枚举失败") }
	t.Cleanup(func() { interfaceAddrs = old })
	if got := detectBaseHost(); got != "" {
		t.Fatalf("枚举失败应返回空, 得 %q", got)
	}
}

// injectAddrs 注入网卡地址清单并注册还原。
func injectAddrs(t *testing.T, addrs []string) {
	t.Helper()
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.IP, error) {
		out := make([]net.IP, 0, len(addrs))
		for _, s := range addrs {
			out = append(out, net.ParseIP(s))
		}
		return out, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })
}

// serveUntilLock 起一个真实 serve(注入网卡探测),等 lock 写好即返回;
// 测试收尾自动 shutdown 并等 Serve 返回。
func serveUntilLock(t *testing.T, cfg *config.Config, addrs []string) *lifecycle.Lock {
	t.Helper()
	injectAddrs(t, addrs)
	codeCh := make(chan int, 1)
	go func() { codeCh <- Serve(cfg) }()
	t.Cleanup(func() {
		if lk, err := lifecycle.ReadLock(cfg.DataDir); err == nil {
			shutdownReq(t, lk.Port, lk.Token)
		}
		select {
		case <-codeCh:
		case <-time.After(10 * time.Second):
			t.Error("Serve 未在 shutdown 后退出")
		}
	})
	return waitLock(t, cfg.DataDir, 10*time.Second)
}

func baseHostCfg(t *testing.T, externalURL string) *config.Config {
	t.Helper()
	return &config.Config{
		Port: freeDriftBase(t), Addr: "127.0.0.1", DataDir: t.TempDir(),
		ExternalURL:       externalURL,
		PageRetentionDays: 14, DecisionRetentionD: 90,
	}
}

// 第一层优先:external_url 已配置时,探测结果即使唯一命中也不采用。
func TestServeExternalURLWinsOverDetection(t *testing.T) {
	cfg := baseHostCfg(t, "review.example.com")
	lk := serveUntilLock(t, cfg, []string{"192.168.9.9", "100.100.1.5"})
	if lk.BaseHost != "review.example.com" {
		t.Fatalf("lock.base_host = %q, want external_url 原样", lk.BaseHost)
	}
}

// 第二层:未配置时探测 NetBird 段唯一命中 IPv4 写入 lock。
func TestServeDetectsUniqueNetBirdHost(t *testing.T) {
	cfg := baseHostCfg(t, "")
	lk := serveUntilLock(t, cfg, []string{"192.168.9.9", "fe80::1", "100.100.1.5"})
	if lk.BaseHost != "100.100.1.5" {
		t.Fatalf("lock.base_host = %q, want 探测唯一命中 100.100.1.5", lk.BaseHost)
	}
}

// 第三层:歧义留空,但 server 照常启动、页面照常可审(仅 submit 侧报错)。
func TestServeAmbiguousDetectionLeavesBaseHostEmptyButServes(t *testing.T) {
	cfg := baseHostCfg(t, "")
	lk := serveUntilLock(t, cfg, []string{"100.100.1.5", "100.100.2.9"})
	if lk.BaseHost != "" {
		t.Fatalf("lock.base_host = %q, 多命中应留空", lk.BaseHost)
	}
	resp, err := localClient().Get(fmt.Sprintf("http://127.0.0.1:%d/ping", lk.Port))
	if err != nil {
		t.Fatalf("歧义时 server 应照常启动: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping 状态 = %d, want 200", resp.StatusCode)
	}
}

// 零命中同样留空。
func TestServeZeroHitLeavesBaseHostEmpty(t *testing.T) {
	cfg := baseHostCfg(t, "")
	lk := serveUntilLock(t, cfg, []string{"192.168.9.9"})
	if lk.BaseHost != "" {
		t.Fatalf("lock.base_host = %q, 零命中应留空", lk.BaseHost)
	}
}

// external_url 允许裸 IPv6,原样写入 lock(方括号序列化属 URL 拼接层,F8)。
func TestServeIPv6ExternalURLAsBaseHost(t *testing.T) {
	cfg := baseHostCfg(t, "fd00::1")
	lk := serveUntilLock(t, cfg, nil)
	if lk.BaseHost != "fd00::1" {
		t.Fatalf("lock.base_host = %q, want fd00::1", lk.BaseHost)
	}
}

// 非法 external_url(scheme/端口/方括号)启动即报配置错误,退出码 1,
// 不触碰 store/端口/实例锁。
func TestServeRejectsInvalidExternalURL(t *testing.T) {
	for _, bad := range []string{"host:8321", "http://host", "https://host", "[fd00::1]", "bad host"} {
		dataDir := t.TempDir()
		cfg := &config.Config{
			Port: 1, Addr: "127.0.0.1", DataDir: dataDir, ExternalURL: bad,
			PageRetentionDays: 14, DecisionRetentionD: 90,
		}
		if code := Serve(cfg); code != 1 {
			t.Fatalf("external_url=%q 应以退出码 1 拒绝启动, 得 %d", bad, code)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "server.lock")); !os.IsNotExist(err) {
			t.Fatalf("external_url=%q 拒绝启动时不得留下 server.lock: %v", bad, err)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "logs")); !os.IsNotExist(err) {
			t.Fatalf("external_url=%q 拒绝启动时不得先建日志目录: %v", bad, err)
		}
	}
}
