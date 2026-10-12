// Package upgrade 提供 mockit 的升级能力(规格 #11)。
//
// 本文件是"检查最新版本"入口(票 #14):从 release 基地址取最新 tag,
// 与当前版本按 semver 比较,结果缓存于数据目录(24 小时有效)。
// 它也是之后 MCP 新版提醒的数据来源。
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL 是生产环境的 release 基地址(GitHub)。
const DefaultBaseURL = "https://github.com/allanpk716/mockit/releases"

// baseURLEnv 是覆盖 release 基地址的隐藏环境变量,仅用于验收和调试。
const baseURLEnv = "MOCKIT_RELEASE_BASE"

// checkTTL 是检查结果的默认缓存有效期。
const checkTTL = 24 * time.Hour

// httpTimeout 是取最新 tag 的 HTTP 超时。
const httpTimeout = 15 * time.Second

// State 是检查结果的状态。
type State string

const (
	StateUpgradable State = "upgradable" // 最新 release 高于当前版本,可升级
	StateLatest     State = "latest"     // 已是最新
	StateDev        State = "dev"        // 开发版:当前版本解析不了或高于最新 release;不提醒
	StateFailed     State = "failed"     // 检查失败(网络 / HTTP / 响应异常)
)

// Result 是一次"检查最新版本"的结果;失败也作为结果返回,不 panic。
type Result struct {
	Current  string // 当前版本(原样)
	Latest   string // 最新 release 的 tag(如 v0.3.0;失败时为空)
	State    State
	Err      string // State == failed 时的原因
	CacheHit bool   // 是否命中有效期内的缓存(未发网络请求)
}

// String 给出人读的一行式报告(CLI `mockit upgrade --check` 直接打印)。
func (r Result) String() string {
	cur := dispVersion(r.Current)
	switch r.State {
	case StateFailed:
		return fmt.Sprintf("当前版本: %s\n检查最新版本失败: %s", cur, r.Err)
	case StateDev:
		return fmt.Sprintf("当前版本: %s\n最新版本: %s\n可升级: 否(开发版:当前版本解析不了或高于最新 release,不提醒)",
			cur, dispVersion(r.Latest))
	case StateLatest:
		return fmt.Sprintf("当前版本: %s\n最新版本: %s\n可升级: 否(已是最新)", cur, dispVersion(r.Latest))
	default: // StateUpgradable
		return fmt.Sprintf("当前版本: %s\n最新版本: %s\n可升级: 是(%s → %s)",
			cur, dispVersion(r.Latest), cur, dispVersion(r.Latest))
	}
}

// Checker 检查最新版本;依赖全部经字段注入。
type Checker struct {
	BaseURL string        // release 基地址,如 https://github.com/allanpk716/mockit/releases
	DataDir string        // 数据目录,检查缓存写在其下
	Current string        // 当前版本(如 0.2.0)
	TTL     time.Duration // 缓存有效期,零值用默认 24 小时
	Client  *http.Client  // 可选注入;默认遵守代理环境变量
}

// NewChecker 用生产默认值构造:GitHub 基地址(可被隐藏环境变量
// MOCKIT_RELEASE_BASE 覆盖),缓存放 dataDir。
func NewChecker(dataDir, current string) *Checker {
	base := DefaultBaseURL
	if v := os.Getenv(baseURLEnv); v != "" {
		base = v
	}
	return &Checker{BaseURL: base, DataDir: dataDir, Current: current}
}

// cacheFile 是缓存文件路径。
func (c *Checker) cacheFile() string { return filepath.Join(c.DataDir, "update-check.json") }

// ttl 返回生效的缓存有效期。
func (c *Checker) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return checkTTL
}

// client 返回生效的 HTTP 客户端。
func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, // 遵守 HTTP(S)_PROXY / NO_PROXY
		},
	}
}

// checkCache 是缓存文件的内容。
type checkCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

// Check 检查最新版本。force 为真时跳过缓存强制刷新(--check 走强制);
// 网络 / HTTP / 响应异常作为 StateFailed 的结果返回,不覆盖已有缓存。
func (c *Checker) Check(ctx context.Context, force bool) Result {
	res := Result{Current: c.Current}

	var latest string
	if !force {
		if cc, ok := c.readCache(); ok {
			latest = cc.Latest
			res.CacheHit = true
		}
	}
	if latest == "" {
		tag, err := c.fetchLatest(ctx)
		if err != nil {
			res.State = StateFailed
			res.Err = err.Error()
			return res
		}
		latest = tag
		c.writeCache(checkCache{Latest: latest, CheckedAt: time.Now()})
	}
	res.Latest = latest

	cur, curOK := parseSemver(c.Current)
	lat, latOK := parseSemver(latest)
	switch {
	case !curOK || !latOK || cur.Cmp(lat) > 0:
		res.State = StateDev
	case cur.Cmp(lat) == 0:
		res.State = StateLatest
	default:
		res.State = StateUpgradable
	}
	return res
}

// readCache 读有效期内且内容可解析的缓存;否则视为没有缓存。
func (c *Checker) readCache() (checkCache, bool) {
	b, err := os.ReadFile(c.cacheFile())
	if err != nil {
		return checkCache{}, false
	}
	var cc checkCache
	if err := json.Unmarshal(b, &cc); err != nil {
		return checkCache{}, false
	}
	if _, ok := parseSemver(cc.Latest); !ok || time.Since(cc.CheckedAt) > c.ttl() {
		return checkCache{}, false
	}
	return cc, true
}

// writeCache 写缓存;尽力而为,失败不影响检查结果。
func (c *Checker) writeCache(cc checkCache) {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return
	}
	b, err := json.Marshal(cc)
	if err != nil {
		return
	}
	_ = os.WriteFile(c.cacheFile(), b, 0o644)
}

// fetchLatest 从 release 基地址取最新 tag:GET <base>/latest 跟随重定向,
// 最终 URL 形如 .../tag/vX.Y.Z。
func (c *Checker) fetchLatest(ctx context.Context) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("未配置 release 基地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.Request == nil || resp.Request.URL == nil {
		return "", fmt.Errorf("响应缺少最终地址")
	}
	tag, ok := tagFromPath(resp.Request.URL.Path)
	if !ok {
		return "", fmt.Errorf("最终地址里没有版本 tag(得 %q)", resp.Request.URL.Path)
	}
	if _, ok := parseSemver(tag); !ok {
		return "", fmt.Errorf("tag %q 不是 vX.Y.Z 形式", tag)
	}
	return tag, nil
}

// tagFromPath 从形如 .../releases/tag/v0.3.0 的路径里取 tag。
func tagFromPath(p string) (string, bool) {
	i := strings.LastIndex(p, "/tag/")
	if i < 0 {
		return "", false
	}
	tag := p[i+len("/tag/"):]
	if tag == "" || strings.Contains(tag, "/") {
		return "", false
	}
	return tag, true
}

// semver 是解析后的语义化版本(仅 vX.Y.Z;发布契约不含预发布段)。
type semver struct {
	major, minor, patch int
}

// parseSemver 解析 vX.Y.Z 或 X.Y.Z;解析不了第二个返回值为假。
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, part := range parts {
		if part == "" || strings.TrimLeft(part, "0123456789") != "" {
			return semver{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return semver{}, false
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	return v, true
}

// Cmp 按 semver 比较:-1 小于、0 相等、1 大于。
func (v semver) Cmp(o semver) int {
	for _, p := range [3][2]int{{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch}} {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	return 0
}

// dispVersion 去掉展示用的 v 前缀。
func dispVersion(s string) string { return strings.TrimPrefix(s, "v") }
