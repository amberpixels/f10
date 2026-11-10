package finish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
)

// The janitor catches the task branches that went around finish: a PR merged
// in the host's UI, a worktree removed by hand. Every keep rule is checked
// before anything is deleted, and one branch failing never stops the next.

// JanitorEvery is how long start and finish wait between two janitor runs.
const JanitorEvery = 6 * time.Hour

// JanitorStamp's mtime is the last run.
const JanitorStamp = "janitor.stamp"

// SweepIfDue never fails the command that ran it: an error is one warning.
// The stamp moves first, so commands in quick succession run it once.
func SweepIfDue(ctx context.Context, w io.Writer, stamp string, now time.Time,
	run func(context.Context) ([]string, error),
) {
	if !JanitorDue(stamp, now) {
		return
	}

	if err := TouchStamp(stamp, now); err != nil {
		fmt.Fprintf(w, "janitor: warning: %v\n", err)

		return
	}

	lines, err := run(ctx)
	for _, l := range lines {
		fmt.Fprintln(w, "janitor: "+l)
	}

	if err != nil {
		fmt.Fprintf(w, "janitor: warning: %v\n", err)
	}
}

func JanitorDue(stamp string, now time.Time) bool {
	fi, err := os.Stat(stamp)

	return err != nil || now.Sub(fi.ModTime()) >= JanitorEvery
}

func TouchStamp(stamp string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(stamp), 0o750); err != nil {
		return fmt.Errorf("writing the janitor stamp: %w", err)
	}

	if err := os.WriteFile(stamp, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing the janitor stamp: %w", err)
	}

	return os.Chtimes(stamp, now, now)
}

// JanitorInput is everything the janitor needs, so it runs in a test
// without a checkout.
type JanitorInput struct {
	Main    string
	Host    *hostops.Host // nil without a host CLI: only branches never pushed qualify
	Prefix  string
	Storage storage.Root
	Dry     bool
	// LocalOnly: the watcher asks the network nothing before the host
	LocalOnly bool
}

func JanitorFor(t *target.Target) JanitorInput {
	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	in := JanitorInput{Main: main, Prefix: t.Effective.IDPrefix, Storage: t.Layout.StorageRoot}

	if h, err := hostops.HostOf(t); err == nil {
		h = h.In(main)
		in.Host = &h
	}

	return in
}

type StaleBranch struct {
	Name                string
	Ids                 []string // the tasks it ships, a bundle's lead first
	LocalSHA, originSHA string   // "" where the branch is not
	unread              error    // the local tip git failed to read, which keeps the branch
}

// Janitor reports a line per removal, and per branch kept for a reason worth
// knowing.
func Janitor(ctx context.Context, in JanitorInput) ([]string, error) {
	def, err := JanitorDefault(ctx, in.Main)
	if err != nil {
		return nil, err
	}

	branches, held, err := TaskBranches(ctx, in, def)
	if err != nil {
		return nil, err
	}

	j := &Sweeper{In: in, Def: def}

	removed := map[string]time.Time{}

	for _, b := range branches {
		if held[b.Name] {
			continue
		}

		if ended, ok := j.sweep(ctx, b); ok {
			removed[b.Name] = ended
		}
	}

	j.archive(branches, removed)

	if len(j.Lines) == 0 {
		j.Lines = append(j.Lines, "nothing to clear")
	}

	return j.Lines, nil
}

// JanitorDefault falls back to the main checkout's branch only with no origin.
func JanitorDefault(ctx context.Context, main string) (string, error) {
	if def, err := stack.LocalDefault(ctx, main); err == nil {
		return def, nil
	}

	// a git failure is not "no origin": the current branch would pass for the default
	origin, err := gitx.Config(ctx, main, "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("reading origin: %w", err)
	}

	if origin == "" {
		if cur, err := gitx.Branch(ctx, main); err != nil || cur != "" {
			return cur, err
		}
	}

	return "", errors.New("cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)")
}

