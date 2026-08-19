package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/internal/probe"
	"github.com/amberpixels/f10/internal/resolve"
)

const mainProjectMD = `# demo project

- **Project** - A demo service, Go.
- **Tracker** - GitHub Issues. Id format ` + "`GH-###`" + `.
  Fetch: ` + "`gh issue view <n> --comments`" + `.
- **Ship pipelines** - implement → review (local) → pr
- **Visibility** - public

## Custom notes

Free prose the contract does not know.
`

func TestParseFile(t *testing.T) {
	path := writeProjectMD(t, mainProjectMD)

	fields, unknown := parseFile(path, "main")

	for _, want := range []string{"Project", "Tracker", "Ship pipeline(s)", "Visibility"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("field %q not parsed; got %v", want, keys(fields))
		}
	}

	// the indented Fetch sub-line belongs to Tracker's body, not a field
	if tracker := fields["Tracker"]; !contains(tracker.body, "gh issue view") {
		t.Errorf("Tracker body lost its sub-lines: %q", tracker.body)
	}

	// the alias form resolves to the canonical name
	if pipe := fields["Ship pipeline(s)"]; !contains(pipe.body, "review (local)") {
		t.Errorf("Ship pipelines alias not resolved: %q", pipe.body)
	}

	// unmatched openers are kept, never dropped
	if len(unknown) != 2 { // the title heading and "Custom notes"
		t.Errorf("unknown sections = %d, want 2: %+v", len(unknown), unknown)
	}
}

func TestBuildMergesLayersFieldLevel(t *testing.T) {
	res := &resolve.Resolution{
		Layers: []resolve.Layer{
			{Label: "main", Dir: dirWithProjectMD(t, "- **Project** - main says\n- **Verify** - just test\n")},
			{Label: "worktree", Dir: dirWithProjectMD(t, "- **Verify** - go test ./...\n")},
		},
		Layering: resolve.LayeringExtends,
	}

	eff := Build(res, &probe.Probes{})

	verify := fieldByName(t, eff, "Verify")
	if verify.Value != "go test ./..." || verify.Origin != "declared (worktree)" {
		t.Errorf("worktree field did not win: %+v", verify)
	}

	project := fieldByName(t, eff, "Project")
	if project.Value != "main says" || project.Origin != "declared (main)" {
		t.Errorf("silence did not keep main's field: %+v", project)
	}
}

func TestBuildDefaultsAndDetection(t *testing.T) {
	res := &resolve.Resolution{} // no layers at all
	pb := &probe.Probes{
		CLI:   probe.Signal{Value: "gh", Evidence: "well-known host github.com"},
		Host:  "github.com",
		Stack: []probe.Signal{{Value: "Go", Evidence: "go.mod"}},
	}

	eff := Build(res, pb)

	for name, want := range map[string]struct{ origin, value string }{
		"Project":          {origin: OriginDetected, value: "Go (go.mod)"},
		"Tracker":          {origin: OriginDetected, value: "GitHub Issues (via gh)"},
		"Ship pipeline(s)": {origin: OriginDefault, value: "implement → pr"},
		"Visibility":       {origin: OriginDefault, value: "stealth"},
		"Roles":            {origin: OriginAbsent, value: ""},
	} {
		f := fieldByName(t, eff, name)
		if f.Origin != want.origin || f.Value != want.value {
			t.Errorf("%s = %q (%s), want %q (%s)", name, f.Value, f.Origin, want.value, want.origin)
		}
	}

	if eff.Visibility != "stealth" || eff.TrackerKind != "GitHub Issues" {
		t.Errorf("sub-facts: visibility=%q trackerKind=%q", eff.Visibility, eff.TrackerKind)
	}

	// Layering says nothing outside a linked worktree
	for _, f := range eff.Fields {
		if f.Name == "Layering" {
			t.Errorf("Layering reported outside a worktree: %+v", f)
		}
	}
}

func TestBuildDeclaredVisibilityWins(t *testing.T) {
	res := &resolve.Resolution{
		Layers: []resolve.Layer{
			{Label: "main", Dir: dirWithProjectMD(t, "- **Visibility** - public\n")},
		},
	}

	eff := Build(res, &probe.Probes{})

	if eff.Visibility != "public" {
		t.Errorf("visibility = %q, want public", eff.Visibility)
	}

	f := fieldByName(t, eff, "Visibility")
	if f.Origin != "declared (main)" {
		t.Errorf("visibility origin = %q", f.Origin)
	}
}

func writeProjectMD(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "project.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

func dirWithProjectMD(t *testing.T, body string) string {
	t.Helper()

	return filepath.Dir(writeProjectMD(t, body))
}

func fieldByName(t *testing.T, eff *Effective, name string) Field {
	t.Helper()

	for _, f := range eff.Fields {
		if f.Name == name {
			return f
		}
	}

	t.Fatalf("field %q missing; got %+v", name, eff.Fields)

	return Field{}
}

func keys(m map[string]section) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
