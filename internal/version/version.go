// Package version carries the build's identity.
//
// It lives in its own package so both binaries are stamped by one linker flag rather than
// two, and so a hand-built binary is honestly labelled "dev" instead of claiming whatever
// number happened to be hardcoded when the file was last edited.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version is the release tag, injected at build time with:
//
//	-ldflags "-X github.com/dpalfery/terragraph/internal/version.Version=v0.0.1"
var Version = "dev"

// Commit is the source revision, injected the same way. Left empty for local builds, where
// the Go toolchain's own VCS stamp is a better answer.
var Commit = ""

// String renders the full build identity for --version and the MCP handshake.
func String() string {
	commit := Commit
	if commit == "" {
		commit = vcsRevision()
	}
	if commit == "" {
		return fmt.Sprintf("%s %s/%s", Version, runtime.GOOS, runtime.GOARCH)
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("%s (%s) %s/%s", Version, commit, runtime.GOOS, runtime.GOARCH)
}

// vcsRevision reads the revision the Go toolchain stamps into a binary built inside a git
// checkout. It means a `go build` with no linker flags still identifies itself, which is
// what a bug report from a local build needs.
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}
