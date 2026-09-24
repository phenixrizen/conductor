// Package version exposes build metadata injected through -ldflags.
package version

// Version and Commit are set at build time:
//
//	-ldflags "-X github.com/phenixrizen/conductor/internal/version.Version=v1.0.0"
var (
	Version = "dev"
	Commit  = "unknown"
)

// String returns a human readable version label.
func String() string {
	return Version + " (" + Commit + ")"
}
