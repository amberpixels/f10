package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/timing"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The stats verb is active time per task and step: every wait on the user is
// out, tool run time stays in. Density is implement minutes per 100 changed
// lines; a task from before waits were recorded has no numbers.
func statsCommand() *cli.Command {
	return &cli.Command{
		Name:      "stats",
		Usage:     "active minutes per task by step group, with size and density; a task id adds its steps",
		ArgsUsage: "[<task-id>]",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "since", Value: "30d", Usage: "only tasks active in the last `N`d or Nw"},
		},
		Action: runStats,
	}
}

func runStats(ctx context.Context, cmd *cli.Command) error {
	now := time.Now()

	cutoff, err := statsCutoff(cmd.String("since"), now)
	if err != nil {
		return ui.Exit(ui.ExitUsage, "stats: --since: %v", err)
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	// resolved before the ledger is read, so 414 matches FT-414 and a typo errors
	var task string
	if arg := strings.TrimSpace(cmd.Args().First()); arg != "" {
		r, err := t.reference(ctx, arg)
		if err != nil {
			return fmt.Errorf("stats: %w", err)
		}

		task = r.ID
	}

	events, err := ledger.ReadEvents(ctx, ledger.Path(), ledger.Filter{Project: ledger.Where(ctx, t.dir).Project})
	if err != nil {
		return err
	}

	rows := statRows(events, task, cutoff)

	if err := writeStats(cmd.Writer, rows); err != nil || task == "" || len(rows) == 0 {
		return err
	}

	fmt.Fprintln(cmd.Writer)

	return writeSteps(cmd.Writer, rows[0])
}

// statRow is one task's numbers; a step absent, or a nil, was never recorded.
type statRow struct {
	task  string
	steps map[string]time.Duration
	// shown is the breakdown's rows: a step, or a labelled entry of one by
	// its label, so two pr entries read as pr and pr-polish
	shown  map[string]shownStep
	lines  *int
	md     int
	latest time.Time
}

type shownStep struct {
	step   string
	active time.Duration
}

// The table's step groups: the review loop, and every other named step.
var reviewLoop = []string{timing.Review, timing.Resolve, timing.Judge}

// group sums the steps the column holds; nil when none was recorded.
func (r statRow) group(column string) *time.Duration {
	var (
		sum   time.Duration
		found bool
	)

	for step, d := range r.steps {
		if stepColumn(step) == column {
			sum, found = sum+d, true
		}
	}

	if !found {
		return nil
	}

	return &sum
}

func stepColumn(step string) string {
	switch {
	case step == timing.Plan, step == timing.Implement, step == timing.Glue:
		return step
	case slices.Contains(reviewLoop, step):
		return "review"
	}

	return "deliver"
}

// density is implement minutes per 100 changed lines, when both exist.
func (r statRow) density() *float64 {
	impl := r.group(timing.Implement)
	if impl == nil || r.lines == nil || *r.lines == 0 {
		return nil
	}

	d := impl.Minutes() * 100 / float64(*r.lines)

	return &d
}

// statRows is a row per task with a window or a size, newest first: a task
// named is shown whatever its age, else only those active since cutoff.
func statRows(events []ledger.Event, task string, cutoff time.Time) []statRow {
	rows := map[string]*statRow{}

	row := func(id string, at time.Time) *statRow {
		r, ok := rows[id]
		if !ok {
			r = &statRow{task: id}
			rows[id] = r
		}

		if at.After(r.latest) {
			r.latest = at
		}

		return r
	}

	for _, w := range timing.Windows(events) {
		if w.Task == "" {
			continue
		}

		r := row(w.Task, w.To)
		if r.steps == nil {
			r.steps, r.shown = map[string]time.Duration{}, map[string]shownStep{}
		}

		r.steps[w.Step] += w.Active

		key := cmp.Or(w.Label, w.Step)
		r.shown[key] = shownStep{step: w.Step, active: r.shown[key].active + w.Active}
	}

	// the latest size stands: a rerun of finish records again
	for _, e := range events {
		if e.Kind != ledger.TaskSized || e.Task == "" {
			continue
		}

		lines, ok := e.Payload.Val.Int("lines")
		if !ok {
			continue
		}

		md, _ := e.Payload.Val.Int("md_lines")
		r := row(e.Task, e.At)
		r.lines, r.md = &lines, md
	}

	var out []statRow

	for _, r := range rows {
		if task != "" && !strings.EqualFold(r.task, task) || task == "" && r.latest.Before(cutoff) {
			continue
		}

		out = append(out, *r)
	}

	slices.SortFunc(out, func(a, b statRow) int {
		return cmp.Or(b.latest.Compare(a.latest), strings.Compare(a.task, b.task))
	})

	return out
}

func writeStats(w io.Writer, rows []statRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no active time recorded")

		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)

	line := func(cells ...string) {
		fmt.Fprintln(tw, strings.Join(cells, "\t")+"\t")
	}

	columns := []string{timing.Plan, timing.Implement, "review", "deliver", timing.Glue}
	line(append(append([]string{"task"}, columns...), "size", "density")...)

	var sizes, densities []float64

	medians := make([][]float64, len(columns))

	for _, r := range rows {
		cells := []string{r.task}
		for i, c := range columns {
			cells = append(cells, minutes(r.group(c), &medians[i]))
		}

		size := "-"
		if r.lines != nil {
			size = fmt.Sprintf("%d (%d%% md)", *r.lines, percent(r.md, *r.lines))
			sizes = append(sizes, float64(*r.lines))
		}

		line(append(cells, size, ratio(r.density(), &densities))...)
	}

	size := "-"
	if m, ok := median(sizes); ok {
		size = fmt.Sprintf("%.0f", m)
	}

	cells := []string{"median"}
	for _, vs := range medians {
		cells = append(cells, medianMinutes(vs))
	}

	line(append(cells, size, medianMinutes(densities))...)

	return tw.Flush()
}

