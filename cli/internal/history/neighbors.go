package history

import (
	"bufio"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/regression"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// StandingFloor is the share of a task's added lines that must still stand
// at HEAD for its history to apply; below it the task is stale.
const StandingFloor = 30

// Touch is one task's work on the files: its commits, the lines they added
// there, and how many of those lines blame still gives it at HEAD.
type Touch struct {
	Task     string   `json:"task"`
	Commits  []string `json:"commits"`
	Added    int      `json:"added"`
	Standing int      `json:"standing"`
}

// Stale reports a task whose lines were mostly rewritten since. A task that
// only deleted lines has nothing to measure, so it stays.
func (t Touch) Stale() bool { return t.Added > 0 && t.Standing*100 < StandingFloor*t.Added }

// Neighbors is the tasks whose commits since touched paths at HEAD, oldest
// first, skip left out. One git log reads every commit's trailers and line
// counts; only a commit with no Task: or Change: trailer costs more git.
func Neighbors(ctx context.Context, dir, prefix string, paths []string, since time.Time, skip string) ([]Touch, error) {
	commits, err := commitsSince(ctx, dir, paths, since)
	if err != nil {
		return nil, err
	}

	byTask := map[string]*Touch{}

	var order []string

	for _, c := range slices.Backward(commits) {
		task := c.task
		if task == "" {
			if task, err = regression.CommitTask(ctx, dir, prefix, c.sha); err != nil {
				return nil, err
			}
		}

		if task == "" || strings.EqualFold(task, skip) {
			continue
		}

		t, ok := byTask[task]
		if !ok {
			t = &Touch{Task: task}
			byTask[task] = t
			order = append(order, task)
		}

		t.Commits = append(t.Commits, c.sha)
		t.Added += c.added
	}

	owner := map[string]*Touch{}

	for _, t := range byTask {
		for _, sha := range t.Commits {
			owner[sha] = t
		}
	}

	for _, p := range paths {
		for sha, n := range blameCounts(ctx, dir, p) {
			if t := owner[sha]; t != nil {
				t.Standing += n
			}
		}
	}

	out := make([]Touch, 0, len(order))
	for _, id := range order {
		out = append(out, *byTask[id])
	}

	return out, nil
}

type commit struct {
	sha   string
	task  string // from its trailers; "" when they name none
	added int
}

const (
	recordSep = "\x1e"
	fieldSep  = "\x1f"
)

// commitsSince is git log over paths, newest first, with each commit's
// task trailer and the lines it added to those paths.
func commitsSince(ctx context.Context, dir string, paths []string, since time.Time) ([]commit, error) {
	args := []string{
		"log", "--no-renames", "--numstat", "--since=" + since.UTC().Format(time.RFC3339),
		"--format=" + recordSep + "%H" + fieldSep + "%(trailers:only,unfold)" + fieldSep, "HEAD", "--",
	}

	out, err := gitx.Exec(ctx, dir, append(args, paths...)...)
	if err != nil {
		return nil, err
	}

	var commits []commit

	for rec := range strings.SplitSeq(out, recordSep) {
		sha, rest, ok := strings.Cut(rec, fieldSep)
		if !ok {
			continue
		}

		trailers, numstat, _ := strings.Cut(rest, fieldSep)
		commits = append(
			commits,
			commit{sha: strings.TrimSpace(sha), task: trailerTask(trailers), added: addedLines(numstat)},
		)
	}

	return commits, nil
}

var changeRe = regexp.MustCompile(`(?im)^change:\s*(\S+)`)

// trailerTask is a bundle's lead from Task:, else the id a Change: names.
func trailerTask(trailers string) string {
	if tasks := trace.Parse(trailers).Tasks; len(tasks) > 0 {
		return tasks[0]
	}

	if m := changeRe.FindStringSubmatch(trailers); m != nil {
		id, _, _ := strings.Cut(m[1], ".")

		return id
	}

	return ""
}

// addedLines sums numstat's first column; a binary file's "-" counts none.
func addedLines(numstat string) int {
	total := 0

	for line := range strings.SplitSeq(numstat, "\n") {
		added, _, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}

		if n, err := strconv.Atoi(added); err == nil {
			total += n
		}
	}

	return total
}

var blameHeader = regexp.MustCompile(`^([0-9a-f]{40}) \d+ \d+`)

// blameCounts is the lines each commit owns in path at HEAD; a path HEAD
// does not hold owns none, because its code is gone.
func blameCounts(ctx context.Context, dir, path string) map[string]int {
	out, err := gitx.Exec(ctx, dir, "blame", "--porcelain", "HEAD", "--", path)
	if err != nil {
		return nil
	}

	counts := map[string]int{}

	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for sc.Scan() {
		if m := blameHeader.FindStringSubmatch(sc.Text()); m != nil {
			counts[m[1]]++
		}
	}

	return counts
}
