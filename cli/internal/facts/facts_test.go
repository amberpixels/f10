package facts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/storage"
)

const mainProjectMD = `# demo project

- **Project** - A demo service, Go.
- **Tracker** - GitHub Issues. Id format ` + "`GH-###`" + `.
  Fetch: ` + "`gh issue view <n> --comments`" + `.
- **Visibility** - public

## Custom notes

Free prose the contract does not know.
`

func TestParseFile(t *testing.T) {
	t.Parallel()

	path := writeProjectMD(t, mainProjectMD)

	fields, unknown := parseFile(readFile(t, path), path, "main")

	for _, want := range []string{"Project", "Tracker", "Visibility"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("field %q not parsed; got %v", want, keys(fields))
		}
	}

	// the indented Fetch sub-line belongs to Tracker's body, not a field
	if tracker := fields["Tracker"]; !contains(tracker.body, "gh issue view") {
		t.Errorf("Tracker body lost its sub-lines: %q", tracker.body)
	}

	// an unmatched heading with content is kept; the empty title heading
	// says nothing and is skipped
	if len(unknown) != 1 || unknown[0].name != "Custom notes" {
		t.Errorf("unknown sections = %+v, want just Custom notes", unknown)
	}
}

// The heading-per-field shape with plain labels and sub-bullets - how real
// project.md files are written (acme is the reference).
const headingProjectMD = "# acme · f10 project instructions\n\n" +
	"## Project\n" +
	"Acme - inventory app. Ruby on Rails + PostgreSQL.\n" +
	"Roles: plan as a **senior software architect**; implement as a **senior Rails developer**.\n\n" +
	"## Tracker\n" +
	"Notion. Task id format: **`ABC-####`**.\n" +
	"- **Fetch:** invoke the `tasks` skill with the number.\n" +
	"- **Create:** invoke the `create-notion-task` skill.\n\n" +
	"## Visibility\nstealth\n"

func TestParseFileHeadingShape(t *testing.T) {
	t.Parallel()

	path := writeProjectMD(t, headingProjectMD)
	fields, unknown := parseFile(readFile(t, path), path, "main")

	// the empty title heading is skipped, and nothing else is unknown
	if len(unknown) != 0 {
		t.Errorf("unknown sections = %+v, want none", unknown)
	}

	// the plain Roles: label inside Project's prose opens its own field
	roles := fields["Roles"]
	if !contains(roles.body, "senior software architect") {
		t.Errorf("Roles not caught from a plain label: %q", roles.body)
	}

	// each role clause renders on a line of its own, hard-wrap unwrapped
	got := rolesLines("plan as a **senior software architect + Ruby on Rails developer**; implement as a\n" +
		"**senior Ruby on Rails developer**. Quality bar: SOLID / DRY / MVC and the conventions in\n" +
		"the repo's CLAUDE.md.")
	want := "plan as a **senior software architect + Ruby on Rails developer**\n" +
		"implement as a **senior Ruby on Rails developer**.\n" +
		"Quality bar: SOLID / DRY / MVC and the conventions in the repo's CLAUDE.md."

	if got != want {
		t.Errorf("rolesLines:\n got: %q\nwant: %q", got, want)
	}

	// the contract's bullet shape is already line-structured and stays put
	bullets := "- **Always** - plan as a senior architect\n- **Conditional** - + security engineer on auth"
	if rolesLines(bullets) != bullets {
		t.Errorf("bullet-shaped Roles were reflowed: %q", rolesLines(bullets))
	}

	if contains(fields["Project"].body, "Roles:") {
		t.Errorf("Roles still buried in Project: %q", fields["Project"].body)
	}

	// unknown bold items stay inside their section instead of hijacking it
	tracker := fields["Tracker"]
	if !contains(tracker.body, "Fetch:") || !contains(tracker.body, "Create:") {
		t.Errorf("Tracker lost its sub-bullets: %q", tracker.body)
	}
}

func TestBuildMergesLayersFieldLevel(t *testing.T) {
	t.Parallel()

	lay := &layout.Layout{
		Layers: []layout.Layer{
			{Label: "main", Dir: dirWithProjectMD(t, "- **Project** - main says\n- **Verify** - just test\n")},
			{Label: "worktree", Dir: dirWithProjectMD(t, "- **Verify** - go test ./...\n")},
		},
		Layering: layout.LayeringExtends,
	}

	eff := Build(lay, &probe.Probes{}, "")

	verify := fieldByName(t, eff, "Verify")
	if verify.Value != "go test ./..." || verify.Origin.String() != "declared (worktree)" {
		t.Errorf("worktree field did not win: %+v", verify)
	}

	project := fieldByName(t, eff, "Project")
	if project.Value != "main says" || project.Origin.String() != "declared (main)" {
		t.Errorf("silence did not keep main's field: %+v", project)
	}
}

