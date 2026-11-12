package render_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestTaskLinksID(t *testing.T) {
	testkit.WithLinks(t, true)

	l := render.LinksFor(io.Discard, false)
	l.Load(t.Context(), testkit.LinkProject, "FT-1+FT-2", "FT-9")

	if got := l.ID(testkit.LinkProject, "FT-9"); got != "FT-9" {
		t.Errorf("an id the ledger lacks = %q", got)
	}

	if got, want := l.ID(testkit.LinkProject, "FT-1+FT-2"),
		render.Hyperlink(
			testkit.IssueURL("FT-1"),
			"FT-1",
		)+"+"+render.Hyperlink(
			testkit.IssueURL("FT-2"),
			"FT-2",
		); got != want {
		t.Errorf("bundle = %q, want %q", got, want)
	}

	if got := l.ID("other", "FT-1"); got != "FT-1" {
		t.Errorf("an id under another project = %q", got)
	}

	if got := render.LinksFor(io.Discard, true); got.On {
		t.Error("--plain links")
	}

	var none *render.TaskLinks
	if none.ID(testkit.LinkProject, "FT-1") != "FT-1" {
		t.Error("a nil taskLinks links")
	}
}

// Off, Load never opens the ledger, so a command piped to a file makes no
// database read.
func TestTaskLinksOffReadsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(ledger.PathEnv, path)

	render.LinksOn(false).Load(t.Context(), testkit.LinkProject, "FT-1")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the ledger was touched: %v", err)
	}
}

func TestPadRightCountsVisibleText(t *testing.T) {
	if got := render.PadRight(
		render.Hyperlink("https://x", "FT-1"),
		6,
	); render.VisibleWidth(got) != 6 ||
		!strings.HasSuffix(got, "  ") {
		t.Errorf("padRight = %q", got)
	}
}
