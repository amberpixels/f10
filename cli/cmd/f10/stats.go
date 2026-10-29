package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/timing"
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
	since, err := facts.ParseStaleAfter(cmd.String("since"))
	if err != nil {
		return cli.Exit("f10 stats: --since: "+err.Error(), 2)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}

	_ = ledger.Sync(ctx) // a failed replay still reads what the database holds

	l, err := ledger.OpenRead(ctx, ledger.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing recorded yet
	}

	if err != nil {
		return err
	}

	defer l.Close()

	events, err := l.List(ctx, ledger.Filter{Project: ledger.Where(ctx, cwd).Project})
	if err != nil {
		return err
	}

	task := strings.TrimSpace(cmd.Args().First())
	rows := statRows(events, task, time.Now().Add(-since))

	if err := writeStats(cmd.Writer, rows); err != nil || task == "" || len(rows) == 0 {
		return err
	}

	fmt.Fprintln(cmd.Writer)

	return writeSteps(cmd.Writer, rows[0])
}

// statRow is one task's numbers; a step absent, or a nil, was never recorded.
type statRow struct {
	task   string
	steps  map[string]time.Duration
	lines  *int
	md     int
	latest time.Time
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
			r.steps = map[string]time.Duration{}
		}

		r.steps[w.Step] += w.Active
	}

	// the latest size stands: a rerun of finish records again
	for _, e := range events {
		if e.Kind != ledger.TaskSized || e.Task == "" {
			continue
		}

		lines, ok := count(e.Payload.Val["lines"])
		if !ok {
			continue
		}

		md, _ := count(e.Payload.Val["md_lines"])
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
	steps := slices.Collect(maps.Keys(r.steps))
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

		return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a, b))
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "step\tactive\t")

	var total time.Duration

	for _, s := range steps {
		total += r.steps[s]
		fmt.Fprintf(tw, "%s\t%s\t\n", s, fmtMinutes(r.steps[s].Minutes()))
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

// count reads a payload number: float64 once stored as JSON, int as built.
func count(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}

	return 0, false
}

func percent(part, whole int) int {
	if whole == 0 {
		return 0
	}

	return (part*100 + whole/2) / whole
}
