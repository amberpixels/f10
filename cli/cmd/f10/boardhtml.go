package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// board.html is the static report, one file with a placeholder for the
// Board JSON; the page decides no stage and fetches nothing.
//
//go:embed board.html
var boardHTMLPage string

// boardPlaceholder cannot occur in HTML by accident.
const boardPlaceholder = "__BOARD_JSON__"

// renderBoardHTML needs no template engine: the encoder escapes `<`, `>`
// and `&`, so a title holding `</script>` cannot end the data tag early.
func renderBoardHTML(w io.Writer, b lookaround.Board) error {
	var buf bytes.Buffer

	if err := json.NewEncoder(&buf).Encode(b); err != nil {
		return fmt.Errorf("encoding the board: %w", err)
	}

	page := strings.Replace(boardHTMLPage, boardPlaceholder, strings.TrimSpace(buf.String()), 1)

	_, err := io.WriteString(w, page)

	return err
}
