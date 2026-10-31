// Package version exposes build metadata.
package version

import "fmt"

// Overridden at build time with -ldflags "-X .../internal/version.Version=..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String renders a human-readable version line.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
