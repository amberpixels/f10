package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Integrate brings base into the checked-out branch: merge (message one -m
// per part), rebase, or onto, which replays only the commits past upstream.
// A stop on conflicts is stopped, not an error: the merge or rebase stays in
// progress for whoever settles it.
func Integrate(ctx context.Context, dir, method, base, upstream string, message ...string) (bool, error) {
	var args []string

	switch method {
	case "merge":
		args = []string{"merge", "--no-edit"}
		for _, m := range message {
			args = append(args, "-m", m)
		}

		args = append(args, base)
	case "rebase":
		args = []string{"rebase", base}
	case "onto":
		args = []string{"rebase", "--onto", base, upstream}
	default:
		return false, fmt.Errorf("integrating %s: unknown method %q", base, method)
	}

	_, err := Exec(ctx, dir, args...)
	if err == nil {
		return false, nil
	}

	// git exits 1 for a conflict and for a refusal alike; only an operation
	// left in progress tells them apart
	op, opErr := InProgress(ctx, dir)
	if opErr == nil && op.Kind != "" {
		return true, nil
	}

	return false, err
}

// An Operation is a merge or a rebase in progress. Onto is the other side:
// the merged commit, or the commit a rebase replays onto. Done and Total
// count a rebase's commits, 0 where git does not say.
type Operation struct {
	Kind        string // "merge", "rebase", or "" for none
	Onto        string
	Done, Total int
}

// InProgress reads the merge or rebase in progress in dir from the git dir,
// since REBASE_HEAD exists only while a rebase is stopped on a commit.
func InProgress(ctx context.Context, dir string) (Operation, error) {
	merge, err := Ref(ctx, dir, "MERGE_HEAD")
	if err != nil {
		return Operation{}, fmt.Errorf("reading MERGE_HEAD in %s: %w", dir, err)
	}

	if merge != "" {
		return Operation{Kind: "merge", Onto: merge}, nil
	}

	for _, d := range []struct{ sub, marker, done, total string }{
		{"rebase-merge", "", "msgnum", "end"},
		{"rebase-apply", "rebasing", "next", "last"},
	} {
		path, err := gitPath(ctx, dir, d.sub)
		if err != nil {
			return Operation{}, err
		}

		if !isDir(path) || d.marker != "" && !isFile(filepath.Join(path, d.marker)) {
			continue
		}

		op := Operation{Kind: "rebase", Onto: readTrim(filepath.Join(path, "onto"))}
		op.Done, _ = strconv.Atoi(readTrim(filepath.Join(path, d.done)))
		op.Total, _ = strconv.Atoi(readTrim(filepath.Join(path, d.total)))

		return op, nil
	}

	return Operation{}, nil
}

// gitPath is git's path for name inside dir's git dir, absolute.
func gitPath(ctx context.Context, dir, name string) (string, error) {
	p, err := Exec(ctx, dir, "rev-parse", "--git-path", name)
	if err != nil {
		return "", fmt.Errorf("finding the git dir of %s: %w", dir, err)
	}

	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}

	return p, nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)

	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)

	return err == nil && !fi.IsDir()
}

func readTrim(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(b))
}

// Unmerged lists the paths a stopped merge or rebase left conflicted.
func Unmerged(ctx context.Context, dir string) ([]string, error) {
	out, err := Exec(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}

	return splitZ(out), nil
}

// Markers lists the paths the branch changes since base that still hold a
// conflict marker line. Only <<<<<<< and >>>>>>> count: a bare ======= is a
// markdown heading underline as often as a marker.
func Markers(ctx context.Context, dir, base string) ([]string, error) {
	out, err := Exec(ctx, dir, "diff", "--name-only", "--diff-filter=d", "-z", base+"...HEAD")
	if err != nil {
		return nil, err
	}

	paths := splitZ(out)
	if len(paths) == 0 {
		return nil, nil
	}

	args := append([]string{"grep", "-l", "-I", "-z", "-E", "^(<<<<<<<|>>>>>>>)( |$)", "--"}, paths...)

	found, ok, err := Read(ctx, dir, args...)
	if err != nil || !ok {
		return nil, err
	}

	return splitZ(found), nil
}

func splitZ(out string) []string {
	var paths []string

	for p := range strings.SplitSeq(out, "\x00") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}

	return paths
}
