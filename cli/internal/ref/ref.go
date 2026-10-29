// Package ref looks up which task a lookaround command is about, one cascade
// for every noun: an explicit id, then the branch name, then this session's
// run state. Without a prefix from facts only an explicit reference is taken,
// since no rule can tell a task number in a branch name from any other number.
package ref

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/state"
)

// Where a looked-up reference came from, so an error or a caller can name it.
const (
	OriginExplicit = "explicit"
	OriginBranch   = "branch"
	OriginSession  = "session"
)

// TaskKey is the local config key under `branch.<name>.` linking a branch to
// its task: a branch picked up from a PR need not carry the id in its name,
// and a bundle's branch links its lead.
const TaskKey = "f10-task"

// RidersKey is the key, beside TaskKey, that lists a bundle's riders,
// space-separated: the tasks one branch ships beside its lead.
const RidersKey = "f10-tasks"

// What `f10 start --after` records beside TaskKey. Finish swaps the After
// keys for LandedKey so the next step brings the default branch in first;
// MovedKey is drive's note that the base's head moved past the dependent,
// for the next catchup, which clears both.
const (
	AfterKey       = "f10-after"
	AfterBranchKey = "f10-after-branch"
	LandedKey      = "f10-landed"
	MovedKey       = "f10-base-moved"
)

// ModelKey is the model `f10 start --model` gave the branch's agent; absent
// for claude's default.
const ModelKey = "f10-model"

// Separator joins a bundle's ids, `ABC-1+ABC-2`, lead first.
const Separator = "+"

// ErrNoReference means the cascade ran out. It is wrapped with what each
// tier had to offer: "none found" alone sends a reader to the wrong tier,
// most often a session tier a human's prompt never had.
var ErrNoReference = errors.New("no task reference given")

// A Ref is one looked-up task reference.
type Ref struct {
	ID     string // "GH-22": the prefix and number in the project's format
	Number string // "22": what a host CLI wants
	Origin string
	// Bundle is the bundle's every id, lead first; nil for a task shipping alone
	Bundle []string
}

// Head is the reference a branch is looked up by: the bundle's lead, or the
// task itself. Plan, agent, run state and ledger all key on it.
func (r Ref) Head() Ref {
	if len(r.Bundle) == 0 || r.Bundle[0] == r.ID {
		return r
	}

	return Ref{ID: r.Bundle[0], Number: numberOf(r.Bundle[0]), Origin: r.Origin, Bundle: r.Bundle}
}

// MemberRefs is every member as a reference of its own, the lead first.
func (r Ref) MemberRefs() []Ref {
	if len(r.Bundle) == 0 {
		return []Ref{r}
	}

	refs := make([]Ref, 0, len(r.Bundle))
	for _, id := range r.Bundle {
		refs = append(refs, Ref{ID: id, Number: numberOf(id), Origin: r.Origin})
	}

	return refs
}

func numberOf(id string) string {
	return id[strings.LastIndexByte(id, '-')+1:]
}

// Members is every id the reference ships: the bundle, or the task alone.
func (r Ref) Members() []string {
	if len(r.Bundle) == 0 {
		return []string{r.ID}
	}

	return r.Bundle
}

// Riders is the bundle's ids after its lead, or nil.
func (r Ref) Riders() []string {
	if len(r.Bundle) < 2 {
		return nil
	}

	return r.Bundle[1:]
}

// Label is the reference as typed at an entry point: `ABC-1+ABC-2`, or the id.
func (r Ref) Label() string {
	return strings.Join(r.Members(), Separator)
}

// A Lookup answers references for one checkout.
type Lookup struct {
	Prefix string // id prefix from facts ("ABC", "GH"); empty where nothing declares or implies one
	Dir    string // checkout root - the branch is read from here
	// NoSession stops the cascade at the branch: a commit's trailer names the
	// branch's task or none, never one this session happens to have recorded
	NoSession bool
}

// Find walks the cascade. An explicit reference that does not parse is an
// error, not a fallthrough, since a typo'd id should say so instead of opening
// whatever the branch names; any bundle member comes back with the whole
// bundle, so reading a rider reads the rider and Head finds the branch.
func (r Lookup) Find(ctx context.Context, explicit string) (Ref, error) {
	if strings.TrimSpace(explicit) != "" {
		if !isURL(explicit) && strings.Contains(explicit, Separator) {
			return r.parseBundle(explicit)
		}

		return r.bundled(ctx, explicit, OriginExplicit, nil)
	}

	id, members, err := r.fromBranch(ctx)
	if err != nil {
		return Ref{}, err
	}

	if id != "" {
		return r.bundled(ctx, id, OriginBranch, members)
	}

	if id := sessionTask(); id != "" && !r.NoSession {
		return r.bundled(ctx, id, OriginSession, nil)
	}

	return Ref{}, r.exhausted(ctx)
}

