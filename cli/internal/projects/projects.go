// Package projects maps a bare project name to a checkout directory, for
// `-C r3` run from elsewhere. Lookup is off unless F10_ROOTS is set: GH-20
// fenced the binary as answering for the repo it runs in, never walking the
// filesystem for others, and a basename search over projects is that walk.
package projects

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// RootsEnv is a PATH-style list of globs expanding to checkouts, such as
// `~/code/github.com/*/*`.
const RootsEnv = "F10_ROOTS"

// ErrDisabled means a name was given while name lookup is off.
var ErrDisabled = errors.New("finding a project by name needs " + RootsEnv +
	" set to a colon-separated list of globs (for example ~/code/github.com/*/*); a path such as -C ../r3 always works")

// Roots is the glob list, ~ expanded; empty means name lookup is off.
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

// IsName reports a -C value that is a project name rather than a path.
func IsName(value string) bool {
	return value != "" &&
		!strings.ContainsRune(value, filepath.Separator) &&
		!strings.HasPrefix(value, "~") &&
		!strings.HasPrefix(value, ".")
}

// Find is the one checkout named name under the roots; none or several are
// errors naming what was searched, since a guess would run a command against
// the wrong repo.
func Find(name string) (string, error) {
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
			if filepath.Base(m) == name && IsCheckout(m) {
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

// All is every checkout under the roots, sorted, for `f10 standup --all`.
func All() ([]string, error) {
	roots := Roots()
	if len(roots) == 0 {
		return nil, ErrDisabled
	}

	var found []string

	for _, root := range roots {
		matches, err := filepath.Glob(root)
		if err != nil {
			return nil, fmt.Errorf("bad glob %q in %s: %w", root, RootsEnv, err)
		}

		for _, m := range matches {
			if IsCheckout(m) {
				found = append(found, m)
			}
		}
	}

	slices.Sort(found)

	return slices.Compact(found), nil
}

// IsCheckout reports whether dir is a git checkout. A linked worktree
// carries .git as a file rather than a directory, so both count.
func IsCheckout(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}

	_, err = os.Stat(filepath.Join(dir, ".git"))

	return err == nil
}
