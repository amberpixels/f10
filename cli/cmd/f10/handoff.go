package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// handoff is the file steps/handoff.md writes: what the change leaves for
// whoever deploys it, with the commit it was written at.
type handoff struct {
	head string
	body string
}

// taskHandoff is the task's handoff file, beside its plans.
func taskHandoff(main, plansDir, id string) string {
	return filepath.Join(filepath.Dir(taskPlansDir(main, plansDir)), "handoffs", id+".md")
}

// readHandoff reads the frontmatter's head and the body after it; a missing
// file is ok false with no error.
func readHandoff(path string) (handoff, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return handoff{}, false, nil
	}

	if err != nil {
		return handoff{}, false, fmt.Errorf("reading the handoff at %s: %w", path, err)
	}

	rest, ok := bytes.CutPrefix(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("---\n"))
	if !ok {
		return handoff{}, false, fmt.Errorf("the handoff at %s opens with no frontmatter", path)
	}

	front, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return handoff{}, false, fmt.Errorf("the handoff at %s has no end to its frontmatter", path)
	}

	var h handoff

	for line := range strings.SplitSeq(string(front), "\n") {
		if k, v, found := strings.Cut(line, ":"); found && strings.TrimSpace(k) == "head" {
			h.head = strings.TrimSpace(v)
		}
	}

	if h.head == "" {
		return handoff{}, false, fmt.Errorf("the handoff at %s has no head line", path)
	}

	h.body = strings.Trim(string(body), "\n")

	return h, true, nil
}

// handoffLines is what finish prints under its report. It prints after the
// merge, when the branch is gone, so a stale or missing handoff names what
// to check rather than a command to rerun. tip runs only when the file
// exists, so a finish without one runs no extra git.
func handoffLines(path, id string, tip func() string) []string {
	h, ok, err := readHandoff(path)

	switch {
	case err != nil:
		return []string{err.Error() + ": check the diff for data, config or consumer changes before deploying"}
	case !ok:
		return []string{"no handoff for " + id + ": check the diff for data, config or consumer changes " +
			"before deploying; /f10:handoff writes one before a finish"}
	}

	lines := strings.Split(h.body, "\n")
	if at := tip(); at != "" && h.head != at {
		stale := fmt.Sprintf("the handoff was written at %s, the branch merged at %s: "+
			"it does not cover the later commits, so check them before deploying", shortSHA(h.head), shortSHA(at))
		lines = append([]string{stale}, lines...)
	}

	return lines
}

// branchTip is the local branch's commit, which the handoff step records as
// head; without the branch, the PR's head.
func branchTip(ctx context.Context, main, branch string, p pull) func() string {
	return func() string {
		if sha := gitx.Out(ctx, main, "rev-parse", "--verify", "-q", "refs/heads/"+branch); sha != "" {
			return sha
		}

		return p.headSHA()
	}
}

// archiveHandoff moves the handoff to handoffs/archive/, numbered as plans
// are, so the task's next PR writes its own; a missing file archives nothing.
func archiveHandoff(main, plansDir, id string) (string, error) {
	src := taskHandoff(main, plansDir, id)
	if !exists(src) {
		return "", nil
	}

	dst := filepath.Join(filepath.Dir(src), "archive")
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", fmt.Errorf("archiving the handoff at %s: %w", src, err)
	}

	target := freeArchiveName(dst, id)
	if err := os.Rename(src, target); err != nil {
		return "", fmt.Errorf("archiving the handoff at %s: %w", src, err)
	}

	return target, nil
}

// writeHandoff prints the lines apart from the notes, so the body's
// headings read as their own block.
func writeHandoff(w io.Writer, lines []string) error {
	if len(lines) == 0 {
		return nil
	}

	_, err := fmt.Fprintf(w, "\n%s\n", strings.Join(lines, "\n"))

	return err
}
