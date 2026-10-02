// Package version holds the build information of the binaries. The release build sets the
// variables with -ldflags (see the Makefile).
package version

import "fmt"

// Set by -ldflags "-X github.com/Andste82/chaos-gateway/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns "<name> <version> (<commit>, <date>)".
func String(name string) string {
	return fmt.Sprintf("%s %s (%s, %s)", name, Version, Commit, Date)
}
