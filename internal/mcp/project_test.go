package mcp

import (
	"runtime"
	"testing"
)

func TestNormalizeProjectKey(t *testing.T) {
	got := normalizeProjectKey(`C:\Work\Mockit\`)
	want := "C:/Work/Mockit"
	if runtime.GOOS == "windows" {
		want = "c:/work/mockit"
	}
	if got != want {
		t.Fatalf("normalizeProjectKey = %q, want %q", got, want)
	}
}

func TestProjectInjectedAndCached(t *testing.T) {
	s := &server{detectProject: func() (string, string) {
		return "proj/current", "当前"
	}}
	got := s.project()
	if got.Key != "proj/current" || got.Name != "当前" {
		t.Fatalf("project = %+v", got)
	}
	s.detectProject = func() (string, string) { return "changed", "changed" }
	got = s.project()
	if got.Key != "proj/current" {
		t.Fatalf("来源项目应每进程只计算一次,得 %q", got.Key)
	}
}

func TestProjectNilDetector(t *testing.T) {
	s := &server{}
	if got := s.project(); got != (projectInfo{}) {
		t.Fatalf("nil detectProject 应返回空项目,得 %+v", got)
	}
}