func TestBuildDefaultsAndDetection(t *testing.T) {
	t.Parallel()

	lay := &layout.Layout{Project: "demo"} // no layers at all
	pb := &probe.Probes{
		CLI:   probe.Signal{Value: "gh", Evidence: "well-known host github.com"},
		Host:  "github.com",
		Stack: []probe.Signal{{Value: "Go", Evidence: "go.mod"}},
	}

	eff := Build(lay, pb, "")

	for name, want := range map[string]struct {
		origin OriginKind
		value  string
	}{
		"Project":    {origin: OriginDetected, value: "Go (go.mod)"},
		"Tracker":    {origin: OriginDetected, value: "GitHub Issues (via gh)"},
		"Visibility": {origin: OriginDefault, value: "stealth"},
		"Roles":      {origin: OriginAbsent, value: ""},
	} {
		f := fieldByName(t, eff, name)
		if f.Origin.Kind != want.origin || f.Value != want.value {
			t.Errorf("%s = %q (%s), want %q (%s)", name, f.Value, f.Origin, want.value, want.origin)
		}
	}

	if eff.Visibility != "stealth" || eff.TrackerKind != TrackerGitHub {
		t.Errorf("sub-facts: visibility=%q trackerKind=%q", eff.Visibility, eff.TrackerKind)
	}

	// the host names the tracker's kind, the project's own name its ids
	if eff.IDPrefix != "DEM" || eff.IDPrefixOrigin.String() != "derived from project name demo" {
		t.Errorf("id prefix = %q (%s), want DEM derived from the project name", eff.IDPrefix, eff.IDPrefixOrigin)
	}

	// Layering says nothing outside a linked worktree
	for _, f := range eff.Fields {
		if f.Name == "Layering" {
			t.Errorf("Layering reported outside a worktree: %+v", f)
		}
	}
}

// A Visibility section is prose for the agents; only the frontmatter's
// visibility key sets the fact.
func TestBuildVisibilityProseSetsNothing(t *testing.T) {
	t.Parallel()

	lay := &layout.Layout{
		Layers: []layout.Layer{
			{Label: "main", Dir: dirWithProjectMD(t, "- **Visibility** - public\n")},
		},
	}

	eff := Build(lay, &probe.Probes{}, "")

	if eff.Visibility != "stealth" || len(eff.Problems) > 0 {
		t.Errorf("visibility = %q, problems %+v, want the default stealth and no warning", eff.Visibility, eff.Problems)
	}

	f := fieldByName(t, eff, "Visibility")
	if f.Origin.String() != "declared (main)" {
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

// A declared id format is never second-guessed by the project's name, and
// says where it came from.
func TestBuildDeclaredPrefixWins(t *testing.T) {
	t.Parallel()

	lay := &layout.Layout{
		Project: "demo",
		Layers: []layout.Layer{
			{Label: "main", Dir: dirWithProjectMD(t, "---\ntracker: {kind: jira, id: ABC-####}\n---\n")},
		},
	}

	eff := Build(lay, &probe.Probes{CLI: probe.Signal{Value: "gh"}}, "")

	if eff.IDPrefix != "ABC" || eff.IDPrefixOrigin.String() != "declared (main, frontmatter)" {
		t.Errorf("id prefix = %q (%s), want ABC declared (main, frontmatter)", eff.IDPrefix, eff.IDPrefixOrigin)
	}
}

// A name that derives nothing leaves the prefix empty rather than inventing
// one: only explicit ids are accepted, and init leaves the Tracker to a human.
func TestBuildUnusableProjectName(t *testing.T) {
	t.Parallel()

	eff := Build(&layout.Layout{Project: "x"}, &probe.Probes{CLI: probe.Signal{Value: "gh"}}, "")

	if eff.IDPrefix != "" || eff.IDPrefixOrigin != (Origin{}) {
		t.Errorf("id prefix = %q (%s), want none", eff.IDPrefix, eff.IDPrefixOrigin)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// The drive runs a detected verify one command at a time, so the commands
// must leave the build file as a list, not as the field's display string.
func TestBuildDetectedVerifyCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "justfile"),
		[]byte("lint:\n    true\ntest:\n    true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	lay := &layout.Layout{Project: "demo", CheckoutRoot: dir, StorageRoot: storage.InRepo(dir)}
	eff := Build(lay, probe.Run(t.Context(), lay), "")

	if f := fieldByName(t, eff, "Verify"); f.Origin.Kind != OriginDetected || f.Value != "just lint / just test" {
		t.Errorf("Verify = %q (%s), want the two recipes, detected", f.Value, f.Origin)
	}

	if want := []string{"just lint", "just test"}; !slices.Equal(eff.VerifyCommands, want) {
		t.Errorf("VerifyCommands = %q, want %q", eff.VerifyCommands, want)
	}
}
