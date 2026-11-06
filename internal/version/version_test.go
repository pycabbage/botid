package version

import (
	"runtime/debug"
	"testing"
)

func buildInfo(mainVersion string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}}, true
	}
}

func TestResolve(t *testing.T) {
	noInfo := func() (*debug.BuildInfo, bool) { return nil, false }
	tests := []struct {
		name   string
		linked string
		read   func() (*debug.BuildInfo, bool)
		want   string
	}{
		{"linked value wins over build info", "from-ldflags", buildInfo("from-go-build"), "from-ldflags"},
		{"linked value without build info", "from-ldflags", noInfo, "from-ldflags"},
		{"build info when nothing is linked", "", buildInfo("from-go-build"), "from-go-build"},
		{"go run and go test report devel", "", buildInfo("(devel)"), "(devel)"},
		{"empty build version is unknown", "", buildInfo(""), "unknown"},
		{"no build info is unknown", "", noInfo, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolve(tt.linked, tt.read); got != tt.want {
				t.Errorf("resolve(%q) = %q, want %q", tt.linked, got, tt.want)
			}
		})
	}
}

func TestStringUsesTheLinkedVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "from-ldflags"

	if got := String(); got != "from-ldflags" {
		t.Errorf("String() = %q, want %q", got, "from-ldflags")
	}
}

func TestStringWithoutLinkedVersionIsNeverEmpty(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = ""

	if got := String(); got == "" {
		t.Error("String() is empty")
	}
}
