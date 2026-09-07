// Package projects resolves a bare project name to a checkout directory,
// for `-C r3` run from somewhere else entirely.
//
// Off unless asked for. GH-20 fenced the binary as answering for the repo it
// runs in and never walking the filesystem looking for others, and a
// basename search over projects is exactly that walk. The fence holds
// because the search is opt-in: without F10_ROOTS nothing is globbed and
// nothing is scanned, and a bare name says so rather than guessing. Setting
// the variable - one line in a shell profile, no file for f10 to own - is
// what turns multi-project lookup on.
//
// A path never comes here. `-C ../r3` needs no lookup at all, so it works
// whether or not the variable is set.
package projects

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RootsEnv holds a colon-separated list of globs, PATH-style, each
// expanding to candidate checkout directories: `~/code/github.com/*/*`.
const RootsEnv = "F10_ROOTS"

// ErrDisabled means a name was given while name resolution is off.
var ErrDisabled = errors.New("resolving a project by name needs " + RootsEnv +
	" set to a colon-separated list of globs (for example ~/code/github.com/*/*); a path such as -C ../r3 always works")

// Roots is the configured glob list, with a leading ~ expanded. Empty means
// name resolution is off.
func Roots() []string {
	raw := strings.TrimSpace(os.Getenv(RootsEnv))
	if raw == "" {
		return nil
	}

	home, _ := os.UserHomeDir()

	var roots []string

	for g := range strings.SplitSeq(raw, string(os.PathListSeparator)) {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}

		if home != "" {
			if rest, ok := strings.CutPrefix(g, "~/"); ok {
				g = filepath.Join(home, rest)
			}
		}

		roots = append(roots, g)
	}

	return roots
}

// IsName reports whether a -C value is a project name rather than a path.
// Anything carrying a separator, a ~, or a leading dot is a path, and paths
// resolve without any of this.
func IsName(value string) bool {
	return value != "" &&
		!strings.ContainsRune(value, filepath.Separator) &&
		!strings.HasPrefix(value, "~") &&
		!strings.HasPrefix(value, ".")
}

// Resolve finds the one checkout named name under the configured roots.
// Nothing found and more than one found are both errors that name what was
// searched: a wrong guess here would run a command against the wrong repo.
func Resolve(name string) (string, error) {
	roots := Roots()
	if len(roots) == 0 {
		return "", ErrDisabled
	}

	var found []string

	for _, root := range roots {
		matches, err := filepath.Glob(root)
		if err != nil {
			return "", fmt.Errorf("bad glob %q in %s: %w", root, RootsEnv, err)
		}

		for _, m := range matches {
			if filepath.Base(m) == name && isCheckout(m) {
				found = append(found, m)
			}
		}
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("no checkout named %q under %s (%s)", name, RootsEnv, strings.Join(roots, ", "))
	default:
		return "", fmt.Errorf("%q matches %d checkouts (%s) - pass one as a path",
			name, len(found), strings.Join(found, ", "))
	}
}

// isCheckout reports whether dir is a git checkout. A linked worktree
// carries .git as a file rather than a directory, so both count.
func isCheckout(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}

	_, err = os.Stat(filepath.Join(dir, ".git"))

	return err == nil
}
