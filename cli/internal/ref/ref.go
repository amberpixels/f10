// Package ref looks up which task a lookaround command is about. Every noun
// takes the same optional reference and falls back the same way, so the
// cascade lives here once: an explicit id, then the id in the current branch
// name, then the task this session's run state records.
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
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/state"
)

// Where a looked-up reference came from. Reported so an error, or a caller
// that wants to say "GH-22, from the branch", can name it.
const (
	OriginExplicit = "explicit"
	OriginBranch   = "branch"
	OriginSession  = "session"
)

// TaskKey is the local config key, under `branch.<name>.`, that links a
// branch to its task: start writes it on a branch picked up from a PR,
// whose name need not carry the id, and on a bundle's branch, for its lead.
const TaskKey = "f10-task"

// RidersKey is the key, beside TaskKey, that lists a bundle's riders,
// space-separated: the tasks one branch ships beside its lead.
const RidersKey = "f10-tasks"

// The keys `f10 start --after` writes beside TaskKey: the base task's id
// and its bare branch name. Once the base merges, finish clears both and
// sets LandedKey to the base's id, so the next step brings the default
// branch in first.
const (
	AfterKey       = "f10-after"
	AfterBranchKey = "f10-after-branch"
	LandedKey      = "f10-landed"
)

// ModelKey records the model `f10 start --model` gave the branch's agent,
// for a reader of the branch; absent for claude's default and on branches
// started before it was written.
const ModelKey = "f10-model"

// Separator joins a bundle's ids where they are typed and in its branch
// name: `ABC-1+ABC-2`, the lead first.
const Separator = "+"

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
	// Bundle is every id of the bundle the task belongs to, lead first, or
	// nil for a task that ships alone
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

// numberOf is the number a parsed id ends in: "22" for GH-22 and for 22.
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

// Find walks the cascade. An empty explicit reference falls through to
// the branch and then to the session; a non-empty one that does not parse
// is an error rather than a fallthrough, because a typo'd id should say so
// instead of quietly opening whatever the branch happens to name.
//
// Any member of a bundle comes back with the whole bundle: a `+`-joined
// token as the lead, a single id as itself, so reading a rider still reads
// the rider and Head finds the branch.
func (r Lookup) Find(ctx context.Context, explicit string) (Ref, error) {
	if strings.TrimSpace(explicit) != "" {
		if !isURL(explicit) && strings.Contains(explicit, Separator) {
			return r.parseBundle(explicit)
		}

		return r.bundled(ctx, explicit, OriginExplicit, nil)
	}

	if id, members := r.fromBranch(ctx); id != "" {
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
func (r Lookup) bundleOf(ctx context.Context, id string) []string {
	for _, e := range gitx.ConfigEntries(ctx, r.Dir, `^branch\..*\.`+RidersKey+`$`) {
		branch := strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+RidersKey)
		riders := strings.Fields(e.Value)

		lead := gitx.Out(ctx, r.Dir, "config", "--get", "branch."+branch+"."+TaskKey)
		if lead == "" {
			lead = Match(r.Prefix, branch)
		}

		if lead == "" || len(riders) == 0 {
			continue
		}

		same := func(s string) bool { return strings.EqualFold(s, id) }
		if same(lead) || slices.ContainsFunc(riders, same) {
			return append([]string{lead}, riders...)
		}
	}

	return nil
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

// bundled parses token and attaches its bundle: members when the caller
// already knows them, else the bundle local config records it in.
func (r Lookup) bundled(ctx context.Context, token, origin string, members []string) (Ref, error) {
	got, err := r.parse(token, origin)
	if err != nil {
		return got, err
	}

	if members == nil {
		members = r.bundleOf(ctx, got.ID)
	}

	got.Bundle = members

	return got, nil
}

// fromBranch pulls the task id out of the current branch name, with its
// bundle when the branch ships one. Real branch names carry the id in any
// position and any case - `ABC-2049/dark-mode`, `feature/abc-123-fix-bug` -
// so the prefix is anchored on a non-word boundary rather than on the start
// of the string. A branch picked up from a PR may carry no id at all, so
// the task its local config links it to (`branch.<name>.f10-task`) is read
// first, and the riders recorded beside it.
func (r Lookup) fromBranch(ctx context.Context) (string, []string) {
	branch := gitx.Out(ctx, r.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		return "", nil
	}

	section := "branch." + branch + "."

	if id := gitx.Out(ctx, r.Dir, "config", "--get", section+TaskKey); id != "" {
		var members []string
		if riders := strings.Fields(gitx.Out(ctx, r.Dir, "config", "--get", section+RidersKey)); len(riders) > 0 {
			members = append([]string{id}, riders...)
		}

		return id, members
	}

	if r.Prefix == "" {
		return "", nil
	}

	members := Members(r.Prefix, branch)
	if members == nil {
		return Match(r.Prefix, branch), nil
	}

	return members[0], members
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

// Match is the task id in text: a branch name, a PR title, a commit
// subject. It is fromBranch minus git, so standup reads titles and subjects
// by the same rule and a test needs no checkout.
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

// sessionTask reads the task id the run state records for this session.
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
