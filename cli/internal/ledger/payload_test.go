package ledger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// rawPayload is a payload as some f10 stored it, for rows in a legacy shape.
func rawPayload(t *testing.T, raw string) payloadCol {
	t.Helper()

	var p Payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}

	return col(p)
}

// storedKeys is the payload as it would be stored.
func storedKeys(t *testing.T, e Event) map[string]any {
	t.Helper()

	raw, err := json.Marshal(e.Payload.Val)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	return m
}

// declaredKinds is every string constant in observer.go's kind block.
func declaredKinds(t *testing.T) []string {
	t.Helper()

	f, err := parser.ParseFile(token.NewFileSet(), "observer.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}

		for _, spec := range g.Specs {
			for _, v := range spec.(*ast.ValueSpec).Values {
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					out = append(out, s)
				}
			}
		}
	}

	return out
}

func TestEveryKindHasAPayload(t *testing.T) {
	declared := declaredKinds(t)
	if len(declared) < 20 {
		t.Fatalf("read only %d kinds from observer.go", len(declared))
	}

	for _, k := range declared {
		if _, ok := kinds[k]; !ok {
			t.Errorf("kind %q has no registered payload", k)
		}
	}

	if extra := len(kinds) - len(declared); extra != 0 {
		t.Errorf("%d registered kinds are not declared", extra)
	}
}

func TestNewEventChecksTheKindsType(t *testing.T) {
	if _, err := NewEvent(RunStarted, PROpenedPayload{}); err == nil {
		t.Error("a pr_opened payload made a run_started row")
	}

	if _, err := NewEvent(RunStarted, &RunStartedPayload{}); err == nil {
		t.Error("a pointer payload was taken")
	}

	if _, err := NewEvent("no_such_kind", RunStartedPayload{}); err == nil {
		t.Error("an unknown kind was taken")
	}

	e, err := NewEvent(StateNote, ClaimPayload{Note: "n"})
	if err != nil || storedKeys(t, e)["v"] != float64(1) {
		t.Errorf("claim row %+v, err %v", e, err)
	}

	if _, err := Decode[RunStartedPayload](e); err == nil {
		t.Error("a claim row decoded as run_started")
	}
}

// absent reports a stored value that carries nothing: omitempty drops it.
func absent(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case float64:
		return v == 0
	case bool:
		return !v
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}

	return false
}

func carried(m map[string]any) map[string]any {
	out := map[string]any{}

	for k, v := range m {
		if k != versionKey && !absent(v) {
			out[k] = v
		}
	}

	return out
}

// Rows the release before typed payloads wrote, one per writer and shape,
// decode with nothing lost: re-encoded, every key with content survives.
func TestStoredRowsDecodeWithoutLoss(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "v1-rows.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var events []Event

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row struct {
			Kind    string          `json:"kind"`
			Source  string          `json:"source"`
			Task    string          `json:"task"`
			Payload json.RawMessage `json:"payload"`
		}

		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}

		events = append(
			events,
			Event{Kind: row.Kind, Source: row.Source, Task: row.Task, Payload: rawPayload(t, string(row.Payload))},
		)
	}

	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	path := tempPath(t)
	openT(t, path).Append(context.Background(), events...)

	seen := map[string]bool{}

	for _, e := range list(t, path, Filter{}) {
		seen[e.Kind] = true

		v, err := kinds.decode(e, nil)
		if err != nil {
			t.Errorf("%s: %v", e.Kind, err)

			continue
		}

		again := MustEvent(e.Kind, v)
		if want, got := carried(storedKeys(t, e)), carried(storedKeys(t, again)); !reflect.DeepEqual(want, got) {
			for _, k := range slices.Sorted(maps.Keys(want)) {
				if !reflect.DeepEqual(want[k], got[k]) {
					t.Errorf("%s: key %q stored %v, decodes to %v", e.Kind, k, want[k], got[k])
				}
			}
		}
	}

	for k := range kinds {
		if !seen[k] {
			t.Errorf("the fixture has no %s row", k)
		}
	}
}

type renamedV1 struct {
	Who string `json:"who"`
}

type renamedV2 struct {
	Author string `json:"author"`
}

// A kind bumped to v2 with a renamed key reads its v1 rows through the
// upcaster; a v3 row from a newer f10 decodes as far as it can, flagged.
func TestUpcastAndNewerVersions(t *testing.T) {
	r := registry{}
	register[renamedV1](r, "renamed")
	upcast(r, "renamed", func(o renamedV1) renamedV2 { return renamedV2{Author: o.Who} })

	v1 := Event{Kind: "renamed", Payload: rawPayload(t, `{"who":"ann"}`)}
	if got, err := decodeIn[renamedV2](r, v1); err != nil || got.Author != "ann" {
		t.Errorf("v1 row: %+v, %v", got, err)
	}

	e, err := r.event("renamed", renamedV2{Author: "bob"})
	if err != nil || storedKeys(t, e)["v"] != float64(2) {
		t.Fatalf("v2 row %+v, %v", e, err)
	}

	if got, err := decodeIn[renamedV2](r, e); err != nil || got.Author != "bob" {
		t.Errorf("v2 row: %+v, %v", got, err)
	}

	if _, err := r.event("renamed", renamedV1{}); err == nil {
		t.Error("a v1 payload was written after the bump")
	}

	v3 := Event{Kind: "renamed", Payload: rawPayload(t, `{"v":3,"author":"cy","extra":1}`)}

	got, err := decodeIn[renamedV2](r, v3)
	if !errors.Is(err, ErrNewerVersion) || got.Author != "cy" {
		t.Errorf("v3 row: %+v, %v", got, err)
	}
}

func TestReadEventsOfNoDatabaseIsEmpty(t *testing.T) {
	path := tempPath(t)

	events, err := ReadEvents(context.Background(), path, Filter{})
	if err != nil || events != nil {
		t.Errorf("events %v, err %v", events, err)
	}

	if _, err := os.Stat(path); err == nil {
		t.Error("reading created the database")
	}
}

// A spooled row is read back, and the filter narrows the rows.
func TestReadEventsSyncsTheSpoolAndFilters(t *testing.T) {
	path := tempPath(t)
	openT(t, path).Append(context.Background(), Event{Session: "s1", Kind: RunStarted, Source: Observed})
	Record(Event{Session: "s2", Kind: RunStarted, Source: Observed})

	all, err := ReadEvents(context.Background(), path, Filter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("events %+v, err %v", all, err)
	}

	s2, err := ReadEvents(context.Background(), path, Filter{Session: "s2"})
	if err != nil || len(s2) != 1 || s2[0].Session != "s2" {
		t.Errorf("filtered %+v, err %v", s2, err)
	}
}

// docs/ledger.schema.json is what the structs generate.
func TestSchemaMatchesTheCheckedInFile(t *testing.T) {
	got, err := Schema()
	if err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "ledger.schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Error(
			"docs/ledger.schema.json drifted from the payload structs: run `go run ./cmd/f10 ledger schema > ../docs/ledger.schema.json` in cli/",
		)
	}
}
