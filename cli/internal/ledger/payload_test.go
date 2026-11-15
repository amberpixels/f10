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
	"strings"
	"testing"
	"time"
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
	if _, err := NewEvent(RunStarted, &PROpenedPayload{}); err == nil {
		t.Error("a pr_opened payload made a run_started row")
	}

	if _, err := NewEvent(RunStarted, "skill"); err == nil {
		t.Error("a payload that is no message was taken")
	}

	if _, err := NewEvent("no_such_kind", &RunStartedPayload{}); err == nil {
		t.Error("an unknown kind was taken")
	}

	e, err := NewEvent(StateNote, &ClaimPayload{Note: "n"})
	if err != nil || storedKeys(t, e)["v"] != float64(1) {
		t.Errorf("claim row %+v, err %v", e, err)
	}

	if _, err := Decode[*RunStartedPayload](e); err == nil {
		t.Error("a claim row decoded as run_started")
	}
}

// absent reports a stored value that carries nothing: omitempty drops it,
// and protojson writes an enum's zero as its UNSPECIFIED name.
func absent(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == "" || strings.HasSuffix(v, "_UNSPECIFIED")
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

// carried is a stored payload's content, nested objects included, with
// what tells representation from loss settled: absent values dropped and
// a timestamp written in UTC, since protojson re-encodes in UTC.
func carried(m map[string]any) map[string]any {
	delete(m, versionKey)

	out, _ := normalized(m).(map[string]any)

	return out
}

func normalized(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}

		for k, x := range v {
			if x = normalized(x); !absent(x) {
				out[k] = x
			}
		}

		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalized(x)
		}

		return out
	case string:
		if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return at.UTC().Format(time.RFC3339Nano)
		}
	}

	return v
}

// Rows the release before typed payloads wrote, one per writer and shape,
// decode with nothing lost: re-encoded, every key with content survives.
func TestStoredRowsDecodeWithoutLoss(t *testing.T) {
	seen := checkFixture(t, "v1-rows.jsonl")

	// kinds added after the fixture was frozen have no legacy rows to read
	for k := range kinds {
		if !seen[k] && !bornTyped[k] && !bornProto[k] {
			t.Errorf("the fixture has no %s row", k)
		}
	}
}

// Rows the last release before proto payloads wrote, through the structs
// that were then the payloads, decode with nothing lost. Every kind but the
// ones born as proto messages existed by then, so each has rows here.
func TestTypedRowsDecodeWithoutLoss(t *testing.T) {
	seen := checkFixture(t, "typed-rows.jsonl")

	for k := range kinds {
		if !seen[k] && !bornProto[k] {
			t.Errorf("the fixture has no %s row", k)
		}
	}
}

// checkFixture stores testdata/name's rows, decodes each, re-encodes it and
// fails on any key whose content changed; it returns the kinds it read.
func checkFixture(t *testing.T, name string) map[string]bool {
	t.Helper()

	f, err := os.Open(filepath.Join("testdata", name))
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

			for k := range got {
				if _, ok := want[k]; !ok {
					t.Errorf("%s: key %q appeared on decode: %v", e.Kind, k, got[k])
				}
			}
		}
	}

	return seen
}

// bornTyped is the kinds added after typed payloads, which have no untyped
// rows to keep reading.
var bornTyped = map[string]bool{
	TurnUsed: true, RegressionFound: true, RegressionClaimed: true, AskRaised: true, AskAnswered: true,
}

// bornProto is the kinds added after proto payloads, which no earlier
// release wrote.
var bornProto = map[string]bool{WatchReported: true}

// A kind bumped to a new message reads its v1 rows through the upcaster; a
// v3 row from a newer f10 decodes as far as it can, flagged. Two unrelated
// messages stand in for a kind's old and new shape.
func TestUpcastAndNewerVersions(t *testing.T) {
	r := registry{}
	register[*PlanSavedPayload](r, "moved")
	upcast(r, "moved", func(o *PlanSavedPayload) *PlanSupersededPayload {
		return &PlanSupersededPayload{From: o.GetPath(), Task: o.GetTask()}
	})

	v1 := Event{Kind: "moved", Payload: rawPayload(t, `{"path":"/a.md","task":"FT-1"}`)}
	if got, err := decodeIn[*PlanSupersededPayload](r, v1); err != nil || got.GetFrom() != "/a.md" {
		t.Errorf("v1 row: %v, %v", got, err)
	}

	e, err := r.event("moved", &PlanSupersededPayload{From: "/b.md"})
	if err != nil || storedKeys(t, e)["v"] != float64(2) {
		t.Fatalf("v2 row %+v, %v", e, err)
	}

	if got, err := decodeIn[*PlanSupersededPayload](r, e); err != nil || got.GetFrom() != "/b.md" {
		t.Errorf("v2 row: %v, %v", got, err)
	}

	if _, err := r.event("moved", &PlanSavedPayload{}); err == nil {
		t.Error("a v1 payload was written after the bump")
	}

	v3 := Event{Kind: "moved", Payload: rawPayload(t, `{"v":3,"from":"/c.md","extra":1}`)}

	got, err := decodeIn[*PlanSupersededPayload](r, v3)
	if !errors.Is(err, ErrNewerVersion) || got.GetFrom() != "/c.md" {
		t.Errorf("v3 row: %v, %v", got, err)
	}
}

