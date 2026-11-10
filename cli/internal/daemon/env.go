package daemon

import (
	"slices"
	"strings"
)

// driveEnvPrefixes and driveEnvKeys are the environment a drive passes to
// its commands: what git, gh, glab, Herdr, claude and f10 itself read.
var (
	driveEnvPrefixes = []string{"LC_", "XDG_", "GH_", "GLAB_", "HERDR_", "F10_", "CLAUDE_"}
	driveEnvKeys     = []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "TERM", "SSH_AUTH_SOCK",
		"GITHUB_TOKEN", "GITLAB_TOKEN",
	}
)

func DriveEnv(environ, extra []string) []string {
	var out []string

	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		if slices.Contains(driveEnvKeys, key) || slices.Contains(extra, key) ||
			slices.ContainsFunc(driveEnvPrefixes, func(p string) bool { return strings.HasPrefix(key, p) }) {
			out = append(out, kv)
		}
	}

	return out
}
