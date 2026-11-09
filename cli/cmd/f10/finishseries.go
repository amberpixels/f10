package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// seriesLore sets in.change to the lore change the closed series names, then
// returns the report's lore line ("" with no store).
func seriesLore(in *finishInput, end *seriesEnd) string {
	if end != nil {
		if change, ok := closedChange(taskReviews(in.main, in.plansDir, in.task.ID), *end); ok {
			in.change = change
		}
	}

	if in.loreStore == nil {
		return ""
	}

	return loreNote(in.loreStore, in.loreChange())
}

// closeSeries ends the task's series even when it had no review rounds,
// since the archive is what counts a task's PRs for its lore change; a series
// already closed with the same PR or head (finish run again) adds none.
func closeSeries(dir string, end seriesEnd) (string, error) {
	if exists(dir) {
		return archiveReviews(dir, end)
	}

	archive := filepath.Join(filepath.Dir(dir), "archive")
	for _, n := range seriesNumbers(archive, filepath.Base(dir)) {
		if end.same(readSeriesEnd(filepath.Join(archive, fmt.Sprintf("%s.%d", filepath.Base(dir), n)))) {
			return "", nil
		}
	}

	target := nextSeries(archive, filepath.Base(dir))
	if err := os.MkdirAll(target, 0o750); err != nil {
		return "", fmt.Errorf("closing the review series at %s: %w", dir, err)
	}

	return "", end.write(target)
}

// A seriesEnd is what an archived series records in its closed file: the PR
// (#204, or none), the commit it ended at, and when. A restart is rounds
// ended by a rebase before any PR existed: still the same PR, so it counts
// for no lore change.
type seriesEnd struct {
	pr, head string
	at       time.Time
	restart  bool
}

const seriesClosedFile = "closed"

func (e seriesEnd) write(dir string) error {
	at := e.at
	if at.IsZero() {
		at = time.Now()
	}

	body := fmt.Sprintf("pr: %s\nhead: %s\nclosed: %s\n",
		cmp.Or(e.pr, "none"), e.head, at.UTC().Format(time.RFC3339))
	if e.restart {
		body += "restart: yes\n"
	}

	if err := os.WriteFile(filepath.Join(dir, seriesClosedFile), []byte(body), 0o600); err != nil {
		return fmt.Errorf("recording the end of the review series at %s: %w", dir, err)
	}

	return nil
}

// same compares by PR number, so gh's #7 and glab's !7 match, else by head.
func (e seriesEnd) same(o seriesEnd) bool {
	if n := strings.TrimLeft(e.pr, "#!"); digits(n) && n == strings.TrimLeft(o.pr, "#!") {
		return true
	}

	return e.head != "" && e.head == o.head
}

// readSeriesEnd is zero for a series archived before the file existed.
func readSeriesEnd(dir string) seriesEnd {
	raw, err := os.ReadFile(filepath.Join(dir, seriesClosedFile))
	if err != nil {
		return seriesEnd{}
	}

	var e seriesEnd

	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, _ := strings.Cut(line, ":")

		switch value = strings.TrimSpace(value); key {
		case "pr":
			e.pr = value
		case "head":
			e.head = value
		case "closed":
			e.at, _ = time.Parse(time.RFC3339, value)
		case "restart":
			e.restart = value == "yes"
		}
	}

	return e
}

// seriesNumbers is the archived series of id, ascending.
func seriesNumbers(archive, id string) []int {
	entries, _ := os.ReadDir(archive)

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

func nextSeries(archive, id string) string {
	n := 1
	if have := seriesNumbers(archive, id); len(have) > 0 {
		n = have[len(have)-1] + 1
	}

	return filepath.Join(archive, fmt.Sprintf("%s.%d", id, n))
}

// closedPRs is the archived series that each ended a PR, oldest first: a
// restart is left out.
func closedPRs(dir string) []string {
	archive, id := filepath.Join(filepath.Dir(dir), "archive"), filepath.Base(dir)

	var out []string

	for _, n := range seriesNumbers(archive, id) {
		path := filepath.Join(archive, fmt.Sprintf("%s.%d", id, n))
		if !readSeriesEnd(path).restart {
			out = append(out, path)
		}
	}

	return out
}

// changeOf is the lore change of the task's open series: the task id for its
// first PR, <id>.<n> for its n-th, counted from the PRs archived.
func changeOf(dir string) string {
	return seriesChange(filepath.Base(dir), len(closedPRs(dir))+1)
}

// closedChange is the change of the PR end closed, when the newest archived
// PR is that one: a finish run again after its merge reports on it, not on
// the next series.
func closedChange(dir string, end seriesEnd) (string, bool) {
	closed := closedPRs(dir)
	if len(closed) == 0 || !end.same(readSeriesEnd(closed[len(closed)-1])) {
		return "", false
	}

	return seriesChange(filepath.Base(dir), len(closed)), true
}

func seriesChange(id string, n int) string {
	if n <= 1 {
		return id
	}

	return fmt.Sprintf("%s.%d", id, n)
}

// seriesStart is when the task's open series began, the newest archived
// PR's end; zero for a first series.
func seriesStart(dir string) time.Time {
	closed := closedPRs(dir)
	if len(closed) == 0 {
		return time.Time{}
	}

	newest := closed[len(closed)-1]
	if e := readSeriesEnd(newest); !e.at.IsZero() {
		return e.at
	}

	return modTime(newest)
}
