// Package audit is the f10 audit command: the map an audit partitions and
// briefs its reviewers from, churn, tasks and pain per area over a range.
package audit

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/history"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/pain"
	"github.com/amberpixels/f10/cli/internal/regression"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// AllCommits is how far back `all` reads, because the full history would
// crowd out the code.
const AllCommits = 300

// NoLedger is the pain block when the ledger holds nothing for the range.
const NoLedger = "No ledger history for this range."

// Command is the audit noun.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "audit",
		Usage: "the map an audit is partitioned from",
		Commands: []*cli.Command{
			{
				Name: "map",
				Usage: "churn, tasks and pain per area over a range: review rounds, halts, replans, " +
					"recurring fixed findings, regressions and implement time against the project",
				ArgsUsage: "<range | all>",
				Flags:     []cli.Flag{ui.JSONFlag("emit the whole map as JSON, uncapped")},
				Action:    runMap,
			},
		},
	}
}

// Map is what the command prints.
type Map struct {
	Range  string    `json:"range"`
	Churn  []Churn   `json:"churn"`
	Tasks  []Task    `json:"tasks"`
	Upkeep []string  `json:"upkeep"` // commits naming no task
	Pain   *pain.Map `json:"pain"`
	Ledger bool      `json:"ledger"` // the ledger held rows for the range's tasks
}

