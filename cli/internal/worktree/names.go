package worktree

import (
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
)

func SuffixOf(s string) string {
	if s == "" {
		return ""
	}

	return "-" + s
}

func PrefixOf(r ref.Ref) string {
	return strings.TrimSuffix(r.ID, r.Number)
}
