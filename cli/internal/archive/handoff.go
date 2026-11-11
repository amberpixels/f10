package archive

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

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// TaskHandoff is the task's handoff file.
func TaskHandoff(root storage.Root, id string) string {
	return filepath.Join(root.Handoffs(), id+".md")
}

// handoff is the file steps/handoff.md writes: what the change leaves for
// whoever deploys it, with the commit it was written at.
type handoff struct {
	head string
	body string
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

// HandoffLines is what finish prints under its report. It prints after the
// merge, when the branch is gone, so a stale or missing handoff names what
// to check rather than a command to rerun. tip runs only when the file
// exists, so a finish without one runs no extra git.
func HandoffLines(path, id string, tip func() string) []string {
	h, ok, err := readHandoff(path)

	switch {
	case err != nil:
		return []string{err.Error() + ": check the diff for data, config or consumer changes before deploying"}
	case !ok:
		return []string{"no handoff for " + id + ": check the diff for data, config or consumer changes " +
			"before deploying; /f10:handoff writes one before a finish"}
	}

	lines := strings.Split(h.body, "\n")
	if at := tip(); at != "" && !gitx.SameCommit(h.head, at) {
		stale := fmt.Sprintf("the handoff was written at %s, the branch merged at %s: "+
			"it does not cover the later commits, so check them before deploying", gitx.ShortSHA(h.head), gitx.ShortSHA(at))
		lines = append([]string{stale}, lines...)
	}

	return lines
}

// BranchTip is the local branch's commit, which the handoff step records as
// head; without the branch, the PR's head.
func BranchTip(ctx context.Context, main, branch string, p forge.PR) func() string {
	return func() string {
		if sha := gitx.Out(ctx, main, "rev-parse", "--verify", "-q", "refs/heads/"+branch); sha != "" {
			return sha
		}

		return p.HeadSHA
	}
}

// HandoffTarget names where the handoff will be archived, "" with none.
// Finish names it for its report and moves the file only after printing it,
// so a failure before the report leaves the file for the rerun to print.
func HandoffTarget(root storage.Root, id string) (string, string) {
	src := TaskHandoff(root, id)
	if !fsx.Exists(src) {
		return src, ""
	}

	return src, FreeArchiveName(storage.ArchiveOf(root.Handoffs()), id)
}

func MoveHandoff(src, target string) error {
	if target == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return fmt.Errorf("archiving the handoff at %s: %w", src, err)
	}

	if err := os.Rename(src, target); err != nil {
		return fmt.Errorf("archiving the handoff at %s: %w", src, err)
	}

	return nil
}

// WriteHandoff prints the lines apart from the notes, so the body's
// headings read as their own block.
func WriteHandoff(w io.Writer, lines []string) error {
	if len(lines) == 0 {
		return nil
	}

	_, err := fmt.Fprintf(w, "\n%s\n", strings.Join(lines, "\n"))

	return err
}
