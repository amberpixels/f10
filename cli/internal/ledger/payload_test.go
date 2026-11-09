package ledger

import (
	"context"
	"os"
	"slices"
	"testing"
)

// Each accessor reads a payload as built in memory and as stored.
func TestPayloadAccessors(t *testing.T) {
	built := Payload{
		"s": "x", "n": 3, "list": []string{"a", "b"}, "mixed": []any{"a", 1, "b"},
		"yes": true, "no": false, "num": "3",
	}

	path := tempPath(t)
	openT(t, path).Append(context.Background(), Event{Kind: RunStarted, Source: Observed, Payload: col(built)})
	stored := list(t, path, Filter{})[0].Payload.Val

	for name, p := range map[string]Payload{"built": built, "stored": stored} {
		if p.String("s") != "x" || p.String("n") != "" || p.String("absent") != "" {
			t.Errorf("%s: String", name)
		}

		if n, ok := p.Int("n"); !ok || n != 3 {
			t.Errorf("%s: Int(n) = %d, %v", name, n, ok)
		}

		if _, ok := p.Int("num"); ok {
			t.Errorf("%s: Int read a string", name)
		}

		if _, ok := p.Int("absent"); ok {
			t.Errorf("%s: Int read an absent key", name)
		}

		if got := p.Strings("list"); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("%s: Strings(list) = %v", name, got)
		}

		if got := p.Strings("mixed"); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("%s: Strings(mixed) = %v", name, got)
		}

		if p.Strings("s") != nil || p.Strings("absent") != nil {
			t.Errorf("%s: Strings read a non-list", name)
		}

		if !p.Bool("yes") || p.Bool("no") || p.Bool("s") {
			t.Errorf("%s: Bool", name)
		}
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
