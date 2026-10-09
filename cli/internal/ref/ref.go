// Package ref looks up which task a lookaround command is about. Every noun
// takes the same optional reference and falls back the same way, so the
// cascade lives here once: an explicit id, then the id in the current branch
// name, then the task this session's f10-state file records.
//
// The prefix that makes a branch searchable comes from facts - the task id
// format a project declares, or the one its name derives. Without a prefix
// only an explicit reference is accepted, because no rule can tell a task
// number in a branch name from any other number.
package ref

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// Where a looked-up reference came from. Reported so an error, or a caller
// that wants to say "GH-22, from the branch", can name it.
const (
	OriginExplicit = "explicit"
	OriginBranch   = "branch"
	OriginSession  = "session"
)

// ErrNoReference means the cascade ran out. It is wrapped with what each
// tier actually had to offer, because "none found" alone sends a reader
// looking in the wrong place - most often at a session tier that was never
// available to them.
var ErrNoReference = errors.New("no task reference given")

// A Ref is one looked-up task reference.
type Ref struct {
	ID     string // "GH-22": the prefix and number in the project's format
	Number string // "22": what a host CLI wants
	Origin string
}

// A Lookup answers references for one checkout.
type Lookup struct {
	Prefix string // id prefix from facts ("ABC", "GH"); empty where nothing declares or implies one
	Dir    string // checkout root - the branch is read from here
}

// Find walks the cascade. An empty explicit reference falls through to
// the branch and then to the session; a non-empty one that does not parse
// is an error rather than a fallthrough, because a typo'd id should say so
// instead of quietly opening whatever the branch happens to name.
func (r Lookup) Find(ctx context.Context, explicit string) (Ref, error) {
	if strings.TrimSpace(explicit) != "" {
		return r.parse(explicit, OriginExplicit)
	}

	if id := r.fromBranch(ctx); id != "" {
		return r.parse(id, OriginBranch)
	}

	if id := sessionTask(); id != "" {
		return r.parse(id, OriginSession)
	}

	return Ref{}, r.exhausted(ctx)
}

// exhausted explains which tier came up empty and why. The session tier is
// the one worth naming out loud: it keys on an agent session's id, so at a
// human's prompt it is not empty, it is absent.
func (r Lookup) exhausted(ctx context.Context) error {
	var why []string

	if r.Prefix == "" {
		why = append(why, "no task id format for this project, so a branch cannot be searched")
	} else if branch := gitx.Out(ctx, r.Dir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		why = append(why, fmt.Sprintf("branch %q carries no %s-<n>", branch, r.Prefix))
	}

	if cmp.Or(os.Getenv("F10_SESSION_ID"), os.Getenv("CLAUDE_CODE_SESSION_ID")) == "" {
		why = append(why, "no agent session to have recorded one")
	} else {
		why = append(why, "this session has recorded no task")
	}

	return fmt.Errorf("%w (%s)", ErrNoReference, strings.Join(why, "; "))
}

// numberRE is the reference shapes a user types: a bare number, a `#123`,
// or a prefixed id in any case. A url is accepted too - its last path
// segment is held to the same shape, so an issue link pasted from the
// browser (`.../issues/42`, `.../browse/ABC-7`) is as good as the id it
// ends in.
var numberRE = regexp.MustCompile(`^(?:([A-Za-z][A-Za-z0-9]*)-)?#?(\d+)$`)

// suffixRE is the salted shape `f10 start` takes: a reference glued to a
// word by `-` or `_` (`1454-attempt2`, `GH-1_v2`). The word salts the
// branch so a second worktree for one task can live beside the first.
var suffixRE = regexp.MustCompile(`^((?:[A-Za-z][A-Za-z0-9]*-)?#?\d+)[-_]([A-Za-z0-9][A-Za-z0-9_-]*)$`)

// SplitSuffix separates a salted reference into the reference and its
// suffix. A token without one, or a url, comes back whole with "".
func SplitSuffix(token string) (string, string) {
	token = strings.TrimSpace(token)
	if isURL(token) {
		return token, ""
	}

	if m := suffixRE.FindStringSubmatch(token); m != nil {
		return m[1], m[2]
	}

	return token, ""
}

func isURL(token string) bool {
	return strings.Contains(token, "://")
}

// lastSegment is where a url carries its reference: the final path
// segment, query and fragment dropped.
func lastSegment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	segs := strings.Split(strings.Trim(u.Path, "/"), "/")

	return segs[len(segs)-1]
}

func (r Lookup) parse(token, origin string) (Ref, error) {
	probe := strings.TrimSpace(token)
	if isURL(probe) {
		probe = lastSegment(probe)
	}

	m := numberRE.FindStringSubmatch(strings.TrimPrefix(probe, "#"))
	if m == nil {
		return Ref{}, errors.New("not a task reference: " + token)
	}

	prefix := r.Prefix
	if m[1] != "" {
		prefix = strings.ToUpper(m[1])
	}

	id := m[2]
	if prefix != "" {
		id = prefix + "-" + m[2]
	}

	return Ref{ID: id, Number: m[2], Origin: origin}, nil
}

// fromBranch pulls the task id out of the current branch name. Real branch
// names carry it in any position and any case - `ABC-2049/dark-mode`,
// `feature/abc-123-fix-bug` - so the prefix is anchored on a non-word
// boundary rather than on the start of the string.
func (r Lookup) fromBranch(ctx context.Context) string {
	if r.Prefix == "" {
		return ""
	}

	return matchBranch(r.Prefix, gitx.Out(ctx, r.Dir, "rev-parse", "--abbrev-ref", "HEAD"))
}

// matchBranch is fromBranch minus git, which is what makes it testable
// without standing up a checkout per case.
func matchBranch(prefix, branch string) string {
	if prefix == "" || branch == "" {
		return ""
	}

	re := regexp.MustCompile(`(?i)(?:^|[^a-z0-9])` + regexp.QuoteMeta(prefix) + `-?([0-9]+)`)

	if m := re.FindStringSubmatch(branch); m != nil {
		return prefix + "-" + m[1]
	}

	return ""
}

// sessionTask reads the task id f10-state.sh recorded for this session.
// The state file is flat `key value` lines by design - it is read on every
// status-line tick - so it is parsed here rather than shelled out to.
func sessionTask() string {
	sid := cmp.Or(os.Getenv("F10_SESSION_ID"), os.Getenv("CLAUDE_CODE_SESSION_ID"))
	if sid == "" {
		return ""
	}

	// the same sanitising the script does: the id names a file
	sid = regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(sid, "_")

	dir := os.Getenv("F10_STATE_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}

		dir = filepath.Join(home, ".claude", "f10", "state")
	}

	// through a root rather than a joined path: the session id arrives from
	// the environment, and Root.Open refuses to leave the state directory
	// however it was spelled
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}

	defer root.Close()

	f, err := root.Open(sid + ".state")
	if err != nil {
		return ""
	}

	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "task "); ok {
			return strings.TrimSpace(v)
		}
	}

	return ""
}
