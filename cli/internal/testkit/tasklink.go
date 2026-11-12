package testkit

import (
	"context"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/target"
)

const LinkProject = "github.com/o/r"

func IssueURL(id string) string {
	return "https://github.com/o/r/issues/" + strings.TrimPrefix(id, "FT-")
}

// WithLinks seeds the ledger with FT-1..FT-3 and makes every writer a
// terminal when on; ProjectOf answers linkProject for any directory.
func WithLinks(t *testing.T, on bool) {
	t.Helper()

	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	seam.Swap(t, &render.LinkTerminal, func(io.Writer) bool { return on })
	seam.Swap(t, &target.ProjectOf, func(context.Context, string) string { return LinkProject })

	ledger.RememberRefs(t.Context(), LinkProject,
		ledger.TaskRef{ID: "FT-1", Title: "One", URL: IssueURL("FT-1")},
		ledger.TaskRef{ID: "FT-2", Title: "Two", URL: IssueURL("FT-2")},
		ledger.TaskRef{ID: "FT-3", Title: "Three", URL: IssueURL("FT-3")},
	)
}

var LinkSpan = regexp.MustCompile("\x1b\\]8;;([^\x1b]*)\x1b\\\\([^\x1b]*)\x1b\\]8;;\x1b\\\\")
