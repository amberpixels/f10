package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// The board at a terminal width: titles are cut to what the other columns
// leave, the tail follows the title, and columns line up across groups.
func TestRenderByPersonAtWidth(t *testing.T) {
	boards := []lookaround.Board{
		{Project: lookaround.Project{Name: "api"}, Rows: []lookaround.Row{
			{
				Key: "FT-1", Stage: lookaround.StageNext, Who: lookaround.Who{Person: "bob"},
				Task: &lookaround.Task{ID: "FT-1", Title: "Add rate limiting to every public endpoint of the API"},
				Next: lookaround.Next{Text: "start"},
			},
			{
				Key: "FT-22", Stage: lookaround.StageInReview, Who: lookaround.Who{Person: "ann"},
				Task: &lookaround.Task{ID: "FT-22", Title: "Short"},
			},
		}},
		{Project: lookaround.Project{Name: "website"}, Rows: []lookaround.Row{
			{
				Key: "WEB-3", Stage: lookaround.StageDone, Who: lookaround.Who{Person: "ann"},
				Task: &lookaround.Task{ID: "WEB-3", Title: "表示幅の広いタイトルが端末からはみ出さないこと"},
			},
		}},
	}

	var out bytes.Buffer

	renderByPerson(&out, boards, 70)

	got := strings.TrimLeft(out.String(), "\n")
	for l := range strings.SplitSeq(got, "\n") {
		if w := textx.Width(l); w > 70 {
			t.Errorf("a %d-column line overflows 70: %q", w, l)
		}
	}

	if got != wantByPersonAt70 {
		t.Errorf("board at 70 columns:\n%s\nwant:\n%s", got, wantByPersonAt70)
	}
}

const wantByPersonAt70 = `ann
api      FT-22  in-review    Short
website  WEB-3  done         表示幅の広いタイトルが端末からはみ出さな…

bob
api      FT-1   next         Add rate limiting to every public…  start
`
