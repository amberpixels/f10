package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// The janitor catches the task branches that went around finish: a PR merged
// in the host's UI, a worktree removed by hand. Every keep rule is checked
// before anything is deleted, and one branch failing never stops the next.

// janitorEvery is how long start and finish wait between two janitor runs.
const janitorEvery = 6 * time.Hour

// janitorStamp's mtime is the last run.
const janitorStamp = "janitor.stamp"

func janitorCommand() *cli.Command {
	return &cli.Command{
		Name: "janitor",
		Usage: "remove task branches whose PR merged or closed, locally and on origin, and archive their plans; " +
			"f10 start and f10 finish run it every few hours",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "list what would be removed and change nothing"},
		},
		Action: runJanitor,
	}
}

func runJanitor(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	in := janitorFor(t)
	in.dry = cmd.Bool("dry-run")

	if !in.dry {
		if err := touchStamp(filepath.Join(t.sharedRoot(), janitorStamp), time.Now()); err != nil {
			return err
		}
	}

	lines, err := janitor(ctx, in)
	for _, l := range lines {
		fmt.Fprintln(cmd.Writer, l)
	}

	return err
}

// sweepIfDue never fails the command that ran it: an error is one warning.
// The stamp moves first, so commands in quick succession run it once.
func sweepIfDue(ctx context.Context, w io.Writer, stamp string, now time.Time,
	run func(context.Context) ([]string, error),
) {
	if !janitorDue(stamp, now) {
		return
	}

	if err := touchStamp(stamp, now); err != nil {
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

func janitorDue(stamp string, now time.Time) bool {
	fi, err := os.Stat(stamp)

	return err != nil || now.Sub(fi.ModTime()) >= janitorEvery
}

func touchStamp(stamp string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(stamp), 0o750); err != nil {
		return fmt.Errorf("writing the janitor stamp: %w", err)
	}

	if err := os.WriteFile(stamp, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing the janitor stamp: %w", err)
	}

	return os.Chtimes(stamp, now, now)
}

// janitorInput is everything the janitor needs, so it runs in a test
// without a checkout.
type janitorInput struct {
	main     string
	host     *host // nil without a host CLI: only branches never pushed qualify
	prefix   string
	plansDir string
	dry      bool
	// localOnly: the watcher asks the network nothing before the host
	localOnly bool
}

func janitorFor(t *target) janitorInput {
	main := cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)
	in := janitorInput{main: main, prefix: t.eff.IDPrefix, plansDir: t.sharedPlansDir()}

	if h, err := t.host(); err == nil {
		h = h.at(main)
		in.host = &h
	}

	return in
}

type staleBranch struct {
	name                string
	ids                 []string // the tasks it ships, a bundle's lead first
	localSHA, originSHA string   // "" where the branch is not
}

// janitor reports a line per removal, and per branch kept for a reason worth
// knowing.
func janitor(ctx context.Context, in janitorInput) ([]string, error) {
	def, err := janitorDefault(ctx, in.main)
	if err != nil {
		return nil, err
	}

	branches, held, err := taskBranches(ctx, in, def)
	if err != nil {
		return nil, err
	}

	j := &sweeper{in: in, def: def}

	removed := map[string]time.Time{}

	for _, b := range branches {
		if held[b.name] {
			continue
		}

		if ended, ok := j.sweep(ctx, b); ok {
			removed[b.name] = ended
		}
	}

	j.archive(branches, removed)

	if len(j.lines) == 0 {
		j.lines = append(j.lines, "nothing to clear")
	}

	return j.lines, nil
}

// janitorDefault falls back to the main checkout's branch only with no origin.
func janitorDefault(ctx context.Context, main string) (string, error) {
	if def, err := localDefault(ctx, main); err == nil {
		return def, nil
	}

	if gitx.Out(ctx, main, "remote", "get-url", "origin") == "" {
		if cur := gitx.CurrentBranch(ctx, main); cur != "" {
			return cur, nil
		}
	}

	return "", errors.New("cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)")
}

