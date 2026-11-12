package pr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// prChecks is what --json prints: the request and its head's checks, and
// with --triage what a triage decides by.
type prChecks struct {
	forge.HeadChecks

	PR     string       `json:"pr"`
	Triage *checkTriage `json:"triage,omitempty"`
}

// checkTriage is the facts the ci step decides by.
type checkTriage struct {
	First   *forge.Check `json:"first,omitempty"`
	Default string       `json:"default"` // the first failure's job on the default branch: green, red or ""
	Tried   []string     `json:"tried,omitempty"`
}

// runPRChecks always asks the host: a result belongs to one head, so no
// copy is kept to read instead. A green read ends the branch's red spell.
func runPRChecks(ctx context.Context, cmd *cli.Command) error {
	record, rerun := strings.TrimSpace(cmd.String("record")), cmd.Bool("rerun")
	if record != "" && rerun {
		return errors.New("--rerun and --record are one action each: pass one")
	}

	if record != "" && record != branchmeta.TriedFix {
		return fmt.Errorf("--record takes %s; a rerun records itself through --rerun", branchmeta.TriedFix)
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	s, err := reviews.ResolvePRSubject(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if s.LookupErr != nil {
		return s.LookupErr
	}

	if s.Number == 0 {
		return fmt.Errorf("no open PR for %s", s.Branch)
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	p, err := h.PR(ctx, strconv.Itoa(s.Number))
	if err != nil {
		return err
	}

	hc, err := h.HeadChecks(ctx, p)
	if err != nil {
		return err
	}

	// the record lives on a local branch; a PR read from elsewhere keeps none
	local := stack.RefSHA(ctx, s.Main, "refs/heads/"+s.Branch) != ""

	if local {
		if err := branchmeta.ClearOnGreen(ctx, s.Main, s.Branch, hc.State == forge.Green); err != nil {
			return err
		}
	}

	if record != "" || rerun {
		return actOnChecks(ctx, cmd.Writer, h, hc, s.Main, s.Branch, local, rerun)
	}

	if cmd.Bool("log") {
		return printFailedLog(ctx, cmd.Writer, h, hc)
	}

	out := prChecks{PR: p.URL, HeadChecks: hc}

	if cmd.Bool("triage") && hc.State == forge.Red {
		if out.Triage, err = readTriage(ctx, h, hc, s.Main, s.Branch, local); err != nil {
			return err
		}
	}

	switch {
	case cmd.Bool("json"):
		err = ui.JSON(cmd.Writer, out)
	case cmd.Bool("brief"):
		_, err = fmt.Fprintln(cmd.Writer, ChecksLine(hc))
	default:
		rows := append(ChecksFacts(p.URL, hc), TriageFacts(out.Triage)...)
		err = render.WriteFacts(cmd.Writer, rows, render.LabelWidth(rows), ui.Width(cmd.Writer))
	}

	if err != nil {
		return err
	}

	return checksExit(hc.State)
}

// actOnChecks records a fix, or reruns the failed jobs, on a red head. The
// record refuses a second action of a kind, so a triage asks instead.
func actOnChecks(
	ctx context.Context, w io.Writer, h hostops.Host, hc forge.HeadChecks, dir, branch string, local, rerun bool,
) error {
	if hc.State != forge.Red {
		return fmt.Errorf(
			"the checks on %s are %s, not red: nothing to triage",
			gitx.ShortSHA(hc.SHA),
			stateWord(hc.State),
		)
	}

	if !local {
		return fmt.Errorf("%s is not a branch here: a triage runs in the task's worktree", branch)
	}

	action := branchmeta.TriedFix
	if rerun {
		action = branchmeta.TriedRerun
	}

	tr, err := branchmeta.ReadTriage(ctx, dir, branch)
	if err != nil {
		return err
	}

	if !tr.Allows(hc.SHA, action) {
		return branchmeta.RecordTriage(ctx, dir, branch, hc.SHA, action) // its refusal names why
	}

	if rerun {
		runs, err := h.Rerun(ctx, hc.Failed)
		if err != nil {
			return err
		}

		if len(runs) == 0 {
			return fmt.Errorf("no failed job at %s names a run to rerun: rerun it on the host", gitx.ShortSHA(hc.SHA))
		}

		fmt.Fprintf(w, "reran the failed jobs of %s at %s\n", strings.Join(runs, ", "), gitx.ShortSHA(hc.SHA))
	}

	if err := branchmeta.RecordTriage(ctx, dir, branch, hc.SHA, action); err != nil {
		return err
	}

	if !rerun {
		fmt.Fprintf(w, "recorded a fix round on %s\n", gitx.ShortSHA(hc.SHA))
	}

	return nil
}

// logTail is how many lines of a failed job's log --log prints; a CI log
// runs to thousands, and the failure is at its end.
const logTail = 300

func printFailedLog(ctx context.Context, w io.Writer, h hostops.Host, hc forge.HeadChecks) error {
	first, ok := hc.First()
	if !ok {
		return fmt.Errorf("the checks on %s are %s: no failed job to read", gitx.ShortSHA(hc.SHA), stateWord(hc.State))
	}

	log, err := h.FailedLog(ctx, first)
	if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	if cut := len(lines) - logTail; cut > 0 {
		lines = lines[cut:]
		fmt.Fprintf(
			w,
			"%s: the last %d lines of %d; the whole log is at %s\n",
			first.Name,
			logTail,
			cut+logTail,
			first.URL,
		)
	}

	_, err = fmt.Fprintln(w, strings.Join(lines, "\n"))

	return err
}

func readTriage(
	ctx context.Context,
	h hostops.Host,
	hc forge.HeadChecks,
	dir, branch string,
	local bool,
) (*checkTriage, error) {
	out := &checkTriage{}

	first, ok := hc.First()
	if !ok {
		return out, nil
	}

	out.First = &first

	def, err := h.DefaultJob(ctx, first.Name)
	if err != nil {
		return nil, fmt.Errorf("reading %s on the default branch: %w", first.Name, err)
	}

	out.Default = def

	if local {
		tr, err := branchmeta.ReadTriage(ctx, dir, branch)
		if err != nil {
			return nil, err
		}

		out.Tried = tr.Lines()
	}

	return out, nil
}

// ChecksLine is the one-line form ship reads at each entry: `checks: red
// (lint, test)`.
func ChecksLine(hc forge.HeadChecks) string {
	line := "checks: " + stateWord(hc.State)

	switch {
	case hc.State == forge.Red:
		line += " (" + forge.Names(hc.Failed) + ")"
	case hc.State == forge.Pending && len(hc.Running) > 0:
		line += " (" + forge.Names(hc.Running) + ")"
	}

	return line
}

// ChecksFacts is the block pr checks prints: the request, its head, the
// state and the set counted, then a row per failed job with its log.
func ChecksFacts(url string, hc forge.HeadChecks) []render.Fact {
	rows := []render.Fact{{Label: "pr", Value: url, URL: true}, {Label: "head", Value: hc.SHA}}

	switch {
	case hc.State == "":
		rows = append(rows, render.Fact{Label: "checks", Value: "none"})
	case hc.Required:
		rows = append(rows, render.Fact{Label: "checks", Value: hc.State + " (required)"})
	default:
		rows = append(rows, render.Fact{Label: "checks", Value: hc.State + " (all)"})
	}

	for _, c := range hc.Failed {
		label := "failed " + c.Name
		if c.Cancelled {
			label = "cancelled " + c.Name
		}

		rows = append(rows, render.Fact{Label: label, Value: c.URL, URL: c.URL != ""})
	}

	if names := forge.Names(hc.Running); names != "" {
		rows = append(rows, render.Fact{Label: "running", Value: names})
	}

	return rows
}

// TriageFacts are the rows --triage adds: the failure to read, how its
// job last ended on the default branch, and what this red spell tried.
func TriageFacts(tr *checkTriage) []render.Fact {
	if tr == nil || tr.First == nil {
		return nil
	}

	first := tr.First.Name
	if tr.First.Run != "" {
		first += " (run " + tr.First.Run + ")"
	}

	return []render.Fact{
		{Label: "first", Value: first},
		{Label: "default", Value: stateWord(tr.Default)},
		{Label: "tried", Value: orNothing(strings.Join(tr.Tried, ", "))},
	}
}

func stateWord(state string) string {
	if state == "" {
		return "none"
	}

	return state
}

func orNothing(s string) string {
	if s == "" {
		return "nothing"
	}

	return s
}

func checksExit(state string) error {
	switch state {
	case forge.Red:
		return cli.Exit("", 1)
	case forge.Pending:
		return cli.Exit("", ui.ExitPending)
	}

	return nil
}
