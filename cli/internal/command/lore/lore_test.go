package lore_test

import (
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// lore keeps its own copy of the judge's outcomes so it imports nothing
// from f10; this is what keeps the copy honest.
func TestLoreOutcomesAreVerdicts(t *testing.T) {
	if !slices.Equal(lore.Outcomes, verdict.Verdicts) {
		t.Errorf("lore.Outcomes %q, verdict.Verdicts %q", lore.Outcomes, verdict.Verdicts)
	}
}

func TestLoreNote(t *testing.T) {
	store := &lore.Store{Root: t.TempDir()}

	want := "lore: no entries for FT-9: no intent, no decisions, no lessons"
	if got := lore.Note(store, "FT-9"); got != want {
		t.Errorf("empty: %q, want %q", got, want)
	}

	add := func(e lore.Entry) {
		t.Helper()

		e.Change, e.Human, e.Body = "FT-9", "dev@example.com", "why"
		if _, err := store.Add(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}

	anchor := lore.Anchor{Path: "a.go", Line: 1, Commit: "abcdef1"}
	add(lore.Entry{Kind: lore.KindDecision, Chosen: "x", Anchors: []lore.Anchor{anchor}})
	add(lore.Entry{Kind: lore.KindDecision, Chosen: "y", Anchors: []lore.Anchor{anchor}})

	if got, want := lore.Note(store, "FT-9"), "lore: no intent, 2 decisions, no lessons"; got != want {
		t.Errorf("decisions only: %q, want %q", got, want)
	}

	add(lore.Entry{Kind: lore.KindIntent})
	add(lore.Entry{Kind: lore.KindLesson, Anchors: []lore.Anchor{anchor}})

	if got, want := lore.Note(store, "FT-9"), "lore: intent, 2 decisions, 1 lesson"; got != want {
		t.Errorf("all kinds: %q, want %q", got, want)
	}
}
