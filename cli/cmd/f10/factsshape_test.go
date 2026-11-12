package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
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

	home := testkit.IsolateEnv(t)
	t.Setenv("F10_ROOTS", "")

	repo := testkit.MkRepo(t)

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
	out = strings.ReplaceAll(out, testkit.Canonical(t, home), "$HOME")
	out = strings.ReplaceAll(out, home, "$HOME")

	golden(t, filepath.Join("testdata", "config", name+".golden"), out)
}

func golden(t *testing.T, path, got string) {
	t.Helper()

	if *testkit.UpdateGolden {
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