// TaskBranches reads a branch as finish does: its link or recorded bundle,
// else its name. On origin a bare id counts only in the shapes start creates,
// so a name merely mentioning one is someone else's. held is every branch a
// checkout has, main's included.
func TaskBranches(ctx context.Context, in JanitorInput, def string) ([]StaleBranch, map[string]bool, error) {
	locals, err := gitx.Refs(ctx, in.Main, "refs/heads/")
	if err != nil {
		return nil, nil, err
	}

	var origin map[string]string
	if !in.LocalOnly {
		if origin, err = gitx.RemoteBranches(ctx, in.Main); err != nil {
			return nil, nil, err
		}
	}

	wts, err := gitx.Worktrees(ctx, in.Main)
	if err != nil {
		return nil, nil, err
	}

	held := map[string]bool{}
	for _, wt := range wts {
		held[wt.Branch] = true
	}

	x, err := stack.LoadBranchIndex(ctx, in.Main)
	if err != nil {
		return nil, nil, err
	}

	match := ref.Matcher(in.Prefix)
	byName := map[string]*StaleBranch{}

	add := func(name string, local bool) *StaleBranch {
		if b, ok := byName[name]; ok {
			return b
		}

		if name == def {
			return nil
		}

		// a link with no riders still names the task, as a picked-up PR's
		// branch may carry no id at all
		lead, rest := x.Bundle(name, in.Prefix)
		if lead == "" {
			lead = x.Record(name).Task
		}

		var ids []string

		switch {
		case lead != "":
			ids = append([]string{lead}, rest...)
		case local:
			if id := match(name); id != "" {
				ids = []string{id}
			}
		default:
			if id := match(name); id != "" && startShape(name, id) {
				ids = []string{id}
			}
		}

		if len(ids) == 0 {
			return nil
		}

		byName[name] = &StaleBranch{Name: name, Ids: ids}

		return byName[name]
	}

	for _, name := range locals {
		if b := add(name, true); b != nil {
			// a failed read is not an absent branch, which would let origin's copy go unchecked
			b.LocalSHA, b.unread = gitx.Ref(ctx, in.Main, "refs/heads/"+name)
		}
	}

	for name, sha := range origin {
		if b := add(name, false); b != nil {
			b.originSHA = sha
		}
	}

	out := make([]StaleBranch, 0, len(byName))
	for _, b := range byName {
		out = append(out, *b)
	}

	slices.SortFunc(out, func(a, b StaleBranch) int { return strings.Compare(a.Name, b.Name) })

	return out, held, nil
}

// A Sweeper is one janitor run: its lines, and the host login read once.
type Sweeper struct {
	In    JanitorInput
	Def   string
	Lines []string
	login *string
	prs   map[string]int // a removed branch's request, which closes its task's series
}

// Request is the request that decides b: an open one when any is, else
// the newest; nil when there is none.
func (j *Sweeper) Request(ctx context.Context, b StaleBranch) (*forge.PR, error) {
	prs, err := j.In.Host.PullsByHead(ctx, b.Name)
	if err != nil {
		return nil, fmt.Errorf("%s did not list its requests: %w", j.In.Host.Name, err)
	}

	if len(prs) == 0 {
		return nil, nil
	}

	for _, p := range prs[1:] {
		if p.Status == forge.Open {
			return &p, nil
		}
	}

	return &prs[0], nil
}

// SweepUnpushed removes a branch finish would read as never pushed once its
// changes are in the default branch; its work ended with its last commit.
func (j *Sweeper) SweepUnpushed(ctx context.Context, b StaleBranch) (time.Time, bool) {
	if b.LocalSHA == "" || b.originSHA != "" {
		return time.Time{}, false
	}

	// an upstream git cannot read keeps the branch for this pass
	upstream, err := gitx.Config(ctx, j.In.Main, "branch."+b.Name+".merge")

	switch {
	case err != nil:
		j.say("%s kept: reading its upstream failed: %v", b.Name, err)

		return time.Time{}, false
	case upstream != "":
		return time.Time{}, false
	case !alreadyLanded(ctx, j.In.Main, "refs/heads/"+j.Def, "refs/heads/"+b.Name):
		return time.Time{}, false
	}

	ended, _ := time.Parse(time.RFC3339, gitx.Out(ctx, j.In.Main, "log", "-1", "--format=%cI", b.LocalSHA))

	return ended, j.remove(ctx, b, nil)
}

func (j *Sweeper) say(format string, args ...any) {
	j.Lines = append(j.Lines, fmt.Sprintf(format, args...))
}

// sweep reports whether b was (or, dry, would be) removed, and when its
// request ended.
func (j *Sweeper) sweep(ctx context.Context, b StaleBranch) (time.Time, bool) {
	var (
		p   *forge.PR
		err error
	)

	if b.unread != nil {
		j.say("kept %s: reading its tip failed: %v", b.Name, b.unread)

		return time.Time{}, false
	}

	if j.In.Host != nil {
		if p, err = j.Request(ctx, b); err != nil {
			j.say("kept %s: %v", b.Name, err)

			return time.Time{}, false
		}
	}

	if p == nil {
		return j.SweepUnpushed(ctx, b)
	}

	if p.Status == forge.Open {
		return time.Time{}, false
	}

	if reason := j.keep(ctx, b, p); reason != "" {
		j.say("kept %s: %s", b.Name, reason)

		return time.Time{}, false
	}

	removed := j.remove(ctx, b, p)
	if removed {
		if j.prs == nil {
			j.prs = map[string]int{}
		}

		j.prs[b.Name] = p.Number
	}

	return cmp.Or(p.MergedAt, p.ClosedAt), removed
}