// parseBundle reads `ABC-1+ABC-2` as a bundle: the lead's reference, with
// every member parsed alike. A repeated member, or a single one, is a typo.
func (r Lookup) parseBundle(token string) (Ref, error) {
	parts := strings.Split(strings.TrimSpace(token), Separator)
	if len(parts) < 2 {
		return Ref{}, fmt.Errorf("a bundle is two or more ids joined by %s: %s", Separator, token)
	}

	var lead Ref

	members := make([]string, 0, len(parts))

	for i, part := range parts {
		got, err := r.parse(part, OriginExplicit)
		if err != nil {
			return Ref{}, fmt.Errorf("in bundle %s: %w", token, err)
		}

		if slices.Contains(members, got.ID) {
			return Ref{}, fmt.Errorf("bundle %s names %s twice", token, got.ID)
		}

		if i == 0 {
			lead = got
		}

		members = append(members, got.ID)
	}

	lead.Bundle = members

	return lead, nil
}

// bundleOf is the bundle local config records id in, lead first, or nil:
// a branch whose riders list it, or whose linked lead it is.
func (r Lookup) bundleOf(ctx context.Context, id string) ([]string, error) {
	entries, err := gitx.ConfigEntries(ctx, r.Dir, `^branch\..*\.`+RidersKey+`$`)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		branch := strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+RidersKey)
		riders := strings.Fields(e.Value)

		lead, err := gitx.Config(ctx, r.Dir, "branch."+branch+"."+TaskKey)
		if err != nil {
			return nil, err
		}

		if lead == "" {
			lead = Match(r.Prefix, branch)
		}

		if lead == "" || len(riders) == 0 {
			continue
		}

		same := func(s string) bool { return strings.EqualFold(s, id) }
		if same(lead) || slices.ContainsFunc(riders, same) {
			return append([]string{lead}, riders...), nil
		}
	}

	return nil, nil
}

// exhausted wraps ErrNoReference with what each tier had to offer.
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

// numberRE is a typed reference: a bare number, `#123`, a prefixed id in any
// case. A url's last path segment is held to the same shape, so a pasted
// issue link is as good as the id it ends in.
var numberRE = regexp.MustCompile(`^(?:([A-Za-z][A-Za-z0-9]*)-)?#?(\d+)$`)

// suffixRE is a reference salted with a word (`1454-attempt2`, `GH-1_v2`):
// `f10 start` salts the branch so a second worktree for one task can live
// beside the first.
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

func (r Lookup) bundled(ctx context.Context, token, origin string, members []string) (Ref, error) {
	got, err := r.parse(token, origin)
	if err != nil {
		return got, err
	}

	if members == nil {
		if members, err = r.bundleOf(ctx, got.ID); err != nil {
			return got, err
		}
	}

	got.Bundle = members

	return got, nil
}

// fromBranch reads the link local config holds before the branch name, since
// a branch picked up from a PR may carry no id. An unreadable link is an
// error, not a fall back to the name: a bundle's branch read without it would
// answer for the lead alone.
func (r Lookup) fromBranch(ctx context.Context) (string, []string, error) {
	branch := gitx.Out(ctx, r.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		return "", nil, nil
	}

	section := "branch." + branch + "."

	id, err := gitx.Config(ctx, r.Dir, section+TaskKey)
	if err != nil {
		return "", nil, err
	}

	if id != "" {
		riders, err := gitx.Config(ctx, r.Dir, section+RidersKey)
		if err != nil {
			return "", nil, err
		}

		var members []string
		if fields := strings.Fields(riders); len(fields) > 0 {
			members = append([]string{id}, fields...)
		}

		return id, members, nil
	}

	if r.Prefix == "" {
		return "", nil, nil
	}

	members := Members(r.Prefix, branch)
	if members == nil {
		return Match(r.Prefix, branch), nil, nil
	}

	return members[0], members, nil
}

// Members is the bundle a branch name spells in its first segment,
// `ABC-1+ABC-2/slug`, lead first, or nil for a name that spells none.
func Members(prefix, branch string) []string {
	if prefix == "" {
		return nil
	}

	head, _, _ := strings.Cut(branch, "/")

	parts := strings.Split(head, Separator)
	if len(parts) < 2 {
		return nil
	}

	re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(prefix) + `-([0-9]+)$`)

	members := make([]string, 0, len(parts))

	for _, p := range parts {
		m := re.FindStringSubmatch(p)
		if m == nil {
			return nil
		}

		members = append(members, prefix+"-"+m[1])
	}

	return members
}

// Match is the task id in text (a branch name, a PR title, a commit subject),
// anchored on a non-word boundary rather than the start since the id sits in
// any position and case (`feature/abc-123-fix-bug`).
func Match(prefix, text string) string {
	return Matcher(prefix)(text)
}

// Matcher is Match with the pattern compiled once, for a caller matching
// many texts against one prefix.
func Matcher(prefix string) func(text string) string {
	if prefix == "" {
		return func(string) string { return "" }
	}

	re := regexp.MustCompile(`(?i)(?:^|[^a-z0-9])` + regexp.QuoteMeta(prefix) + `-?([0-9]+)`)

	return func(text string) string {
		if m := re.FindStringSubmatch(text); m != nil {
			return prefix + "-" + m[1]
		}

		return ""
	}
}

func sessionTask() string {
	sid := state.Session()
	if sid == "" {
		return ""
	}

	r, err := state.Load(state.Path(state.Dir(), sid))
	if err != nil {
		return ""
	}

	return r.Task
}
