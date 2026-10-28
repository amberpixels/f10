package standup

import (
	"errors"
	"path"
	"strings"
	"time"
)

// Upkeep is activity that keeps a branch current rather than moving its
// task: catching up with the base, a rebase, regenerated files, a small
// test fix after red CI. Each rule reads facts git or the host recorded,
// never a subject line, and anything the rules cannot settle is work:
// hiding a real change costs more than listing a small one.

// The class of one item of activity, and of a task as a whole.
const (
	ClassWork   = "work"
	ClassUpkeep = "upkeep"
)

// Why an item is upkeep.
const (
	UpkeepBaseMerge = "base-merge" // a merge of the base branch into the task's
	UpkeepRebase    = "rebase"     // a force-push that kept every patch
	UpkeepFiles     = "files"      // only lockfiles, generated files or snapshots
	UpkeepCIFix     = "ci-fix"     // a small test-only change after red CI
)

// ciFixLines caps a CI fix: added plus deleted lines across its files.
const ciFixLines = 30

// A File is one path a commit changed, as numstat counts it.
type File struct {
	Path      string
	Added     int
	Deleted   int
	Binary    bool
	Generated bool // marked linguist-generated, or carrying Go's generated header
}

// CI is how the checks on a commit ended: red when any failed, and when
// the last failure finished.
type CI struct {
	Red bool
	At  time.Time
}

// A ForcePush is one force-push the user made to their own PR.
// SamePatches is whether the commits before and after carry the same
// patches; nil is unknown, when either side is not in the local object
// store.
type ForcePush struct {
	At          time.Time `json:"at"`
	Before      string    `json:"before"`
	After       string    `json:"after"`
	Class       string    `json:"class"`
	Upkeep      string    `json:"upkeep,omitempty"`
	SamePatches *bool     `json:"-"`
}

// upkeepGlobs are the paths a commit can touch and stay upkeep: lockfiles,
// generated code by its conventional names, and snapshot fixtures. A
// manifest (go.mod, package.json) is not here: a dependency bump next to
// code is not clearly upkeep.
var upkeepGlobs = []string{
	"go.sum", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock",
	"Gemfile.lock", "Cargo.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "composer.lock", "mix.lock",
	"flake.lock",
	"*.pb.go", "*_gen.go", "*.gen.*", "zz_generated*",
	"**/__snapshots__/**", "*.snap", "*.golden",
}

// testGlobs are the paths a CI fix may touch.
var testGlobs = []string{
	"*_test.go", "*_spec.rb", "*.test.*", "*.spec.*", "test_*.py", "*_test.py",
	"**/spec/**", "**/test/**", "**/tests/**", "**/__tests__/**", "**/testdata/**",
}

// classifyCommit is the class of one of the user's commits, and why.
func classifyCommit(c Commit, extra []string) (string, string) {
	if len(c.Parents) > 1 {
		if c.BaseMerge {
			return ClassUpkeep, UpkeepBaseMerge
		}

		return ClassWork, ""
	}

	switch {
	case upkeepFiles(c.Files, extra):
		return ClassUpkeep, UpkeepFiles
	case ciFixShape(c) && c.ParentCI != nil && c.ParentCI.Red && !c.ParentCI.At.After(c.At):
		return ClassUpkeep, UpkeepCIFix
	default:
		return ClassWork, ""
	}
}

func classifyPush(p ForcePush) (string, string) {
	if p.SamePatches != nil && *p.SamePatches {
		return ClassUpkeep, UpkeepRebase
	}

	return ClassWork, ""
}

// NeedsCI reports whether c is a CI fix if its parent's CI was red: the
// collector asks the host only for these.
func NeedsCI(c Commit, extra []string) bool {
	return len(c.Parents) == 1 && !upkeepFiles(c.Files, extra) && ciFixShape(c)
}

func upkeepFiles(files []File, extra []string) bool {
	if len(files) == 0 {
		return false
	}

	for _, f := range files {
		if !f.Generated && !matchAny(upkeepGlobs, f.Path) && !matchAny(extra, f.Path) {
			return false
		}
	}

	return true
}

// ciFixShape is a small change to test files only.
func ciFixShape(c Commit) bool {
	if len(c.Files) == 0 {
		return false
	}

	lines := 0

	for _, f := range c.Files {
		if f.Binary || !matchAny(testGlobs, f.Path) {
			return false
		}

		lines += f.Added + f.Deleted
	}

	return lines <= ciFixLines
}

// taskClass is upkeep when the task's activity from from until to (zero:
// no end) holds an upkeep item and no work item. Opening or merging a PR is
// work.
func taskClass(t Task, from, to time.Time) string {
	upkeep := false
	in := func(at time.Time) bool { return inWindow(at, from, to) }

	commits := func(cs []Commit) bool {
		for _, c := range cs {
			if !in(c.At) {
				continue
			}

			if c.Class != ClassUpkeep {
				return false
			}

			upkeep = true
		}

		return true
	}

	for _, p := range t.PRs {
		if in(p.CreatedAt) || (p.MergedAt != nil && in(*p.MergedAt)) {
			return ClassWork
		}

		if !commits(p.Commits) {
			return ClassWork
		}

		for _, fp := range p.ForcePushes {
			if !in(fp.At) {
				continue
			}

			if fp.Class != ClassUpkeep {
				return ClassWork
			}

			upkeep = true
		}
	}

	if !commits(t.Commits) || !upkeep {
		return ClassWork
	}

	return ClassUpkeep
}

// UpkeepPath reports whether a path is a lockfile, generated code or a
// snapshot: a change nobody wrote by hand.
func UpkeepPath(p string) bool { return matchAny(upkeepGlobs, p) }

func matchAny(globs []string, p string) bool {
	for _, g := range globs {
		if Glob(g, p) {
			return true
		}
	}

	return false
}

// Glob matches a slash-separated path the way .gitignore reads a pattern:
// one without a slash matches the base name in any directory, one with a
// slash the whole path, and a `**` segment any number of directories.
func Glob(pattern, p string) bool {
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(p))

		return ok
	}

	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}

	if pat[0] == "**" {
		// a trailing ** is what is inside a directory, never the directory
		if len(pat) == 1 {
			return len(segs) > 0
		}

		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}

		return false
	}

	if len(segs) == 0 {
		return false
	}

	ok, _ := path.Match(pat[0], segs[0])

	return ok && matchSegments(pat[1:], segs[1:])
}

// CheckGlob refuses a pattern Glob cannot read: a malformed segment, or
// an absolute path, since paths are relative to the repository root.
func CheckGlob(pattern string) error {
	if pattern == "" || strings.HasPrefix(pattern, "/") {
		return errors.New("want a path relative to the repository root")
	}

	for seg := range strings.SplitSeq(pattern, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
	}

	return nil
}
