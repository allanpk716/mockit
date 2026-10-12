package upgrade

// 检查入口测试(票 #14):httptest 假 release 服务器覆盖
// 有新版 / 已是最新 / 开发版 / 缓存命中与过期 / 强制刷新 / 网络与响应异常。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRelease 起一个假 release 服务器:/latest 302 到 /tag/<tag>,
// 并统计 /latest 命中次数。
func fakeRelease(t *testing.T, tag string) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			atomic.AddInt64(&hits, 1)
			http.Redirect(w, r, "/tag/"+tag, http.StatusFound)
		case r.URL.Path == "/tag/"+tag:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestCheckUpgradable(t *testing.T) {
	srv, hits := fakeRelease(t, "v0.3.0")
	dir := t.TempDir()
	c := &Checker{BaseURL: srv.URL, DataDir: dir, Current: "0.2.0"}

	res := c.Check(context.Background(), false)

	if res.State != StateUpgradable {
		t.Fatalf("状态 = %q,想 %q(结果 %+v)", res.State, StateUpgradable, res)
	}
	if res.Latest != "v0.3.0" {
		t.Fatalf("最新版本 = %q,想 %q", res.Latest, "v0.3.0")
	}
	if res.CacheHit {
		t.Fatal("首次检查不应命中缓存")
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("/latest 请求数 = %d,想 1", got)
	}
	// 只读:数据目录里只允许出现缓存文件
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "update-check.json" {
		t.Fatalf("数据目录内容 = %v,想只有 update-check.json", ents)
	}
	// 输出含当前版本、最新版本、可升级
	for _, want := range []string{"0.2.0", "0.3.0", "是"} {
		if !strings.Contains(res.String(), want) {
			t.Fatalf("输出 %q 缺 %q", res.String(), want)
		}
	}
}

func TestCheckAlreadyLatest(t *testing.T) {
	srv, _ := fakeRelease(t, "v0.3.0")
	c := &Checker{BaseURL: srv.URL, DataDir: t.TempDir(), Current: "v0.3.0"}

	res := c.Check(context.Background(), false)

	if res.State != StateLatest {
		t.Fatalf("状态 = %q,想 %q(结果 %+v)", res.State, StateLatest, res)
	}
	for _, want := range []string{"0.3.0", "已是最新"} {
		if !strings.Contains(res.String(), want) {
			t.Fatalf("输出 %q 缺 %q", res.String(), want)
		}
	}
}

func TestCheckDev(t *testing.T) {
	cases := []struct {
		name    string
		current string
	}{
		{"当前版本解析不了", "dev-build"},
		{"当前版本高于最新 release", "0.9.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeRelease(t, "v0.3.0")
			c := &Checker{BaseURL: srv.URL, DataDir: t.TempDir(), Current: tc.current}

			res := c.Check(context.Background(), false)

			if res.State != StateDev {
				t.Fatalf("状态 = %q,想 %q(结果 %+v)", res.State, StateDev, res)
			}
			if !strings.Contains(res.String(), "不提醒") {
				t.Fatalf("开发版结果须注明不提醒,得 %q", res.String())
			}
		})
	}
}

func TestCheckCacheHitExpiryAndForce(t *testing.T) {
	srv, hits := fakeRelease(t, "v0.3.0")
	c := &Checker{BaseURL: srv.URL, DataDir: t.TempDir(), Current: "0.2.0", TTL: 80 * time.Millisecond}

	// 首次:发请求,写缓存
	c.Check(context.Background(), false)
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("首次后 /latest 请求数 = %d,想 1", got)
	}

	// 有效期内:命中缓存,不再发请求
	res := c.Check(context.Background(), false)
	if !res.CacheHit || res.State != StateUpgradable {
		t.Fatalf("有效期内应命中缓存且状态不变,得 %+v", res)
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("缓存命中后 /latest 请求数 = %d,想仍 1", got)
	}

	// 强制刷新:无视未过期缓存,重新请求
	res = c.Check(context.Background(), true)
	if res.CacheHit {
		t.Fatal("强制刷新不应命中缓存")
	}
	if got := atomic.LoadInt64(hits); got != 2 {
		t.Fatalf("强制刷新后 /latest 请求数 = %d,想 2", got)
	}

	// 过期后:重新请求
	time.Sleep(100 * time.Millisecond)
	res = c.Check(context.Background(), false)
	if res.CacheHit {
		t.Fatal("缓存过期后不应命中缓存")
	}
	if got := atomic.LoadInt64(hits); got != 3 {
		t.Fatalf("过期后 /latest 请求数 = %d,想 3", got)
	}
}

// badRelease 起一个可控的坏响应服务器:mode 决定 /latest 的行为。
func badRelease(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "http500":
			w.WriteHeader(http.StatusInternalServerError)
		case "noTag": // 不重定向,直回 200:最终地址里没有 tag
			w.WriteHeader(http.StatusOK)
		case "badTag":
			http.Redirect(w, r, "/tag/not-a-version", http.StatusFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckFailures(t *testing.T) {
	for _, mode := range []string{"http500", "noTag", "badTag"} {
		t.Run(mode, func(t *testing.T) {
			srv := badRelease(t, mode)
			c := &Checker{BaseURL: srv.URL, DataDir: t.TempDir(), Current: "0.2.0"}

			res := c.Check(context.Background(), false) // 不 panic,失败也是结果

			if res.State != StateFailed {
				t.Fatalf("状态 = %q,想 %q(结果 %+v)", res.State, StateFailed, res)
			}
			if res.Err == "" {
				t.Fatal("失败结果须带原因")
			}
			if _, err := os.Stat(c.cacheFile()); !os.IsNotExist(err) {
				t.Fatal("失败不应写缓存")
			}
		})
	}
}

func TestCheckNetworkFailureKeepsCache(t *testing.T) {
	srv, _ := fakeRelease(t, "v0.3.0")
	dir := t.TempDir()
	c := &Checker{BaseURL: srv.URL, DataDir: dir, Current: "0.2.0"}
	if res := c.Check(context.Background(), false); res.State != StateUpgradable {
		t.Fatalf("预置成功检查失败:%+v", res)
	}

	srv.Close() // 网络断
	res := c.Check(context.Background(), true)
	if res.State != StateFailed {
		t.Fatalf("断网强制检查应失败,得 %+v", res)
	}

	// 坏结果不覆盖缓存:有效期内仍命中旧值
	res = c.Check(context.Background(), false)
	if !res.CacheHit || res.Latest != "v0.3.0" {
		t.Fatalf("坏结果不应覆盖缓存,得 %+v", res)
	}
}

func TestNewCheckerBaseURLOverride(t *testing.T) {
	srv, hits := fakeRelease(t, "v0.3.1")
	t.Setenv("MOCKIT_RELEASE_BASE", srv.URL)

	c := NewChecker(t.TempDir(), "0.2.0")
	res := c.Check(context.Background(), false)

	if res.State != StateUpgradable || res.Latest != "v0.3.1" {
		t.Fatalf("应使用环境变量覆盖的基地址,得 %+v", res)
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("/latest 请求数 = %d,想 1", got)
	}
}
