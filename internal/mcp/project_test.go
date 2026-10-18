package mcp

import (
	"runtime"
	"testing"
)

func TestNormalizeProjectKey(t *testing.T) {
	var tests []struct {
		name string
		path string
		want string
	}
	if runtime.GOOS == "windows" {
		tests = []struct {
			name string
			path string
			want string
		}{
			{name: "windows path", path: `C:\Work\Mockit\`, want: "c:/work/mockit"},
			{name: "mixed separators", path: `C:/Work/./Mockit/`, want: "c:/work/mockit"},
		}
	} else {
		tests = []struct{ name, path, want string }{
			{name: "unix path", path: "/home/u/Work/./Mockit/", want: "/home/u/Work/Mockit"},
		}
	}

	for _, tt := range tests {
		if got := normalizeProjectKey(tt.path); got != tt.want {
			t.Fatalf("%s: normalizeProjectKey(%q) = %q, want %q", tt.name, tt.path, got, tt.want)
		}
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
