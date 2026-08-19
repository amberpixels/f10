// Package gitx is the binary's one door to git: the same three read-only
// shell-outs bin/resolve.sh makes. No go-git - the bash script is the
// semantics oracle, and shelling out identically keeps the two comparable.
package gitx

import (
	"context"
	"os/exec"
	"strings"
)

// Out runs git with args in dir and returns trimmed stdout, or "" on any
// error - absence is a valid result everywhere this is used.
func Out(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
