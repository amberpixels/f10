package asks

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	ext         = ".yaml"
	answeredDir = "answered"
	claimDir    = "claims"
	// keepAnswered is how long an answered ask stays, for every other way
	// in to show it closed
	keepAnswered = 7 * 24 * time.Hour
)

// ClosedError is an ask no longer open: another way in answered it first.
type ClosedError struct {
	By string
	At time.Time
}

func (e ClosedError) Error() string {
	if e.By == "" {
		return "no open ask"
	}

	return "already answered " + Way(e.By) + " at " + e.At.Local().Format("15:04")
}

// Way names a way in for a sentence: `in its pane`, `with f10 asks`.
func Way(by string) string {
	switch by {
	case ByPane:
		return "in its pane"
	case ByAsks:
		return "with f10 asks"
	case BySkill:
		return "with /f10:asks"
	case ByDrive:
		return "with f10 drive --answer"
	case ByCancelled:
		return "by cancelling its drive"
	}

	return "by " + by
}

// Dir is the inbox: F10_ASKS_DIR, else ~/.claude/f10/asks, beside the run
// state and outside every project.
func Dir() string {
	if d := os.Getenv("F10_ASKS_DIR"); d != "" {
		return d
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".claude", "f10", "asks")
}

// Key names a task's ask: the storage root's hash, so two projects' FT-1
// differ, and the task id.
func Key(root, task string) string {
	sum := sha256.Sum256([]byte(root))

	return hex.EncodeToString(sum[:4]) + "-" + strings.ToUpper(task)
}

// Key is the ask's own key.
func (a *Ask) Key() string { return Key(a.Root, a.Task) }

func openPath(dir, key string) string { return filepath.Join(dir, key+ext) }

func answeredPath(dir, key string) string { return filepath.Join(dir, answeredDir, key+ext) }

// Raise records a, open, and reports whether it is new. An open ask, or an
// answered one no drive has taken, with the same text is the same ask: it
// keeps its time and answer, takes whatever a knows that it did not (the
// drive, the url), and is not new, so the agent's ask and its drive's
// report of it tell the user once.
func Raise(dir string, a Ask, now time.Time) (Ask, bool, error) {
	a.Text = strings.TrimSpace(a.Text)
	a.Questions = Parse(a.Text)
	key := a.Key()

	for _, path := range []string{answeredPath(dir, key), openPath(dir, key)} {
		prev, err := read(path)
		if err != nil || prev.Text != a.Text || !prev.TakenAt.IsZero() {
			continue
		}

		if merge(prev, a) {
			if err := write(path, prev); err != nil {
				return Ask{}, false, err
			}
		}

		return *prev, false, nil
	}

	a.AskedAt = now
	if err := write(openPath(dir, key), &a); err != nil {
		return Ask{}, false, err
	}

	return a, true, nil
}

// merge fills prev's empty fields from a, reporting a change.
func merge(prev *Ask, a Ask) bool {
	changed := false

	for _, f := range []struct {
		dst *string
		src string
	}{{&prev.URL, a.URL}, {&prev.Main, a.Main}, {&prev.Project, a.Project}, {&prev.Suffix, a.Suffix}, {&prev.Skill, a.Skill}} {
		if *f.dst == "" && f.src != "" {
			*f.dst, changed = f.src, true
		}
	}

	if prev.Drive == 0 && a.Drive != 0 {
		prev.Drive, changed = a.Drive, true
	}

	return changed
}

// Claim answers the open ask key: the first claim wins, through a rename
// only one caller can make; a later one gets ClosedError naming the winner.
// delivered says the answer already reached the agent.
func Claim(dir, key, by, answer string, delivered bool, now time.Time) (Ask, error) {
	claims := filepath.Join(dir, claimDir)
	if err := os.MkdirAll(claims, 0o700); err != nil {
		return Ask{}, err
	}

	held := filepath.Join(claims, key+"."+strconv.Itoa(os.Getpid()))
	if err := os.Rename(openPath(dir, key), held); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Ask{}, closed(dir, key)
		}

		return Ask{}, err
	}

	a, err := read(held)
	if err != nil {
		// put it back: an unreadable claim must not swallow the ask
		_ = os.Rename(held, openPath(dir, key))

		return Ask{}, err
	}

	a.Answer, a.By, a.AnsweredAt = answer, by, now
	if delivered {
		a.DeliveredAt = now
	}

	if err := write(answeredPath(dir, key), a); err != nil {
		_ = os.Rename(held, openPath(dir, key))

		return Ask{}, err
	}

	_ = os.Remove(held)

	return *a, nil
}

// closed names who answered key. A winner still writing its answer holds a
// claim file, so a loser waits for it briefly rather than naming nobody.
func closed(dir, key string) error {
	for range 20 {
		if a, err := read(answeredPath(dir, key)); err == nil {
			return ClosedError{By: a.By, At: a.AnsweredAt}
		}

		if held, _ := filepath.Glob(filepath.Join(dir, claimDir, key+".*")); len(held) == 0 {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	return ClosedError{}
}

// Update rewrites the answered ask key through fn.
func Update(dir, key string, fn func(a *Ask)) error {
	a, err := read(answeredPath(dir, key))
	if err != nil {
		return err
	}

	fn(a)

	return write(answeredPath(dir, key), a)
}

// Get is the open ask key, else its answered one, with whether it is open.
func Get(dir, key string) (*Ask, bool, error) {
	if a, err := read(openPath(dir, key)); err == nil {
		return a, true, nil
	}

	a, err := read(answeredPath(dir, key))

	return a, false, err
}

// Open lists the open asks, oldest first.
func Open(dir string) ([]Ask, error) { return list(dir) }

// Answered lists asks answered since, oldest answer first.
func Answered(dir string, since time.Time) ([]Ask, error) {
	all, err := list(filepath.Join(dir, answeredDir))

	var out []Ask

	for _, a := range all {
		if !a.AnsweredAt.Before(since) {
			out = append(out, a)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].AnsweredAt.Before(out[j].AnsweredAt) })

	return out, err
}

// Untaken lists answered asks under root that no drive has acted on.
func Untaken(dir, root string) ([]Ask, error) {
	all, err := list(filepath.Join(dir, answeredDir))

	var out []Ask

	for _, a := range all {
		if a.Root == root && a.TakenAt.IsZero() && a.By != ByCancelled {
			out = append(out, a)
		}
	}

	return out, err
}

// Prune drops answered asks older than a week.
func Prune(dir string, now time.Time) {
	entries, _ := os.ReadDir(filepath.Join(dir, answeredDir))

	for _, e := range entries {
		path := filepath.Join(dir, answeredDir, e.Name())
		if a, err := read(path); err == nil && now.Sub(a.AnsweredAt) > keepAnswered {
			_ = os.Remove(path)
		}
	}
}

func list(dir string) ([]Ask, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var out []Ask

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}

		// a file mid-write or broken is skipped, not fatal to the list
		if a, err := read(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, *a)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].AskedAt.Before(out[j].AskedAt) })

	return out, nil
}

func read(path string) (*Ask, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var a Ask
	if err := yaml.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &a, nil
}

// write replaces path through a rename, so a reader never sees half a file.
func write(path string, a *Ask) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	raw, err := yaml.Marshal(a)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".ask-*")
	if err != nil {
		return err
	}

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return err
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())

		return err
	}

	return os.Rename(tmp.Name(), path)
}
