// Package buildinfo is the binary's version: the link-time stamp, else what
// Go recorded in the build.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Stamp is the release stamp main copies from its -X ldflags variable, which
// stays in main so the shim and the release workflow keep their flag.
var Stamp string

// Version is read from the build so no constant drifts: the stamp, else the
// module version Go read from the `cli/vX.Y.Z` tag, else the commit with its
// date and a dirty mark. A checkout build gets the commit even at the tag,
// since Go derives a version from tags only for a module at the repository root.
func Version() string {
	bi, ok := debug.ReadBuildInfo()

	return versionOf(Stamp, bi, ok)
}

func versionOf(stamp string, bi *debug.BuildInfo, ok bool) string {
	if stamp != "" {
		return stamp
	}

	if !ok || bi == nil {
		return "unknown"
	}

	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}

	var revision, at, dirty string

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value[:min(12, len(s.Value))]
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = " dirty"
			}
		}
	}

	if revision == "" {
		return "devel"
	}

	return "devel " + revision + " " + at + dirty
}
