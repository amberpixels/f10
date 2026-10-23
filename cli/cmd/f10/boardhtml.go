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

// board.html is the static report: markup, styles and the renderer in one
// file, with one placeholder where the Board JSON goes. The page draws the
// JSON at open time and decides no stage; nothing is fetched.
//
//go:embed board.html
var boardHTMLPage string

// boardPlaceholder stands where the JSON goes, inside the page's data
// script tag; it cannot occur in HTML by accident.
const boardPlaceholder = "__BOARD_JSON__"

// renderBoardHTML writes the page with the board embedded. The encoder
// escapes `<`, `>` and `&`, so a title holding `</script>` cannot end the
// data tag early; no template engine is needed.
func renderBoardHTML(w io.Writer, b lookaround.Board) error {
	var buf bytes.Buffer

	if err := json.NewEncoder(&buf).Encode(b); err != nil {
		return fmt.Errorf("encoding the board: %w", err)
	}

	page := strings.Replace(boardHTMLPage, boardPlaceholder, strings.TrimSpace(buf.String()), 1)

	_, err := io.WriteString(w, page)

	return err
}