// Churn is one area's lines over the range.
type Churn struct {
	Dir     string `json:"dir"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Commits int    `json:"commits"`
}

// Task is one task of the range: its commits and the areas they touched.
type Task struct {
	ID      string   `json:"id"`
	Commits []string `json:"commits"`
	Areas   []string `json:"areas"`
}

func runMap(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return ui.Exit(ui.ExitUsage, "audit map: name one <range>, or all")
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	top, err := gitx.Toplevel(ctx, t.Dir)
	if err != nil {
		return fmt.Errorf("audit map needs a git repository: %w", err)
	}

	commits, err := Commits(ctx, top, t.Effective.IDPrefix, cmd.Args().First())
	if err != nil {
		return err
	}

	m := Build(cmd.Args().First(), commits)

	var events []ledger.Event
	if len(m.Tasks) > 0 {
		ids := make([]string, len(m.Tasks))
		for i, task := range m.Tasks {
			ids[i] = task.ID
		}

		if events, err = history.ReadLedger(ctx, ledger.Path(), ledger.Where(ctx, t.Dir).Project, ids); err != nil {
			return err
		}
	}

	m.Ledger = slices.ContainsFunc(events, func(e ledger.Event) bool { return onTasks(e, m.Tasks) })
	m.Pain = pain.Build(pain.Input{Areas: areaTasks(m.Tasks), Events: events, Since: oldest(commits)})

	if cmd.Bool("json") {
		return ui.JSON(cmd.Writer, m)
	}

	return m.Write(cmd.Writer)
}

// Commit is one commit of the range, with the task it maps to.
type Commit struct {
	SHA   string
	Task  string // "" for upkeep
	At    time.Time
	Files []FileStat
}

// FileStat is one file's numstat line.
type FileStat struct {
	Path           string
	Added, Deleted int
}

const (
	recordSep = "\x1e"
	fieldSep  = "\x1f"
)

// Commits reads the range in one git log: a commit's task is its Task: or
// Change: trailer, else what regression.CommitTask finds, so audit, history
// and attribution agree on which task owns a commit.
func Commits(ctx context.Context, dir, prefix, rng string) ([]Commit, error) {
	args := []string{
		"log", "--no-renames", "--no-merges", "--numstat",
		"--format=" + recordSep + "%H" + fieldSep + "%aI" + fieldSep + "%s" + fieldSep +
			"%(trailers:only,unfold)" + fieldSep,
	}

	revs := []string{rng}
	if rng == "all" {
		revs = []string{"-" + strconv.Itoa(AllCommits), "HEAD"}
	}

	args = append(args, revs...)

	out, err := gitx.Exec(ctx, dir, append(args, "--")...)
	if err != nil {
		return nil, fmt.Errorf("audit map: %w", err)
	}

	// the range's commits the default branch does not hold, the short tail
	// that needs CommitTask's branch lookup; the rest need only their subject.
	// Bounded by the range, never by the repository's age.
	offDefault := map[string]bool{}

	revList := append(append([]string{"rev-list"}, revs...), "--not", regression.DefaultRef(ctx, dir), "--")
	if shas, err := gitx.Exec(ctx, dir, revList...); err == nil {
		for sha := range strings.FieldsSeq(shas) {
			offDefault[sha] = true
		}
	}

	var commits []Commit

	for rec := range strings.SplitSeq(out, recordSep) {
		fields := strings.SplitN(rec, fieldSep, 5)
		if len(fields) < 5 {
			continue
		}

		c := Commit{SHA: strings.TrimSpace(fields[0]), Task: history.TrailerTask(fields[3]), Files: numstat(fields[4])}
		c.At, _ = time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))

		switch {
		case c.Task != "":
		case offDefault[c.SHA]:
			if c.Task, err = regression.CommitTask(ctx, dir, prefix, c.SHA); err != nil {
				return nil, err
			}
		default:
			c.Task = regression.SubjectTask(prefix, fields[2])
		}

		commits = append(commits, c)
	}

	return commits, nil
}

func numstat(s string) []FileStat {
	var out []FileStat

	for line := range strings.SplitSeq(s, "\n") {
		cols := strings.SplitN(line, "\t", 3)
		if len(cols) != 3 {
			continue
		}

		added, _ := strconv.Atoi(cols[0]) // a binary file's "-" counts none
		deleted, _ := strconv.Atoi(cols[1])
		out = append(out, FileStat{Path: cols[2], Added: added, Deleted: deleted})
	}

	return out
}

// Area is a file's parent directory, the partition churn sums by.
func Area(file string) string { return path.Dir(file) }

// Build is the churn and the task map; the pain map is added by the caller,
// which reads the ledger.
//
//nolint:lostfield // sums commits into areas and tasks, not a conversion of one
func Build(rng string, commits []Commit) *Map {
	m := &Map{Range: rng, Churn: []Churn{}, Tasks: []Task{}, Upkeep: []string{}}
	churn := map[string]*Churn{}
	tasks := map[string]*Task{}

	for _, c := range commits {
		dirs := map[string]bool{}

		for _, f := range c.Files {
			dir := Area(f.Path)
			ch := churn[dir]

			if ch == nil {
				ch = &Churn{Dir: dir}
				churn[dir] = ch
			}

			ch.Added += f.Added
			ch.Deleted += f.Deleted

			if !dirs[dir] {
				ch.Commits++
				dirs[dir] = true
			}
		}

		if c.Task == "" {
			m.Upkeep = append(m.Upkeep, short(c.SHA))

			continue
		}

		key := strings.ToUpper(c.Task)

		task := tasks[key]
		if task == nil {
			task = &Task{ID: c.Task}
			tasks[key] = task
		}

		task.Commits = append(task.Commits, short(c.SHA))

		for dir := range dirs {
			if !slices.Contains(task.Areas, dir) {
				task.Areas = append(task.Areas, dir)
			}
		}
	}

	for _, ch := range churn {
		m.Churn = append(m.Churn, *ch)
	}

	slices.SortFunc(m.Churn, func(a, b Churn) int {
		return cmp.Or(cmp.Compare(b.Added+b.Deleted, a.Added+a.Deleted), cmp.Compare(a.Dir, b.Dir))
	})

	for _, k := range slices.Sorted(maps.Keys(tasks)) {
		task := tasks[k]
		slices.Sort(task.Areas)
		m.Tasks = append(m.Tasks, *task)
	}

	return m
}

// areaTasks inverts the task map: each area and the tasks that touched it.
func areaTasks(tasks []Task) []pain.AreaTasks {
	by := map[string][]string{}

	for _, t := range tasks {
		for _, a := range t.Areas {
			by[a] = append(by[a], t.ID)
		}
	}

	out := make([]pain.AreaTasks, 0, len(by))
	for _, dir := range slices.Sorted(maps.Keys(by)) {
		out = append(out, pain.AreaTasks{Dir: dir, Tasks: by[dir]})
	}

	return out
}

// onTasks reports a row of one of the range's tasks; a regression_found row
// is the bug's, so it counts only through its origin.
func onTasks(e ledger.Event, tasks []Task) bool {
	id := e.Task

	if e.Kind == ledger.RegressionFound {
		p, _ := ledger.Decode[ledger.RegressionFoundPayload](e)
		id = p.OriginTask
	}

	return slices.ContainsFunc(tasks, func(t Task) bool { return strings.EqualFold(t.ID, id) })
}

// oldest is the range's first commit time: regressions found since count.
func oldest(commits []Commit) time.Time {
	var t time.Time

	for _, c := range commits {
		if t.IsZero() || c.At.Before(t) {
			t = c.At
		}
	}

	return t
}

func short(sha string) string { return sha[:min(7, len(sha))] }

// Write is the text form: churn, tasks, upkeep, then the pain map.
func (m *Map) Write(w io.Writer) error {
	var b strings.Builder

	b.WriteString("churn\n")

	for _, c := range m.Churn {
		fmt.Fprintf(
			&b,
			"  %s  +%d -%d  %d %s\n",
			c.Dir,
			c.Added,
			c.Deleted,
			c.Commits,
			plural(c.Commits, "commit", "commits"),
		)
	}

	b.WriteString("tasks\n")

	for _, t := range m.Tasks {
		fmt.Fprintf(&b, "  %s  %s  %s\n", t.ID, strings.Join(t.Commits, ","), strings.Join(t.Areas, ", "))
	}

	if len(m.Upkeep) > 0 {
		fmt.Fprintf(&b, "upkeep  %s\n", strings.Join(m.Upkeep, ","))
	}

	b.WriteString("pain map\n")

	lines := m.Pain.Lines()
	if !m.Ledger {
		lines = []string{NoLedger}
	}

	for _, l := range lines {
		b.WriteString("  " + l + "\n")
	}

	_, err := io.WriteString(w, b.String())

	return err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