// taskBranches reads a branch as finish does: its link or recorded bundle,
// else its name. On origin a bare id counts only in the shapes start creates,
// so a name merely mentioning one is someone else's. held is every branch a
// checkout has, main's included.
func taskBranches(ctx context.Context, in janitorInput, def string) ([]staleBranch, map[string]bool, error) {
	locals, err := gitx.Refs(ctx, in.main, "refs/heads/")
	if err != nil {
		return nil, nil, err
	}

	var origin map[string]string
	if !in.localOnly {
		if origin, err = gitx.RemoteBranches(ctx, in.main); err != nil {
			return nil, nil, err
		}
	}

	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return nil, nil, err
	}

	held := map[string]bool{}
	for _, wt := range wts {
		held[wt.Branch] = true
	}

	// one config read for every branch's link and recorded riders
	linked, riders := map[string]string{}, map[string][]string{}

	entries, err := gitx.ConfigEntries(ctx, in.main, cfgTaskKeys)
	if err != nil {
		return nil, nil, err
	}

	for _, e := range entries {
		if b, ok := strings.CutSuffix(e.Key, "."+cfgTaskID); ok {
			linked[strings.TrimPrefix(b, "branch.")] = e.Value
		} else if b, ok := strings.CutSuffix(e.Key, "."+cfgRiders); ok {
			riders[strings.TrimPrefix(b, "branch.")] = strings.Fields(e.Value)
		}
	}

	match := ref.Matcher(in.prefix)
	byName := map[string]*staleBranch{}

	add := func(name string, local bool) *staleBranch {
		if b, ok := byName[name]; ok {
			return b
		}

		if name == def {
			return nil
		}

		// the bundle as branchBundle reads it, from the config read above
		lead, rest := linked[name], riders[name]
		if len(rest) > 0 {
			lead = cmp.Or(lead, match(name))
		} else if members := ref.Members(in.prefix, name); members != nil {
			lead, rest = members[0], members[1:]
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

		byName[name] = &staleBranch{name: name, ids: ids}

		return byName[name]
	}

	for _, name := range locals {
		if b := add(name, true); b != nil {
			b.localSHA = gitx.Out(ctx, in.main, "rev-parse", "-q", "--verify", "refs/heads/"+name)
		}
	}

	for name, sha := range origin {
		if b := add(name, false); b != nil {
			b.originSHA = sha
		}
	}

	out := make([]staleBranch, 0, len(byName))
	for _, b := range byName {
		out = append(out, *b)
	}

	slices.SortFunc(out, func(a, b staleBranch) int { return strings.Compare(a.name, b.name) })

	return out, held, nil
}

// A sweeper is one janitor run: its lines, and the host login read once.
type sweeper struct {
	in    janitorInput
	def   string
	lines []string
	login *string
}

func (j *sweeper) say(format string, args ...any) {
	j.lines = append(j.lines, fmt.Sprintf(format, args...))
}

// sweep reports whether b was (or, dry, would be) removed, and when its
// request ended.
func (j *sweeper) sweep(ctx context.Context, b staleBranch) (time.Time, bool) {
	var (
		p   *forge.PR
		err error
	)

	if j.in.host != nil {
		if p, err = j.request(ctx, b); err != nil {
			j.say("kept %s: %v", b.name, err)

			return time.Time{}, false
		}
	}

	if p == nil {
		return j.sweepUnpushed(ctx, b)
	}

	if p.Status == forge.Open {
		return time.Time{}, false
	}

	if reason := j.keep(ctx, b, p); reason != "" {
		j.say("kept %s: %s", b.name, reason)

		return time.Time{}, false
	}

	return cmp.Or(p.MergedAt, p.ClosedAt), j.remove(ctx, b, p)
}

