package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// everyScalar declares each scalar frontmatter key, so the config golden
// shows how each one prints.
const everyScalar = `---
visibility: public
storage: in-repo
tracker:
  kind: jira
  id: AB-###
merge_method: merge
catchup: merge
catchup_stacked: rebase
traces: on
lore: committed
lore_gate: block
stale_after: 14d
driver_timeout: 2m
drive_env: [FOO_TOKEN]
watch: report
watch_catchup: on
report_back: off
standup:
  upkeep: ["docs/**"]
verify:
  - run: go test ./...
    paths: ["**/*.go"]
pace: medium
---
# demo

## Verify

` + "`just lint` / `just test`" + `
`

// configGolden runs f10 config --json on a fresh repo holding project.md
// (none when empty) and a justfile, with every machine path replaced.
func configGolden(t *testing.T, name, projectMD string) {
	t.Helper()

	home := isolateEnv(t)
	t.Setenv("F10_ROOTS", "")

	repo := mkRepo(t)

	if err := os.WriteFile(
		filepath.Join(repo, "justfile"),
		[]byte("lint:\n\techo\ntest:\n\techo\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if projectMD != "" {
		path := filepath.Join(repo, ".f10", "instructions", "project.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(projectMD), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := runApp(t, "-C", repo, "config", "--json").must(t)
	out = strings.ReplaceAll(out, repo, "$REPO")
	out = strings.ReplaceAll(out, canonical(t, home), "$HOME")
	out = strings.ReplaceAll(out, home, "$HOME")

	golden(t, filepath.Join("testdata", "config", name+".golden"), out)
}

func golden(t *testing.T, path, got string) {
	t.Helper()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}

	if string(want) != got {
		t.Errorf("%s differs (run with -update to rewrite):\n%s", path, got)
	}
}

func TestConfigJSONEveryScalar(t *testing.T) { configGolden(t, "every-scalar", everyScalar) }

func TestConfigJSONNothingDeclared(t *testing.T) { configGolden(t, "nothing-declared", "") }

// Only the host's own issues fall back to the host; a declared Notion or
// Jira tracker reads through its driver alone.
func TestHostIssuesByKind(t *testing.T) {
	for kind, want := range map[facts.TrackerKind]bool{
		facts.TrackerGitHub: true,
		facts.TrackerGitLab: true,
		facts.TrackerNotion: false,
		facts.TrackerJira:   false,
	} {
		if got := declaredTracker(t, kind).hostIssues(); got != want {
			t.Errorf("declared %s: hostIssues = %v, want %v", kind, got, want)
		}
	}

	// a kind only the CLI implies is no declaration, so the host answers
	detected := childTarget(t, "gh")
	detected.eff.TrackerKind = facts.TrackerJira
	detected.eff.Facts = nil

	if !detected.hostIssues() || detected.trackerDeclared() {
		t.Error("an undeclared kind should fall back to host issues")
	}

	none := childTarget(t, "gh")
	none.eff.TrackerKind = ""

	if !none.hostIssues() {
		t.Error("no kind should fall back to host issues")
	}
}

// The config view copies Effective field by field, so a field added there
// fails here until the view shows it or this list says the view folds it
// into its own shape.
func TestConfigViewCoversEffective(t *testing.T) {
	folded := []string{
		"agentsOrigin",        // each agents entry carries its own origin
		"pacesOrigin",         // each paces entry carries its own origin
		"paceOrigin",          // the pace entry carries its origin
		"ceilingOrigin",       // each ceiling entry carries its own origin
		"ceilingExcept",       // the ceiling's last entry
		"ceilingExceptOrigin", // the ceiling's last entry's origin
	}

	shown := jsonKeys(reflect.TypeFor[view]())

	for _, key := range jsonKeys(reflect.TypeFor[facts.Effective]()) {
		if !slices.Contains(shown, key) && !slices.Contains(folded, key) {
			t.Errorf("f10 config --json lacks Effective's %q", key)
		}
	}
}

func jsonKeys(typ reflect.Type) []string {
	var keys []string

	for f := range typ.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			keys = append(keys, name)
		}
	}

	return keys
}