// keep is why a branch whose request merged or closed stays, or "".
func (j *Sweeper) keep(ctx context.Context, b StaleBranch, p *forge.PR) string {
	for _, tip := range []struct{ sha, whose string }{{b.LocalSHA, "its"}, {b.originSHA, "origin's"}} {
		if tip.sha == "" || tip.sha == p.HeadSHA {
			continue
		}

		lost, err := j.unlanded(ctx, tip.sha, p)
		if err != nil {
			return fmt.Sprintf("reading what %s tip has beyond %s's head failed: %v", tip.whose, p.URL, err)
		}

		if len(lost) > 0 {
			return tip.whose + " " + describeLost(lost) + " never reached " + p.URL
		}
	}

	stacked, err := j.In.Host.StackedPRs(ctx, b.Name)
	if err != nil {
		return "listing the requests based on it failed: " + err.Error()
	}

	if len(stacked) > 0 {
		return stacked[0].URL + " is based on it, and deleting the branch would close it"
	}

	if p.Status != forge.Merged {
		deps, err := dependentsOf(ctx, j.In.Main, b.Name)
		if err != nil {
			return "reading the branches started --after it failed: " + err.Error()
		}

		if len(deps) > 0 {
			return strings.Join(deps, ", ") + " started --after it, and " + p.URL + " closed without a merge"
		}
	}

	return ""
}

// unlanded is what tip has beyond the request's head that the default branch
// lacks too: a commit made after the last push, which the merge left behind.
// A tip that only merged main in, or one behind a head a colleague pushed, has
// none. A head the clone never saw (its branch deleted on origin after
// someone else's push) is fetched by sha first.
func (j *Sweeper) unlanded(ctx context.Context, tip string, p *forge.PR) ([]gitx.Cherry, error) {
	def := "refs/heads/" + j.Def

	lost, err := gitx.Unlanded(ctx, j.In.Main, def, tip, p.HeadSHA)
	if err == nil || !gitx.UnknownObject(err) {
		return lost, err
	}

	if err := gitx.FetchCommit(ctx, j.In.Main, p.HeadSHA); err != nil {
		return nil, fmt.Errorf("fetching its head %s: %w", gitx.ShortSHA(p.HeadSHA), err)
	}

	return gitx.Unlanded(ctx, j.In.Main, def, tip, p.HeadSHA)
}

// remove drops origin's copy only for the user's own request. A step failing
// is a line, and the branch counts as kept.
func (j *Sweeper) remove(ctx context.Context, b StaleBranch, p *forge.PR) bool {
	why := "never pushed, already in " + j.Def
	if p != nil {
		why = p.URL + " " + p.Status
	}

	var gone []string

	dropOrigin := b.originSHA != ""
	if dropOrigin && !j.own(ctx, p) {
		dropOrigin = false

		if b.LocalSHA == "" {
			return false
		}

		j.say("kept origin/%s: %s was opened by %s", b.Name, p.URL, cmp.Or(p.Author, "someone else"))
	}

	if b.LocalSHA != "" {
		gone = append(gone, "local branch")
	}

	if dropOrigin {
		gone = append(gone, "origin")
	}

	if j.In.Dry {
		j.say("would remove %s (%s): %s", b.Name, why, strings.Join(gone, ", "))

		return true
	}

	if p != nil && p.Status == forge.Merged {
		released, err := ReleaseDependents(ctx, j.In.Main, b.Name, j.Def, true)
		if err != nil {
			j.say("kept %s: %v", b.Name, err)

			return false
		}

		j.Lines = append(j.Lines, released...)
	}

	if dropOrigin {
		if err := j.In.Host.DeleteBranch(ctx, b.Name); err != nil {
			j.say("kept %s: deleting origin/%s failed: %v", b.Name, b.Name, err)

			return false
		}
	}

	if dropOrigin {
		if err := gitx.DeleteRemoteRef(ctx, j.In.Main, b.Name); err != nil {
			j.say("origin/%s is gone but its local copy is not: %v", b.Name, err)
		}
	}

	if b.LocalSHA != "" {
		if err := gitx.DeleteBranch(ctx, j.In.Main, b.Name); err != nil {
			j.say("kept %s: deleting the local branch failed: %v", b.Name, err)

			return false
		}
	}

	j.say("removed %s (%s): %s", b.Name, why, strings.Join(gone, ", "))

	return true
}

