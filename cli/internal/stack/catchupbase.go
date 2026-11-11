package stack

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// What a base was found through, in the step's words.
const (
	FoundLanded  = string(branchmeta.Landed)
	FoundAfter   = string(branchmeta.AfterBranch)
	FoundGone    = string(branchmeta.AfterBranch) + " gone"
	FoundDefault = "default branch"
)

// The code row's values.
const (
	CodeYes  = "yes"
	CodeNone = "none"
)

// Methods besides merge and rebase: none has nothing to bring in, skip is a
// guard with nothing to do.
const (
	MethodMerge  = "merge"
	MethodRebase = "rebase"
	MethodOnto   = "onto"
	MethodNone   = "none"
	MethodSkip   = "skip"
)

type CatchupPlan struct {
	Branch, Base, SHA, Found, How, Method, Why, Mark string
	// Upstream is the base's old tip that onto rebases from
	Upstream string
	// Code says whether a recorded base branch has commits of its own: ship
	// waits on one with none, by the rule the drive releases dependents on
	Code string
}

func (p *CatchupPlan) Write(w io.Writer) error {
	var b strings.Builder

	for _, row := range [][2]string{
		{"branch", p.Branch},
		{"base", p.Base},
		{"sha", p.SHA},
		{"upstream", p.Upstream},
		{"found", p.Found},
		{"how", p.How},
		{"method", p.Method},
		{"why", p.Why},
		{"mark", p.Mark},
		{"code", p.Code},
	} {
		if row[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", row[0], row[1])
		}
	}

	_, err := io.WriteString(w, b.String())

	return err
}

func (p *CatchupPlan) skip(why string) {
	p.Method, p.Why = MethodSkip, why
}

// findBase takes f10-landed, then the recorded branch, then the default. The
// guard before pr or judge reads only origin, so no unpushed commits ride in.
func (p *CatchupPlan) findBase(ctx context.Context, in CatchupInput, def string) error {
	fromOrigin := in.Guard == "pr" || in.Guard == "judge"

	landed, err := branchmeta.Get(ctx, in.Main, p.Branch, branchmeta.Landed)
	if err != nil {
		return err
	}

	if landed != "" {
		p.Found, p.Mark = FoundLanded, string(branchmeta.Landed)+" "+def

		if fromOrigin {
			return p.originBase(ctx, in.Main, def, "f10-landed: the default branch as origin holds it")
		}

		sha, name, err := DefaultTargetOf(ctx, in.Main, def)
		if err != nil {
			return err
		}

		p.Base, p.SHA = name, sha
		p.How = "f10-landed: the default branch, local when it holds origin's"

		return nil
	}

	after, sha, err := RecordedBase(ctx, in.Main, p.Branch)
	if err != nil {
		return err
	}

	switch {
	case after != "" && sha != "":
		return p.afterBase(ctx, in.Main, after, def, fromOrigin)
	case after != "":
		p.Found = FoundGone
		p.How = "f10-after-branch " + after + " exists nowhere: its task finished, so the default branch"
	case in.Guard == "implement":
		p.skip("no recorded base")
		return nil
	default:
		p.Found, p.How = FoundDefault, "nothing recorded: the default branch"
	}

	p.Mark = string(branchmeta.Landed) + " " + def

	if fromOrigin {
		return p.originBase(ctx, in.Main, def, p.How)
	}

	p.SHA, p.Base, err = DefaultTip(ctx, in.Main, def, originFirst)

	return err
}

func (p *CatchupPlan) originBase(ctx context.Context, main, name, how string) error {
	sha, err := gitx.Ref(ctx, main, "refs/remotes/origin/"+name)
	if err != nil {
		return err
	}

	if sha == "" {
		return fmt.Errorf("origin/%s is missing: the PR's target cannot be read", name)
	}

	p.Base, p.SHA, p.How = "origin/"+name, sha, how

	return nil
}

