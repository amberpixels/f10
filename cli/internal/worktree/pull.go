package worktree

import (
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/taskid"
)

// PullPathRE matches GitHub's `/pull/12` and GitLab's `/-/merge_requests/12`.
var PullPathRE = regexp.MustCompile(`/(?:pull|merge_requests)/(\d+)(?:[/?#]|$)`)

// PullToken's probe is true for a bare `#12`, which may name a task instead:
// ask the host before taking it (GitHub's issues and PRs share one counter).
func PullToken(token string) (string, bool) {
	switch {
	case strings.Contains(token, "://"):
		if m := PullPathRE.FindStringSubmatch(token); m != nil {
			return m[1], false
		}
	case len(token) > 1 && (token[0] == '!' || token[0] == '#') && taskid.IsNumber(token[1:]):
		return token[1:], token[0] == '#'
	}

	return "", false
}
