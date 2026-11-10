package archive

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// A SeriesEnd is what an archived series records in its closed file: the PR
// (#204, or none), the commit it ended at, and when. A restart is rounds
// ended by a rebase before any PR existed: still the same PR, so it counts
// for no lore change.
type SeriesEnd struct {
	Pr, Head string
	At       time.Time
	Restart  bool
}

func (e SeriesEnd) Write(dir string) error {
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}

	body := fmt.Sprintf("pr: %s\nhead: %s\nclosed: %s\n",
		cmp.Or(e.Pr, "none"), e.Head, at.UTC().Format(time.RFC3339))
	if e.Restart {
		body += "restart: yes\n"
	}

	if err := os.WriteFile(filepath.Join(dir, seriesClosedFile), []byte(body), 0o600); err != nil {
		return fmt.Errorf("recording the end of the review series at %s: %w", dir, err)
	}

	return nil
}

// Same compares by PR number, so gh's #7 and glab's !7 match, else by head.
func (e SeriesEnd) Same(o SeriesEnd) bool {
	if n := strings.TrimLeft(e.Pr, "#!"); worktree.Digits(n) && n == strings.TrimLeft(o.Pr, "#!") {
		return true
	}

	return e.Head != "" && e.Head == o.Head
}

const seriesClosedFile = "closed"

// ReadSeriesEnd is zero for a series archived before the file existed.
func ReadSeriesEnd(dir string) SeriesEnd {
	raw, err := os.ReadFile(filepath.Join(dir, seriesClosedFile))
	if err != nil {
		return SeriesEnd{}
	}

	var e SeriesEnd

	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, _ := strings.Cut(line, ":")

		switch value = strings.TrimSpace(value); key {
		case "pr":
			e.Pr = value
		case "head":
			e.Head = value
		case "closed":
			e.At, _ = time.Parse(time.RFC3339, value)
		case "restart":
			e.Restart = value == "yes"
		}
	}

	return e
}

// SeriesNumbers is the archived series of id, ascending.
func SeriesNumbers(archived, id string) []int {
	entries, _ := os.ReadDir(archived)

	var out []int

	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), id+".")
		if n, err := strconv.Atoi(rest); ok && e.IsDir() && err == nil && n > 0 {
			out = append(out, n)
		}
	}

	slices.Sort(out)

	return out
}

func NextSeries(archived, id string) string {
	n := 1
	if have := SeriesNumbers(archived, id); len(have) > 0 {
		n = have[len(have)-1] + 1
	}

	return filepath.Join(archived, fmt.Sprintf("%s.%d", id, n))
}

// ClosedPRs is the archived series that each ended a PR, oldest first: a
// restart is left out.
func ClosedPRs(dir string) []string {
	archived, id := storage.ArchiveOf(filepath.Dir(dir)), filepath.Base(dir)

	var out []string

	for _, n := range SeriesNumbers(archived, id) {
		path := filepath.Join(archived, fmt.Sprintf("%s.%d", id, n))
		if !ReadSeriesEnd(path).Restart {
			out = append(out, path)
		}
	}

	return out
}

// ChangeOf is the lore change of the task's open series: the task id for its
// first PR, <id>.<n> for its n-th, counted from the PRs archived.
func ChangeOf(dir string) string {
	return SeriesChange(filepath.Base(dir), len(ClosedPRs(dir))+1)
}

func SeriesChange(id string, n int) string {
	if n <= 1 {
		return id
	}

	return fmt.Sprintf("%s.%d", id, n)
}

// SeriesStart is when the task's open series began, the newest archived
// PR's end; zero for a first series.
func SeriesStart(dir string) time.Time {
	closed := ClosedPRs(dir)
	if len(closed) == 0 {
		return time.Time{}
	}

	newest := closed[len(closed)-1]
	if e := ReadSeriesEnd(newest); !e.At.IsZero() {
		return e.At
	}

	return ModTime(newest)
}
