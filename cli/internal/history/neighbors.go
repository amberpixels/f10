package history

import (
	"bufio"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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

	var order, touched []string

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
		touched = append(touched, c.files...)
	}

	owner := map[string]*Touch{}

	for _, t := range byTask {
		for _, sha := range t.Commits {
			owner[sha] = t
		}
	}

	for _, counts := range blameAll(ctx, dir, uniq(touched)) {
		for sha, n := range counts {
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
	files []string // the paths it added lines to
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
		added, files := addedLines(numstat)
		commits = append(
			commits,
			commit{sha: strings.TrimSpace(sha), task: trailerTask(trailers), added: added, files: files},
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

// addedLines sums numstat's first column, with the files it counted; a
// binary file's "-" counts none.
func addedLines(numstat string) (int, []string) {
	total := 0

	var files []string

	for line := range strings.SplitSeq(numstat, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(fields) < 3 {
			continue
		}

		if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
			total += n
			files = append(files, fields[2])
		}
	}

	return total, files
}

// blameJobs bounds the git blame processes running at once.
const blameJobs = 8

// blameAll is blameCounts for each path, a few at a time: only the files a
// neighbor added lines to are blamed, so a directory's other files cost none.
func blameAll(ctx context.Context, dir string, paths []string) []map[string]int {
	out := make([]map[string]int, len(paths))
	sem := make(chan struct{}, blameJobs)

	var wg sync.WaitGroup

	for i, p := range paths {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			out[i] = blameCounts(ctx, dir, p)
		})
	}

	wg.Wait()

	return out
}

func uniq(ss []string) []string {
	slices.Sort(ss)

	return slices.Compact(ss)
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