// afterBase is the recorded branch, local first: a catchup by name takes the
// base task's newest work, pushed or not, unless f10-base-moved names a sha.
func (p *CatchupPlan) afterBase(ctx context.Context, main, after, def string, fromOrigin bool) error {
	p.Found = FoundAfter

	remote, err := gitx.Ref(ctx, main, "refs/remotes/origin/"+after)
	if err != nil {
		return err
	}

	local, err := gitx.Ref(ctx, main, "refs/heads/"+after)
	if err != nil {
		return err
	}

	p.Code = CodeNone

	switch {
	case local != "" && BaseHasCode(ctx, main, after, def):
		p.Code = CodeYes
	case local == "" && OriginBaseHasCode(ctx, main, after, def):
		p.Code = CodeYes
	}

	moved := ""
	if !fromOrigin {
		if moved, err = movedMark(ctx, main, p.Branch, after); err != nil {
			return err
		}
	}

	switch {
	case fromOrigin && remote == "":
		p.skip("base " + after + " is not on origin yet")
		return nil
	case fromOrigin:
		p.Base, p.SHA, p.How = "origin/"+after, remote, "f10-after-branch: "+after+" as origin holds it"
	case moved != "":
		// the drive and the watcher gate the push on this sha, so a newer
		// local tip would carry commits neither checked
		p.Base, p.SHA = moved, moved
		p.How = "f10-base-moved: " + gitx.ShortSHA(moved) + " of " + after + ", the commit the mark names"
	case local != "":
		p.Base, p.SHA, p.How = after, local, "f10-after-branch: "+after
	default:
		p.Base, p.SHA, p.How = "origin/"+after, remote, "f10-after-branch: "+after+", on origin only"
	}

	p.Mark = string(branchmeta.Moved) + " " + p.SHA

	return nil
}

type CatchupInput struct {
	Dir, Main string
	Guard     string // "" for a run asked for by name, else the entry it guards

	Catchup, Stacked               string
	CatchupDefault, StackedDefault bool // the value is the default, not declared

	ShipStopped bool

	// Fetched is the tick's or the watcher pass's fetch, nil outside one
	Fetched *FetchOnce
}

// rewritten is the base's old tip when its history was rewritten: onto
// drops the old commits the new base holds in another shape.
func (in CatchupInput) method(onOrigin, stacked bool, rewritten string) (string, string) {
	switch {
	case in.Guard != "" && onOrigin:
		return MethodMerge, "the branch is on origin, so the push after it never forces"
	case rewritten != "":
		return MethodOnto, in.ontoWhy(stacked, rewritten)
	case onOrigin && in.ShipStopped:
		return MethodMerge, "the branch is on origin and its ship stopped: ship resumes after it, and its push never forces"
	case stacked && in.StackedDefault:
		return MethodMerge, "catchup_stacked not declared: merge by default, since rebasing onto a branch that " +
			"later rebases or squash-merges replays its old commits"
	case stacked:
		return in.Stacked, "catchup_stacked: " + in.Stacked
	case in.Catchup == "off":
		return MethodRebase, "catchup: off covers only ship's guard: rebase by default"
	case in.CatchupDefault:
		return MethodRebase, "catchup not declared: rebase by default"
	}

	return in.Catchup, "catchup: " + in.Catchup
}

// ontoWhy names the declared key onto overrides, so the report can say so.
func (in CatchupInput) ontoWhy(stacked bool, rewritten string) string {
	why := "base rewritten since " + gitx.ShortSHA(rewritten) + ": its old commits are dropped, " +
		"and the next push needs --force-with-lease when the branch is on origin"

	switch {
	case stacked && !in.StackedDefault:
		why += "; overrides catchup_stacked: " + in.Stacked
	case !stacked && !in.CatchupDefault:
		why += "; overrides catchup: " + in.Catchup
	}

	return why
}

// CatchupPolicy is the catchup keys of the effective configuration.
func CatchupPolicy(eff *facts.Effective) CatchupInput {
	return CatchupInput{
		Catchup:        eff.Catchup,
		Stacked:        eff.CatchupStacked,
		CatchupDefault: factDefault(eff, "catchup"),
		StackedDefault: factDefault(eff, "catchup_stacked"),
	}
}

func factDefault(eff *facts.Effective, key string) bool {
	for _, f := range eff.Facts {
		if f.Key == key {
			return f.Origin.Kind == facts.OriginDefault
		}
	}

	return true
}

