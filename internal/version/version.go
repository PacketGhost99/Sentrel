// Package version holds build metadata injected with -ldflags.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version line.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
