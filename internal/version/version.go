// Package version is the program's version, stamped at build time by cmd/release:
//
//	-ldflags "-X github.com/biggaeroza/bga-mix/internal/version.Version=1.0.0 -X ...Commit=abc123 -X ...Date=2026-11-18"
package version

import "fmt"

// Set by the linker; a plain `go build` gives "dev".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String is the version with its commit and date when stamped.
func String() string {
	if Commit == "" {
		return Version
	}
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, Date)
}

// Program names the program and its version.
func Program() string { return "bgamix " + String() }