// A kind whose old versions only added keys keeps stamping its number, and
// reads a row of each version with its one message.
func TestAdditiveVersionsShareAMessage(t *testing.T) {
	e := MustEvent(RunStarted, &RunStartedPayload{Skill: "ship", Model: "m"})
	if storedKeys(t, e)["v"] != float64(2) {
		t.Errorf("run_started stored %v", storedKeys(t, e))
	}

	v1 := Event{Kind: RunStarted, Payload: rawPayload(t, `{"skill":"plan"}`)}
	if got, err := Decode[*RunStartedPayload](v1); err != nil || got.GetSkill() != "plan" {
		t.Errorf("v1 row: %v, %v", got, err)
	}
}

// Stored bytes are compact and open with v, whatever whitespace protojson
// chose, and every scalar key is written, empty or not.
func TestStoredPayloadIsCompact(t *testing.T) {
	e := MustEvent(Committed, &CommittedPayload{Branch: "b", Sha: "abc"})

	raw, err := json.Marshal(e.Payload.Val)
	if err != nil {
		t.Fatal(err)
	}

	if want := `{"v":1,"branch":"b","sha":"abc","subject":""}`; string(raw) != want {
		t.Errorf("stored %s, want %s", raw, want)
	}
}

// protojson refuses invalid UTF-8, which encoding/json replaced: the row is
// still written, the bad bytes replaced, and the caller's message untouched.
func TestInvalidUTF8IsReplaced(t *testing.T) {
	p := &CommittedPayload{Subject: "fix \xff", Branch: "b"}

	e, err := NewEvent(Committed, p)
	if err != nil {
		t.Fatal(err)
	}

	if got := storedKeys(t, e)["subject"]; got != "fix \uFFFD" {
		t.Errorf("stored subject %q", got)
	}

	if p.GetSubject() != "fix \xff" {
		t.Error("the caller's message changed")
	}

	m := MustEvent(ReviewRoundOpened, &ReviewRoundOpenedPayload{Categories: map[string]int32{"a\xff": 1}})
	if cats, _ := storedKeys(t, m)["categories"].(map[string]any); cats["a\uFFFD"] != float64(1) {
		t.Errorf("stored categories %v", cats)
	}
}

// An ask row the structs wrote stores an unset enum as "", which protojson
// refuses on its own; it reads as unspecified, nested or not.
func TestEmptyEnumReadsAsUnspecified(t *testing.T) {
	answered := Event{
		Kind:    AskAnswered,
		Payload: rawPayload(t, `{"v":1,"id":"k","answer":"","answered_by":"","delivered":false}`),
	}
	if got, err := Decode[*AskAnsweredPayload](answered); err != nil || got.GetId() != "k" {
		t.Errorf("ask_answered: %v, %v", got, err)
	}

	raised := Event{
		Kind:    AskRaised,
		Payload: rawPayload(t, `{"v":1,"ask":{"id":"k","answered_by":"","questions":[{"n":1}]}}`),
	}
	if got, err := Decode[*AskRaisedPayload](raised); err != nil || got.GetAsk().GetId() != "k" {
		t.Errorf("ask_raised: %v, %v", got, err)
	}
}

// A row that does not decode, or of another kind, still yields a message,
// so a reader that ignores the error reads zero values.
func TestDecodeNeverReturnsNil(t *testing.T) {
	bad := Event{Kind: Committed, Payload: rawPayload(t, `{"sha":1}`)}
	if got, err := Decode[*CommittedPayload](bad); err == nil || got == nil {
		t.Errorf("bad row: %v, %v", got, err)
	}

	other := MustEvent(StateNote, &ClaimPayload{})
	if got, err := Decode[*CommittedPayload](other); err == nil || got == nil {
		t.Errorf("other kind: %v, %v", got, err)
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

// docs/ledger.schema.json is what the payload messages generate.
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
			"docs/ledger.schema.json drifted from the payload messages: run `go run ./cmd/f10 ledger schema > ../docs/ledger.schema.json` in cli/",
		)
	}
}
