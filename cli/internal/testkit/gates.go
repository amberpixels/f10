package testkit

import (
	"path/filepath"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/amberpixels/f10/cli/internal/lore"
)

// WriteLore writes entries as `f10 lore add` would leave them.
func WriteLore(t *testing.T, root string, entries ...lore.Entry) {
	t.Helper()

	for _, e := range entries {
		e.ID = ulid.MustNew(ulid.Timestamp(e.At), ulid.DefaultEntropy()).String()

		raw, err := lore.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}

		WriteFile(t, filepath.Join(root, e.Change, e.FileName()), string(raw))
	}
}
