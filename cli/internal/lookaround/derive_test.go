package lookaround

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every fixture under testdata/derive is a Facts file beside the Board it
// derives to, at one fixed now and since. F10_UPDATE=1 rewrites the
// boards from the code: read the diff before keeping one.
var (
	fixedNow   = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	fixedSince = fixedNow.Add(-7 * 24 * time.Hour)
)

func TestDeriveFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/derive/*.facts.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}

	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".facts.json")

		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			var f Facts
			if err := json.Unmarshal(raw, &f); err != nil { //nolint:musttag // a fixture's keys are the field names
				t.Fatal(err)
			}

			got, err := json.MarshalIndent(Derive(f, fixedNow, fixedSince), "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			got = append(got, '\n')
			golden := strings.TrimSuffix(path, ".facts.json") + ".board.json"

			if os.Getenv("F10_UPDATE") != "" {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (F10_UPDATE=1 writes it)", err)
			}

			if !bytes.Equal(got, want) {
				t.Errorf("board differs from %s:\n%s", golden, got)
			}
		})
	}
}

// A board is the same whatever order the facts came in.
func TestDeriveIsStable(t *testing.T) {
	raw, err := os.ReadFile("testdata/derive/next-ranked.facts.json")
	if err != nil {
		t.Fatal(err)
	}

	var f Facts
	if err := json.Unmarshal(raw, &f); err != nil { //nolint:musttag // a fixture's keys are the field names
		t.Fatal(err)
	}

	first := Derive(f, fixedNow, fixedSince)

	f.Tasks[0], f.Tasks[1] = f.Tasks[1], f.Tasks[0]

	second := Derive(f, fixedNow, fixedSince)

	for i := range first.Rows {
		if first.Rows[i].Key != second.Rows[i].Key {
			t.Fatalf("row %d: %s then %s", i, first.Rows[i].Key, second.Rows[i].Key)
		}
	}
}

// Nothing in a fixture or a derived board names f10, outside the commands
// a next action spells.
func TestFixturesNameNothing(t *testing.T) {
	paths, _ := filepath.Glob("testdata/derive/*.json")

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}

		if rows, ok := doc["rows"].([]any); ok {
			for _, r := range rows {
				if next, ok := r.(map[string]any)["next"].(map[string]any); ok {
					delete(next, "command")
				}
			}
		}

		clean, _ := json.Marshal(doc)
		if strings.Contains(strings.ToLower(string(clean)), "f10") {
			t.Errorf("%s names f10", path)
		}
	}
}
