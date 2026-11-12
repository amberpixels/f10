package board_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	boardcmd "github.com/amberpixels/f10/cli/internal/command/board"
	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// Rows of several projects group under the person each resolves to, the
// unresolved last, and each line names its project; a row waiting on its
// base sorts after the work in progress.
func TestRenderByPerson(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	boards := []lookaround.Board{
		{Project: lookaround.Project{Name: "api"}, Rows: []lookaround.Row{
			{Key: "FT-1", Stage: lookaround.StageNext, Who: lookaround.Who{Person: "bob"}},
			{Key: "FT-2", Stage: lookaround.StageInReview, Who: lookaround.Who{Person: "ann"}, LastActivity: at},
			{Key: "#9", Stage: lookaround.StagePROpen},
			{
				Key: "FT-4", Stage: lookaround.StageBlocked, BlockedBy: []string{"FT-2"},
				Who: lookaround.Who{Person: "ann"},
			},
		}},
		{Project: lookaround.Project{Name: "web"}, Rows: []lookaround.Row{
			{Key: "WEB-3", Stage: lookaround.StageDone, Who: lookaround.Who{Person: "ann"}, LastActivity: at},
		}},
	}

	var out bytes.Buffer

	boardcmd.RenderByPerson(&out, boards, 0)

	var heads, lines []string

	for l := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}

		if !strings.Contains(l, "  ") {
			heads = append(heads, l)
		} else {
			lines = append(lines, strings.Join(strings.Fields(l)[:2], " "))
		}
	}

	if got, want := strings.Join(heads, ","), "ann,bob,nobody"; got != want {
		t.Errorf("headings = %q, want %q\n%s", got, want, out.String())
	}

	if got, want := strings.Join(lines, ","), "api FT-2,api FT-4,web WEB-3,api FT-1,api #9"; got != want {
		t.Errorf("rows = %q, want %q\n%s", got, want, out.String())
	}
}

// The glance counts what is in flight and what waits on a person, names
// the oldest, and says failed once for a project nothing could read.
func TestRenderSummary(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	boards := []lookaround.Board{
		{
			Project: lookaround.Project{Name: "api"},
			Sources: []lookaround.Source{{Name: "host", OK: true}},
			Rows: []lookaround.Row{
				{Key: "FT-1", Stage: lookaround.StageNext, Next: lookaround.Next{Command: "f10 start FT-1"}},
				{
					Key: "FT-2", Stage: lookaround.StageInReview, LastActivity: now.Add(-50 * time.Hour),
					Next: lookaround.Next{Command: "/f10:resolve FT-2"},
				},
				{
					Key: "FT-3", Stage: lookaround.StagePROpen, LastActivity: now.Add(-3 * time.Hour),
					Next: lookaround.Next{Text: "waiting on review"},
				},
				{Key: "FT-4", Stage: lookaround.StageDone},
			},
		},
		lookaround.Unreachable(
			lookaround.Project{Name: "web"}, os.ErrNotExist, now, lookaround.Window{Since: now}, lookaround.ViewState,
		),
		{Project: lookaround.Project{Name: "ops"}, Sources: []lookaround.Source{
			{Name: "host", Error: "gh: down"}, {Name: "tracker", OK: true}, {Name: "local", OK: true},
		}},
	}

	var out bytes.Buffer

	boardcmd.RenderSummary(&out, boards, now, 0)

	for _, want := range []string{
		"api  2 in flight  1 waiting on you  oldest FT-2 2d",
		"web  failed  file does not exist",
		"ops  nothing in flight  host failed  gh: down",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
}

func TestTitleAfterKey(t *testing.T) {
	for _, tc := range []struct{ title, key, want string }{
		{"FT-121: Show f10 board", "FT-121", "Show f10 board"},
		{"FT-121 - Show f10 board", "FT-121", "Show f10 board"},
		{"FT-121: Show f10 board", "FT-12", "FT-121: Show f10 board"},
		{"Show f10 board", "FT-121", "Show f10 board"},
		{"FT-121", "FT-121", "FT-121"},
		{"#12 title", "", "#12 title"},
		{"FT-121 (+FT-122, FT-123, 2 more): Show f10 board", "FT-121", "Show f10 board"},
	} {
		if got := boardcmd.TitleAfterKey(tc.title, tc.key); got != tc.want {
			t.Errorf("titleAfterKey(%q, %q) = %q, want %q", tc.title, tc.key, got, tc.want)
		}
	}
}
