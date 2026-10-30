package facts

import (
	"strings"

	"github.com/amberpixels/f10/cli/internal/standup"
)

// A VerifyPick is a declared command the diff calls for, with the pattern
// and the changed file that matched; both are "" for a command with no paths.
type VerifyPick struct {
	VerifyDecl

	Pattern string
	File    string
}

// ScopeVerify splits the declared commands into those the changed files
// call for and those they do not, each in declared order.
func ScopeVerify(cmds []VerifyDecl, changed []string) ([]VerifyPick, []VerifyDecl) {
	var (
		run  []VerifyPick
		left []VerifyDecl
	)

	for _, c := range cmds {
		if len(c.Paths) == 0 {
			run = append(run, VerifyPick{VerifyDecl: c})

			continue
		}

		if pattern, file, ok := matchedBy(c.Paths, changed); ok {
			run = append(run, VerifyPick{VerifyDecl: c, Pattern: pattern, File: file})
		} else {
			left = append(left, c)
		}
	}

	return run, left
}

func matchedBy(patterns, files []string) (string, string, bool) {
	for _, f := range files {
		for _, p := range patterns {
			if standup.Glob(p, f) {
				return p, f, true
			}
		}
	}

	return "", "", false
}

// verifyShown is the list as f10 config prints it, one command with its
// paths per entry.
func verifyShown(cmds []VerifyDecl) string {
	parts := make([]string, 0, len(cmds))

	for _, c := range cmds {
		paths := "every diff"
		if len(c.Paths) > 0 {
			paths = strings.Join(c.Paths, ", ")
		}

		parts = append(parts, c.Run+" ("+paths+")")
	}

	return strings.Join(parts, "; ")
}
