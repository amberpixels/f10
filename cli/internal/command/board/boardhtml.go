package board

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

// BoardHTMLPage is board.html, the static report, one file with a placeholder for the
// Board JSON; the page decides no stage and fetches nothing.
//
//go:embed board.html
var BoardHTMLPage string

// BoardPlaceholder cannot occur in HTML by accident.
const BoardPlaceholder = "__BOARD_JSON__"

// colorsPlaceholder is where the stage colors go, drawn from the terminal's
// palette so the page and the terminal never disagree.
const colorsPlaceholder = "__STAGE_COLORS__"

// stageCSS is one rule per stage the palette colors, in a stable order.
func stageCSS() string {
	colors := statusview.StageColors()

	var b strings.Builder

	for _, stage := range slices.Sorted(maps.Keys(colors)) {
		fmt.Fprintf(&b, "  .stage[data-stage=%q] { color: var(--%s); }\n", stage, colors[stage])
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// RenderBoardHTML needs no template engine: the encoder escapes `<`, `>`
// and `&`, so a title holding `</script>` cannot end the data tag early.
func RenderBoardHTML(w io.Writer, b lookaround.Board) error {
	var buf bytes.Buffer

	if err := json.NewEncoder(&buf).Encode(b); err != nil {
		return fmt.Errorf("encoding the board: %w", err)
	}

	page := strings.Replace(BoardHTMLPage, colorsPlaceholder, stageCSS(), 1)
	page = strings.Replace(page, BoardPlaceholder, strings.TrimSpace(buf.String()), 1)

	_, err := io.WriteString(w, page)

	return err
}