// request is the request that decides b: an open one when any is, else
// the newest; nil when there is none.
func (j *sweeper) request(ctx context.Context, b staleBranch) (*forge.PR, error) {
	prs, err := j.in.host.PullsByHead(ctx, b.name)
	if err != nil {
		return nil, fmt.Errorf("%s did not list its requests: %w", j.in.host.Name, err)
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

// keep is why a branch whose request merged or closed stays, or "".
func (j *sweeper) keep(ctx context.Context, b staleBranch, p *forge.PR) string {
	switch {
	case b.localSHA != "" && b.localSHA != p.HeadSHA:
		return "its tip is not " + p.URL + "'s head, so it has commits the request lacks"
	case b.originSHA != "" && b.originSHA != p.HeadSHA:
		return "origin's tip is not " + p.URL + "'s head"
	}

	stacked, err := j.in.host.stackedPulls(ctx, b.name)
	if err != nil {
		return "listing the requests based on it failed: " + err.Error()
	}

	if len(stacked) > 0 {
		return stacked[0].url() + " is based on it, and deleting the branch would close it"
	}

	if p.Status != forge.Merged {
		deps, err := dependentsOf(ctx, j.in.main, b.name)
		if err != nil {
			return "reading the branches started --after it failed: " + err.Error()
		}

		if len(deps) > 0 {
			return strings.Join(deps, ", ") + " started --after it, and " + p.URL + " closed without a merge"
		}
	}

	return ""
}

// sweepUnpushed removes a branch finish would read as never pushed once its
// changes are in the default branch; its work ended with its last commit.
func (j *sweeper) sweepUnpushed(ctx context.Context, b staleBranch) (time.Time, bool) {
	if b.localSHA == "" || b.originSHA != "" {
		return time.Time{}, false
	}

	// an upstream git cannot read keeps the branch for this pass
	upstream, err := gitx.Config(ctx, j.in.main, "branch."+b.name+".merge")

	switch {
	case err != nil:
		j.say("%s kept: reading its upstream failed: %v", b.name, err)

		return time.Time{}, false
	case upstream != "":
		return time.Time{}, false
	case !alreadyLanded(ctx, j.in.main, "refs/heads/"+j.def, "refs/heads/"+b.name):
		return time.Time{}, false
	}

	ended, _ := time.Parse(time.RFC3339, gitx.Out(ctx, j.in.main, "log", "-1", "--format=%cI", b.localSHA))

	return ended, j.remove(ctx, b, nil)
}

// remove drops origin's copy only for the user's own request. A step failing
// is a line, and the branch counts as kept.
func (j *sweeper) remove(ctx context.Context, b staleBranch, p *forge.PR) bool {
	why := "never pushed, already in " + j.def
	if p != nil {
		why = p.URL + " " + p.Status
	}

	var gone []string

	dropOrigin := b.originSHA != ""
	if dropOrigin && !j.own(ctx, p) {
		dropOrigin = false

		if b.localSHA == "" {
			return false
		}

		j.say("kept origin/%s: %s was opened by %s", b.name, p.URL, cmp.Or(p.Author, "someone else"))
	}

	if b.localSHA != "" {
		gone = append(gone, "local branch")
	}

	if dropOrigin {
		gone = append(gone, "origin")
	}

	if j.in.dry {
		j.say("would remove %s (%s): %s", b.name, why, strings.Join(gone, ", "))

		return true
	}

	if p != nil && p.Status == forge.Merged {
		released, err := releaseDependents(ctx, j.in.main, b.name, j.def)
		if err != nil {
			j.say("kept %s: %v", b.name, err)

			return false
		}

		j.lines = append(j.lines, released...)
	}

	if dropOrigin {
		if err := j.in.host.deleteRemoteBranch(ctx, b.name); err != nil {
			j.say("kept %s: deleting origin/%s failed: %v", b.name, b.name, err)

			return false
		}
	}

	if dropOrigin {
		if err := gitx.DeleteRemoteRef(ctx, j.in.main, b.name); err != nil {
			j.say("origin/%s is gone but its local copy is not: %v", b.name, err)
		}
	}

	if b.localSHA != "" {
		if err := gitx.DeleteBranch(ctx, j.in.main, b.name); err != nil {
			j.say("kept %s: deleting the local branch failed: %v", b.name, err)

			return false
		}
	}

	j.say("removed %s (%s): %s", b.name, why, strings.Join(gone, ", "))

	return true
}

// own reports whether the signed-in host user opened p. A login the host
// will not tell, or a request with no author, is someone else's.
func (j *sweeper) own(ctx context.Context, p *forge.PR) bool {
	if p == nil || p.Author == "" {
		return false
	}

	if j.login == nil {
		login, err := j.in.host.Login(ctx)
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
func (j *sweeper) archive(branches []staleBranch, removed map[string]time.Time) {
	left := map[string][]string{}
	cleared := map[string]time.Time{}

	for _, b := range branches {
		ended, ok := removed[b.name]

		for _, id := range b.ids {
			if ok {
				cleared[id] = laterOf(cleared[id], ended)
			} else {
				left[id] = append(left[id], b.name)
			}
		}
	}

	ids := make([]string, 0, len(cleared))
	for id := range cleared {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	for _, id := range ids {
		plan := taskPlan(j.in.main, j.in.plansDir, id)

		switch {
		case !exists(plan):
		case len(left[id]) > 0:
			j.say("kept the plan for %s: %s still uses it", id, strings.Join(left[id], ", "))

			continue
		case writtenAfter(plan, cleared[id]):
			j.say("kept the plan for %s: it was written after its branch's work ended", id)

			continue
		case j.in.dry:
			j.say("would archive %s", plan)

			continue
		default:
			archived, _, err := archivePlan(j.in.main, j.in.main, j.in.plansDir, id)
			if err != nil {
				j.say("%v", err)

				continue
			}

			j.say("archived %s to %s", plan, archived)
		}

		if len(left[id]) == 0 && !j.in.dry {
			if note := removeImages(j.in.main, j.in.plansDir, id); note != "" {
				j.say("%s", note)
			}
		}
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
	entries, err := gitx.ConfigEntries(ctx, dir, `^branch\..*\.`+cfgAfterBranch+`$`)
	if err != nil {
		return nil, err
	}

	var deps []string

	for _, e := range entries {
		if e.Value == branch {
			deps = append(deps, strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+cfgAfterBranch))
		}
	}

	return deps, nil
}
