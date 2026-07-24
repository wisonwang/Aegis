// Package version exposes build-time identity for Aegis. The variables are
// stamped at link time via -ldflags, e.g.:
//
//	go build -ldflags "-X github.com/wisonwang/aegis/internal/version.Version=1.2.3 \
//	                   -X github.com/wisonwang/aegis/internal/version.Commit=$(git rev-parse --short HEAD)"
//
// They default to dev/unknown for local builds and tests.
package version

var (
	// Version is the semantic version of this build (e.g. v1.2.3).
	Version = "dev"
	// Commit is the short git SHA this build was produced from.
	Commit = "unknown"
)
