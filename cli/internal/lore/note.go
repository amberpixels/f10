package lore

import (
	"errors"
	"strconv"
	"strings"
)

// Note names each missing kind, so a change that reached finish without a
// ship run shows it; "" when lore cannot be read, since finish never fails
// on it.
func Note(store *Store, change string) string {
	entries, err := store.Effective(change)

	switch {
	case errors.Is(err, ErrNoRecord):
		return "lore: no entries for " + change + ": no intent, no decisions, no lessons"
	case err != nil:
		return ""
	}

	count := map[Kind]int{}
	for _, e := range entries {
		count[e.Kind]++
	}

	parts := []string{"no intent"}
	if count[KindIntent] > 0 {
		parts[0] = "intent"
	}

	for _, k := range []struct {
		kind      Kind
		one, many string
	}{{KindDecision, "decision", "decisions"}, {KindLesson, "lesson", "lessons"}} {
		if n := count[k.kind]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+plural(n, k.one, k.many))
		} else {
			parts = append(parts, "no "+k.many)
		}
	}

	return "lore: " + strings.Join(parts, ", ")
}
