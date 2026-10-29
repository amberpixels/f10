package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The catchup step's base and method, decided here so the step, ship's
// guard and the dry run read one answer; settling conflicts stays the step's.
func catchupCommand() *cli.Command {
	return &cli.Command{
		Name:     "catchup",
		Hidden:   true,
		Category: plumbingCategory,
		Usage:    "what a catchup brings in and how",
		Commands: []*cli.Command{
			{
				Name:     "base",
				Hidden:   true,
				Category: plumbingCategory,
				Usage:    "print the current branch's catchup base, how it was found, the method and why, and the mark a conflict sets",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "guard",
						Usage: "decide as ship's guard before this entry: implement, pr or judge",
					},
					&cli.BoolFlag{Name: "no-fetch", Usage: "read the refs at hand instead of fetching origin first"},
				},
				Action: runCatchupBase,
			},
		},
	}
}

// What a base was found through, in the step's words.
const (
	foundLanded  = "f10-landed"
	foundAfter   = "f10-after-branch"
	foundGone    = "f10-after-branch gone"
	foundDefault = "default branch"
)

// Methods besides merge and rebase: none has nothing to bring in, skip is a
// guard with nothing to do.
const (
	methodMerge  = "merge"
	methodRebase = "rebase"
	methodNone   = "none"
	methodSkip   = "skip"
)

type catchupPlan struct {
	branch, base, sha, found, how, method, why, mark string
}

type catchupInput struct {
	dir, main string
	guard     string // "" for a run asked for by name, else the entry it guards

	catchup, stacked               string
	catchupDefault, stackedDefault bool // the value is the default, not declared

	shipStopped bool
}

func runCatchupBase(ctx context.Context, cmd *cli.Command) error {
	guard := cmd.String("guard")
	if guard != "" && !slices.Contains([]string{"implement", "pr", "judge"}, guard) {
		return fmt.Errorf("--guard %q: want implement, pr or judge", guard)
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	in := catchupInput{
		dir:            t.lay.CheckoutRoot,
		main:           cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		guard:          guard,
		catchup:        t.eff.Catchup,
		stacked:        t.eff.CatchupStacked,
		catchupDefault: factDefault(t.eff, "catchup"),
		stackedDefault: factDefault(t.eff, "catchup_stacked"),
		shipStopped:    shipStopped(),
	}

	if !cmd.Bool("no-fetch") {
		if err := gitx.Fetch(ctx, in.main); err != nil {
			fmt.Fprintf(cmd.ErrWriter, "f10: fetching origin failed, deciding on the refs at hand: %v\n", err)
		}
	}

	p, err := resolveCatchup(ctx, in)
	if err != nil {
		return err
	}

	return p.write(cmd.Writer)
}

func factDefault(eff *facts.Effective, key string) bool {
	for _, f := range eff.Facts {
		if f.Key == key {
			return f.Origin == facts.OriginDefault
		}
	}

	return true
}

// shipStopped reads this session's ship status; no session or no run file
// is a ship that never stopped.
func shipStopped() bool {
	session := state.Session()
	if session == "" {
		return false
	}

	r, err := state.Load(state.Path(state.Dir(), session))
	if err != nil || r == nil {
		return false
	}

	s := r.Status("ship")

	return s == "failed" || s == "partial"
}

func resolveCatchup(ctx context.Context, in catchupInput) (catchupPlan, error) {
	branch := gitx.CurrentBranch(ctx, in.dir)
	if branch == "" {
		return catchupPlan{}, fmt.Errorf("HEAD is detached in %s: a catchup needs a branch", in.dir)
	}

	p := catchupPlan{branch: branch}
	def := cmp.Or(strings.TrimPrefix(defaultBranch(ctx, in.main), "origin/"), "main")

	switch {
	case in.guard != "" && in.catchup == "off":
		p.skip("catchup: off")
		return p, nil
	case in.guard != "" && branch == def:
		p.skip("on the default branch")
		return p, nil
	}

	if err := p.findBase(ctx, in, def); err != nil || p.method == methodSkip {
		return p, err
	}

	inBranch, err := gitx.IsAncestor(ctx, in.dir, p.sha, "HEAD")
	if err != nil {
		return catchupPlan{}, fmt.Errorf("checking whether %s is in %s: %w", p.base, branch, err)
	}

	if inBranch {
		p.method, p.why, p.mark = methodNone, "base already in the branch", ""
		return p, nil
	}

	onOrigin, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+branch)
	if err != nil {
		return catchupPlan{}, fmt.Errorf("reading origin/%s: %w", branch, err)
	}

	p.method, p.why = in.method(onOrigin != "", p.found == foundAfter)

	return p, nil
}

func (p *catchupPlan) skip(why string) {
	p.method, p.why = methodSkip, why
}

