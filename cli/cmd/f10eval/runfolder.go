package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The run folder sits beside its report and keeps what the run saw, did and
// left. docs/evals.md owns the layout, because the report page reads it.
const (
	transcriptFile = "transcript.jsonl"
	timesFile      = "transcript.times"
	diffFile       = "diff.patch"
	beforeDir      = "before"
	afterDir       = "after"
	inputsDir      = "inputs"
)

// A transcript is the stream as claude printed it, every turn of a run, and
// beside it each line's arrival in milliseconds since the run's first turn,
// because stream-json lines carry no time.
type transcript struct {
	mu    sync.Mutex
	lines *bufio.Writer
	times *bufio.Writer
	files []*os.File
	start time.Time
	err   error
}

func openTranscript(dir string) (*transcript, error) {
	t := &transcript{start: time.Now()}

	for _, name := range []string{transcriptFile, timesFile} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			_ = t.close()

			return nil, err
		}

		t.files = append(t.files, f)
	}

	t.lines, t.times = bufio.NewWriter(t.files[0]), bufio.NewWriter(t.files[1])

	return t, nil
}

// write keeps one line. A nil transcript keeps nothing, for a run with no
// folder. The first error sticks and close reports it.
func (t *transcript) write(line []byte) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.err != nil {
		return
	}

	ms := time.Since(t.start).Milliseconds()

	if _, err := t.lines.Write(append(line, '\n')); err != nil {
		t.err = err
	} else if _, err := fmt.Fprintln(t.times, ms); err != nil {
		t.err = err
	}
}

func (t *transcript) close() error {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	errs := []error{t.err}

	for _, w := range []*bufio.Writer{t.lines, t.times} {
		if w != nil {
			errs = append(errs, w.Flush())
		}
	}

	for _, f := range t.files {
		errs = append(errs, f.Close())
	}

	return errors.Join(errs...)
}

// A runFolder is one run's folder while the run fills it.
type runFolder struct {
	dir string
	tr  *transcript
}

// openRunFolder starts the folder afresh, since a folder left from an
// earlier run belongs to a report this run replaces.
func openRunFolder(dir string) (*runFolder, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}

	tr, err := openTranscript(dir)
	if err != nil {
		return nil, err
	}

	return &runFolder{dir: dir, tr: tr}, nil
}

// inputs copies what the scenario gave the run, and the scenario file with
// the accept commands it never saw.
func (f *runFolder) inputs(scenarioDir, fixtureRepo, task string) error {
	in := filepath.Join(f.dir, inputsDir)
	if err := os.MkdirAll(in, 0o750); err != nil {
		return err
	}

	for src, dst := range map[string]string{
		filepath.Join(scenarioDir, "scenario.json"):                     "scenario.json",
		filepath.Join(fixtureRepo, "tasks", task+".md"):                 "task.md",
		filepath.Join(fixtureRepo, "f10", "instructions", "project.md"): "project.md",
	} {
		if err := copyFile(src, filepath.Join(in, dst)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}

	return nil
}

func (f *runFolder) before(repo string) error {
	return copyTree(repo, filepath.Join(f.dir, beforeDir))
}

// after keeps the repo as the run left it and its diff from the initial
// commit, untracked files included. .f10/ stays out of the diff, as prepare
// excluded it, and is in after/.
func (f *runFolder) after(ctx context.Context, repo string) error {
	if err := copyTree(repo, filepath.Join(f.dir, afterDir)); err != nil {
		return err
	}

	if out, err := git(ctx, repo, "add", "-A", "--intent-to-add").CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, out)
	}

	root, _, _ := strings.Cut(gitOut(ctx, repo, "rev-list", "--max-parents=0", "HEAD"), "\n")
	if root == "" {
		return errors.New("the run's repo has no initial commit")
	}

	patch, err := git(ctx, repo, "diff", "--binary", root).Output()
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}

	return os.WriteFile(filepath.Join(f.dir, diffFile), patch, 0o600)
}

func (f *runFolder) close() error {
	if f == nil {
		return nil
	}

	return f.tr.close()
}

// copyTree copies src to dst without .git. A symlink is copied as a link,
// never followed out of the repo.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		target := filepath.Join(dst, rel)

		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o750)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}

			return os.Symlink(link, target) //nolint:gosec // writes the link itself, never follows it
		case d.Type().IsRegular():
			return copyFile(path, target)
		}

		return nil
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}

	_, err = io.Copy(out, in)

	return errors.Join(err, out.Close())
}

// readTranscript is a run folder's events, each with its arrival in
// milliseconds. A missing or short times file leaves the times at 0.
func readTranscript(dir string) ([]event, []int64, error) {
	body, err := os.ReadFile(filepath.Join(dir, transcriptFile))
	if err != nil {
		return nil, nil, err
	}

	times, _ := os.ReadFile(filepath.Join(dir, timesFile))
	ms := strings.Fields(string(times))

	var (
		events []event
		at     []int64
	)

	for i, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		var e event
		if line == "" || json.Unmarshal([]byte(line), &e) != nil {
			continue
		}

		var t int64
		if i < len(ms) {
			t, _ = strconv.ParseInt(ms[i], 10, 64)
		}

		events, at = append(events, e), append(at, t)
	}

	return events, at, nil
}