// stepOrder is the breakdown's order: the default flow's, other steps
// alphabetically after it, glue last.
var stepOrder = []string{
	timing.Plan, timing.Judge, timing.Implement, timing.Cleanup, timing.PR, timing.Review, timing.Resolve,
}

// writeSteps is one task's active time per step, with a total.
func writeSteps(w io.Writer, r statRow) error {
	steps := slices.Collect(maps.Keys(r.shown))
	slices.SortFunc(steps, func(a, b string) int {
		rank := func(s string) int {
			if i := slices.Index(stepOrder, s); i >= 0 {
				return i
			}

			if s == timing.Glue {
				return len(stepOrder) + 1
			}

			return len(stepOrder)
		}

		// a step's own row before its labelled entries
		sa, sb := r.shown[a].step, r.shown[b].step
		labelled := func(key, step string) int {
			if key == step {
				return 0
			}

			return 1
		}

		return cmp.Or(cmp.Compare(rank(sa), rank(sb)), strings.Compare(sa, sb),
			cmp.Compare(labelled(a, sa), labelled(b, sb)), strings.Compare(a, b))
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "step\tactive\t")

	var total time.Duration

	for _, s := range steps {
		d := r.shown[s].active
		total += d
		fmt.Fprintf(tw, "%s\t%s\t\n", s, fmtMinutes(d.Minutes()))
	}

	fmt.Fprintf(tw, "total\t%s\t\n", fmtMinutes(total.Minutes()))

	return tw.Flush()
}

// minutes prints a duration in minutes and collects it for the median.
func minutes(d *time.Duration, into *[]float64) string {
	if d == nil {
		return "-"
	}

	*into = append(*into, d.Minutes())

	return fmtMinutes(d.Minutes())
}

func ratio(v *float64, into *[]float64) string {
	if v == nil {
		return "-"
	}

	*into = append(*into, *v)

	return fmtMinutes(*v)
}

func medianMinutes(vs []float64) string {
	m, ok := median(vs)
	if !ok {
		return "-"
	}

	return fmtMinutes(m)
}

// fmtMinutes keeps a decimal only where it matters.
func fmtMinutes(m float64) string {
	if m < 10 {
		return fmt.Sprintf("%.1fm", m)
	}

	return fmt.Sprintf("%.0fm", m)
}

func median(vs []float64) (float64, bool) {
	if len(vs) == 0 {
		return 0, false
	}

	s := slices.Sorted(slices.Values(vs))
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2, true
	}

	return s[len(s)/2], true
}

func percent(part, whole int) int {
	if whole == 0 {
		return 0
	}

	return (part*100 + whole/2) / whole
}

// statsCutoff is where --since starts the table: a count of days or weeks
// back from now.
func statsCutoff(arg string, now time.Time) (time.Time, error) {
	p, err := calendar.Parse(arg, now, calendar.Offsets, nil)
	if err != nil {
		return time.Time{}, err
	}

	return p.Start, nil
}
