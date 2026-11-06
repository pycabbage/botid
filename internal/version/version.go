package version

import "runtime/debug"

// Version is set at link time with -X github.com/pycabbage/botid/internal/version.Version=<value>.
// It stays empty in the source tree.
var Version string

// String returns the linked Version. Without one it returns the version that go build derives from the
// version control tag or commit, and "unknown" when the binary carries no build information.
func String() string {
	return resolve(Version, debug.ReadBuildInfo)
}

func resolve(linked string, read func() (*debug.BuildInfo, bool)) string {
	if linked != "" {
		return linked
	}
	if info, ok := read(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "unknown"
}
