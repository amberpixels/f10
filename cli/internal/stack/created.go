package stack

import (
	"context"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// createdAt is the commit branch was created at, from its oldest reflog
// entry, or "" once the reflog no longer reaches back to the creation.
func createdAt(ctx context.Context, dir, branch string) string {
	lines := strings.Split(gitx.Out(ctx, dir, "reflog", "show", "--format=%H %gs", "refs/heads/"+branch), "\n")

	sha, subject, _ := strings.Cut(lines[len(lines)-1], " ")
	if !strings.HasPrefix(subject, "branch: Created from") {
		return ""
	}

	return sha
}
