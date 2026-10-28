package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// The stats verb is how long planning and implementing took, per task, in
// the run's own time: every wait on the user is out, tool run time stays
// in. Size is what finish measured; density is implement minutes per 100
// changed lines. It reads the ledger, so counting starts with the release
// that records waits.
func statsCommand() *cli.Command {
	return &cli.Command{
		Name:      "stats",
		Usage:     "active plan and implement minutes per task, with size and density",
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

	return writeStats(cmd.Writer, statRows(events, strings.TrimSpace(cmd.Args().First()), time.Now().Add(-since)))
}

// statRow is one task's numbers; a nil is a value never recorded.
type statRow struct {
	task      string
	plan      *time.Duration
	implement *time.Duration
	lines     *int
	md        int
	latest    time.Time
}

// density is implement minutes per 100 changed lines, when both exist.
func (r statRow) density() *float64 {
	if r.implement == nil || r.lines == nil || *r.lines == 0 {
		return nil
	}

	d := r.implement.Minutes() * 100 / float64(*r.lines)

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

	add := func(sum **time.Duration, d time.Duration) {
		if *sum == nil {
			*sum = new(time.Duration)
		}

		**sum += d
	}

	for _, w := range timing.Windows(events) {
		if w.Task == "" {
			continue
		}

		r := row(w.Task, w.To)
		if w.Step == timing.Plan {
			add(&r.plan, w.Active)
		} else {
			add(&r.implement, w.Active)
		}
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
		_, err := fmt.Fprintln(w, "no plan or implement time recorded")

		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)

	line := func(cells ...string) {
		fmt.Fprintln(tw, strings.Join(cells, "\t")+"\t")
	}

	line("task", "plan", "implement", "size", "density")

	var plans, impls, sizes, densities []float64

	for _, r := range rows {
		size := "-"
		if r.lines != nil {
			size = fmt.Sprintf("%d (%d%% md)", *r.lines, percent(r.md, *r.lines))
			sizes = append(sizes, float64(*r.lines))
		}

		line(r.task, minutes(r.plan, &plans), minutes(r.implement, &impls), size, ratio(r.density(), &densities))
	}

	size := "-"
	if m, ok := median(sizes); ok {
		size = fmt.Sprintf("%.0f", m)
	}

	line("median", medianMinutes(plans), medianMinutes(impls), size, medianMinutes(densities))

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