// findBase takes f10-landed, then the recorded branch, then the default. The
// guard before pr or judge reads only origin, so no unpushed commits ride in.
func (p *catchupPlan) findBase(ctx context.Context, in catchupInput, def string) error {
	fromOrigin := in.guard == "pr" || in.guard == "judge"

	landed, err := gitx.Config(ctx, in.main, "branch."+p.branch+"."+cfgLanded)
	if err != nil {
		return err
	}

	if landed != "" {
		p.found, p.mark = foundLanded, cfgLanded+" "+def

		if fromOrigin {
			return p.originBase(ctx, in.main, def, "f10-landed: the default branch as origin holds it")
		}

		sha, name, err := defaultTarget(ctx, in.main)
		if err != nil {
			return err
		}

		p.base, p.sha = name, sha
		p.how = "f10-landed: the default branch, local when it holds origin's"

		return nil
	}

	after, found, err := recordedBase(ctx, in.main, p.branch)
	if err != nil {
		return err
	}

	switch {
	case after != "" && found:
		return p.afterBase(ctx, in.main, after, fromOrigin)
	case after != "":
		p.found = foundGone
		p.how = "f10-after-branch " + after + " exists nowhere: its task finished, so the default branch"
	case in.guard == "implement":
		p.skip("no recorded base")
		return nil
	default:
		p.found, p.how = foundDefault, "nothing recorded: the default branch"
	}

	p.mark = cfgLanded + " " + def

	sha, err := gitx.Ref(ctx, in.main, "refs/remotes/origin/"+def)
	if err != nil || sha != "" {
		p.base, p.sha = "origin/"+def, sha
		return err
	}

	if fromOrigin {
		return fmt.Errorf("origin/%s is missing: the PR's target cannot be read", def)
	}

	sha, err = gitx.Ref(ctx, in.main, "refs/heads/"+def)
	if err == nil && sha == "" {
		err = fmt.Errorf("cannot read %s here, locally or on origin", def)
	}

	p.base, p.sha = def, sha

	return err
}

func (p *catchupPlan) originBase(ctx context.Context, main, name, how string) error {
	sha, err := gitx.Ref(ctx, main, "refs/remotes/origin/"+name)
	if err != nil {
		return err
	}

	if sha == "" {
		return fmt.Errorf("origin/%s is missing: the PR's target cannot be read", name)
	}

	p.base, p.sha, p.how = "origin/"+name, sha, how

	return nil
}

// afterBase is the recorded branch, local first: a catchup by name takes the
// base task's newest work, pushed or not.
func (p *catchupPlan) afterBase(ctx context.Context, main, after string, fromOrigin bool) error {
	p.found = foundAfter

	remote, err := gitx.Ref(ctx, main, "refs/remotes/origin/"+after)
	if err != nil {
		return err
	}

	local, err := gitx.Ref(ctx, main, "refs/heads/"+after)
	if err != nil {
		return err
	}

	switch {
	case fromOrigin && remote == "":
		p.skip("base " + after + " is not on origin yet")
		return nil
	case fromOrigin:
		p.base, p.sha, p.how = "origin/"+after, remote, "f10-after-branch: "+after+" as origin holds it"
	case local != "":
		p.base, p.sha, p.how = after, local, "f10-after-branch: "+after
	default:
		p.base, p.sha, p.how = "origin/"+after, remote, "f10-after-branch: "+after+", on origin only"
	}

	p.mark = cfgMoved + " " + p.sha

	return nil
}

func (in catchupInput) method(onOrigin, stacked bool) (string, string) {
	switch {
	case in.guard != "" && onOrigin:
		return methodMerge, "the branch is on origin, so the push after it never forces"
	case onOrigin && in.shipStopped:
		return methodMerge, "the branch is on origin and its ship stopped: ship resumes after it, and its push never forces"
	case stacked && in.stackedDefault:
		return methodMerge, "catchup_stacked not declared: merge by default, since rebasing onto a branch that " +
			"later rebases or squash-merges replays its old commits"
	case stacked:
		return in.stacked, "catchup_stacked: " + in.stacked
	case in.catchup == "off":
		return methodRebase, "catchup: off covers only ship's guard: rebase by default"
	case in.catchupDefault:
		return methodRebase, "catchup not declared: rebase by default"
	}

	return in.catchup, "catchup: " + in.catchup
}

func (p *catchupPlan) write(w io.Writer) error {
	var b strings.Builder

	for _, row := range [][2]string{
		{"branch", p.branch},
		{"base", p.base},
		{"sha", p.sha},
		{"found", p.found},
		{"how", p.how},
		{"method", p.method},
		{"why", p.why},
		{"mark", p.mark},
	} {
		if row[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", row[0], row[1])
		}
	}

	_, err := io.WriteString(w, b.String())

	return err
}

// recordedBase is the f10-after-branch name and whether it exists, locally
// or on origin; a recorded base found nowhere was finished and deleted.
func recordedBase(ctx context.Context, main, branch string) (string, bool, error) {
	after, err := gitx.Config(ctx, main, "branch."+branch+"."+cfgAfterBranch)
	if err != nil || after == "" {
		return "", false, err
	}

	for _, ref := range []string{"refs/heads/" + after, "refs/remotes/origin/" + after} {
		sha, err := gitx.Ref(ctx, main, ref)
		if err != nil {
			return after, false, err
		}

		if sha != "" {
			return after, true, nil
		}
	}

	return after, false, nil
}
