package gitx

import "strings"

// ShortSHA is a sha as people read it: git's default 7 characters.
func ShortSHA(sha string) string {
	return sha[:min(len(sha), 7)]
}

// SameCommit matches an abbreviated sha against a full one, either way and
// in any case, so a head copied short is not stale against its own commit.
func SameCommit(a, b string) bool {
	if len(a) < 7 || len(b) < 7 {
		return false
	}

	a, b = strings.ToLower(a), strings.ToLower(b)

	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}