func ResolveCatchup(ctx context.Context, in CatchupInput) (CatchupPlan, error) {
	branch := gitx.CurrentBranch(ctx, in.Dir)
	if branch == "" {
		return CatchupPlan{}, fmt.Errorf("HEAD is detached in %s: a catchup needs a branch", in.Dir)
	}

	p := CatchupPlan{Branch: branch}

	// no silent "main": pr base and the drive fail the same way without it
	def, err := LocalDefault(ctx, in.Main)
	if err != nil {
		return CatchupPlan{}, err
	}

	if in.Guard != "" && branch == def {
		p.skip("on the default branch")
		return p, nil
	}

	if err := p.findBase(ctx, in, def); err != nil || p.Method == MethodSkip {
		return p, err
	}

	// the base still prints: ship asks about a base with no code even when off
	if in.Guard != "" && in.Catchup == "off" {
		p.Mark = ""
		p.skip("catchup: off")

		return p, nil
	}

	inBranch, err := gitx.IsAncestor(ctx, in.Dir, p.SHA, "HEAD")
	if err != nil {
		return CatchupPlan{}, fmt.Errorf("checking whether %s is in %s: %w", p.Base, branch, err)
	}

	if inBranch {
		p.Method, p.Why, p.Mark = MethodNone, "base already in the branch", ""
		return p, nil
	}

	onOrigin, err := gitx.Ref(ctx, in.Main, "refs/remotes/origin/"+branch)
	if err != nil {
		return CatchupPlan{}, fmt.Errorf("reading origin/%s: %w", branch, err)
	}

	rewritten, err := baseRewritten(ctx, in, p)
	if err != nil {
		return CatchupPlan{}, err
	}

	p.Method, p.Why = in.method(onOrigin != "", p.Found == FoundAfter, rewritten)
	if p.Method == MethodOnto {
		p.Upstream = rewritten
	}

	return p, nil
}

// RecordTip records the base commit branch is built on, so a catchup after
// the base's history is rewritten replays only branch's own commits. The
// newer of the local and origin merge-bases wins: a guard integrates origin's
// copy, which a stale local branch would hide. A base found nowhere keeps the
// tip recorded before.
func RecordTip(ctx context.Context, dir, branch, base string) error {
	tip, err := NewerMergeBase(ctx, dir, "refs/heads/"+branch, base)
	if err != nil || tip == "" {
		return err
	}

	if err := branchmeta.Set(ctx, dir, branch, branchmeta.Tip, tip); err != nil {
		return fmt.Errorf("recording %s's base tip on %s: %w", branch, base, err)
	}

	return nil
}

// baseRewritten is the recorded base tip when the branch holds it and the
// base no longer does (squash-merged or rebased), else "".
func baseRewritten(ctx context.Context, in CatchupInput, p CatchupPlan) (string, error) {
	tip, err := branchmeta.Get(ctx, in.Main, p.Branch, branchmeta.Tip)
	if err != nil || tip == "" {
		return "", err
	}

	held, err := gitx.IsAncestor(ctx, in.Dir, tip, "HEAD")
	if err != nil || !held {
		return "", err
	}

	kept, err := gitx.IsAncestor(ctx, in.Dir, tip, p.SHA)
	if err != nil || kept {
		return "", err
	}

	return tip, nil
}

// movedMark is the commit f10-base-moved names on branch, "" when unset. A
// mark naming no commit, or one the base no longer reaches (rewritten since),
// fails rather than falling back to a branch tip or bringing in old commits.
func movedMark(ctx context.Context, main, branch, after string) (string, error) {
	key := branchmeta.Moved.For(branch)

	moved, err := branchmeta.Get(ctx, main, branch, branchmeta.Moved)
	if err != nil || moved == "" {
		return "", err
	}

	sha, err := gitx.Ref(ctx, main, moved+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("reading the %s mark %s: %w", branchmeta.Moved, moved, err)
	}

	if sha == "" {
		return "", fmt.Errorf("%s names %s, no commit here: unset %s to catch up with the base branch",
			branchmeta.Moved, moved, key)
	}

	for _, ref := range []string{"refs/heads/" + after, "refs/remotes/origin/" + after} {
		held, err := gitx.IsAncestor(ctx, main, sha, ref)
		if err != nil {
			return "", fmt.Errorf("checking whether %s holds the %s mark: %w", after, branchmeta.Moved, err)
		}

		if held {
			return sha, nil
		}
	}

	return "", fmt.Errorf("%s names %s, which %s no longer holds: unset %s to catch up with the base branch",
		branchmeta.Moved, gitx.ShortSHA(sha), after, key)
}