// own reports whether the signed-in host user opened p. A login the host
// will not tell, or a request with no author, is someone else's.
func (j *Sweeper) own(ctx context.Context, p *forge.PR) bool {
	if p == nil || p.Author == "" {
		return false
	}

	if j.login == nil {
		login, err := j.In.Host.Login(ctx)
		if err != nil {
			login = ""
		}

		j.login = &login
	}

	return *j.login != "" && strings.EqualFold(*j.login, p.Author)
}

// archive keeps the plan of a task with a branch left (a second attempt may
// still read it) and one written after the request ended (the task was
// planned again); a kept plan keeps its images.
func (j *Sweeper) archive(branches []StaleBranch, removed map[string]time.Time) {
	left := map[string][]string{}
	cleared := map[string]time.Time{}
	ends := map[string]archive.SeriesEnd{} // what closes each task's series: its removed branch

	for _, b := range branches {
		ended, ok := removed[b.Name]

		for _, id := range b.Ids {
			if ok {
				cleared[id] = laterOf(cleared[id], ended)
				ends[id] = archive.SeriesEnd{Pr: "none", Head: cmp.Or(b.LocalSHA, b.originSHA)}
				if n := j.prs[b.Name]; n > 0 {
					ends[id] = archive.SeriesEnd{Pr: "#" + strconv.Itoa(n), Head: ends[id].Head}
				}
			} else {
				left[id] = append(left[id], b.Name)
			}
		}
	}

	ids := make([]string, 0, len(cleared))
	for id := range cleared {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		plan := j.In.Storage.Plan(id)
		live := fsx.Exists(plan)

		switch {
		case len(left[id]) > 0:
			if live {
				j.say("kept the plan for %s: %s still uses it", id, strings.Join(left[id], ", "))
			}
		case live && writtenAfter(plan, cleared[id]):
			j.say("kept the plan for %s: it was written after its branch's work ended", id)
		case j.In.Dry:
			for _, p := range []string{plan, j.In.Storage.Reviews(id), archive.TaskHandoff(j.In.Storage, id)} {
				if fsx.Exists(p) {
					j.say("would archive %s", p)
				}
			}
		default:
			j.clear(id, ends[id])
		}
	}
}

// clear archives a task whose branches are all gone; closing its review
// series lets a later PR on the task start at round 1 with its own lore
// change. A failed step is a line, and the rest still run.
func (j *Sweeper) clear(id string, end archive.SeriesEnd) {
	plan := j.In.Storage.Plan(id)

	a, _ := archiveTask(j.In.Storage, id, &end, func(_ string, err error) error {
		j.say("%v", err)

		return nil
	})

	if a.plan != "" {
		j.say("archived %s to %s", plan, a.plan)
	}

	if a.reviews != "" {
		j.say("archived %s's findings to %s", id, a.reviews)
	}

	switch err := taskcmd.MoveHandoff(a.handoffSrc, a.handoff); {
	case err != nil:
		j.say("%v", err)
	case a.handoff != "":
		j.say("archived %s's handoff to %s", id, a.handoff)
	}

	if a.images != "" {
		j.say("%s", a.images)
	}
}

// describeLost names the oldest lost commit, and counts the rest.
func describeLost(lost []gitx.Cherry) string {
	first := fmt.Sprintf("%q (%s)", lost[0].Subject, gitx.ShortSHA(lost[0].SHA))

	switch len(lost) {
	case 1:
		return first
	case 2:
		return first + " and 1 more commit"
	default:
		return fmt.Sprintf("%s and %d more commits", first, len(lost)-1)
	}
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

// writtenAfter reports whether path changed after t; a zero t is no bound.
func writtenAfter(path string, t time.Time) bool {
	fi, err := os.Stat(path)

	return err == nil && !t.IsZero() && fi.ModTime().After(t)
}

func startShape(name, id string) bool {
	return name == id || strings.HasPrefix(name, id+"/") || strings.HasPrefix(name, id+"-") ||
		strings.HasPrefix(name, id+ref.Separator)
}

func dependentsOf(ctx context.Context, dir, branch string) ([]string, error) {
	records, err := branchmeta.LoadAll(ctx, dir)
	if err != nil {
		return nil, err
	}

	return branchmeta.Dependents(records, branch), nil
}
