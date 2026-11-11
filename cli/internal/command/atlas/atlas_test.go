package atlas

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/atlas"
)

// Each edge line sits under its own project, a multi-part error included.
func TestRenderKeepsEdgeLinesUnderTheirNode(t *testing.T) {
	t.Parallel()

	doc := atlas.Document{
		Nodes: []atlas.Node{
			{ID: "local/a", Name: "a", Error: "reading the history: bad; reading go.mod: bad"},
			{ID: "local/b", Name: "b"},
			{ID: "local/c", Name: "c"},
		},
		Edges: []atlas.Edge{{From: "local/b", To: "local/c", Kind: "depends", Source: "manifest", Manifest: "go.mod"}},
	}

	var out bytes.Buffer
	render(&out, doc, "atlas.md")

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[1], "b ") || lines[2] != "  depends on: c" ||
		!strings.HasPrefix(lines[3], "c ") || lines[4] != "  used by: b" {
		t.Errorf("render =\n%s", out.String())
	}
}
