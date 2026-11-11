package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// ExcludeLine is spelled as a repo that did it by hand would, so an existing
// entry is found.
const ExcludeLine = storage.DirName + "/"

// ExcludeF10 appends the entry to info/exclude rather than .gitignore, so the
// ignore itself is never committed.
func ExcludeF10(ctx context.Context, root string) (string, error) {
	named := gitx.Out(ctx, root, "rev-parse", "--git-path", "info/exclude")
	if named == "" {
		return "", errors.New("git could not name info/exclude")
	}

	path := named
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, named)
	}

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == ExcludeLine {
			return "already in " + path, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	entry := ExcludeLine + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		entry = "\n" + entry
	}

	if _, err := f.WriteString(entry); err != nil {
		return "", fmt.Errorf("appending to %s: %w", path, err)
	}

	return ExcludeLine + " in " + path, f.Close()
}
