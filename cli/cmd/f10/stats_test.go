package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// -C answers for the checkout it names, and a bare number resolves to the
// project's id as every other command reads it.
func TestStatsTakesTheCheckoutAndABareNumber(t *testing.T) {
	repo := testkit.TracedRepo(t, "main2", "")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	t.Chdir(t.TempDir())

	ctx := t.Context()
	project := ledger.Where(ctx, repo).Project

	var events []ledger.Event

	for i, e := range testkit.StatEvents(time.Now().Add(-2 * time.Hour)) {
		e.ID, e.Project = fmt.Sprintf("e%d", i), project
		e.Task = strings.Replace(e.Task, "FT-", "DM-", 1)
		events = append(events, e)
	}

	l, err := ledger.Open(ctx, ledger.Path(), time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, events...)
	_ = l.Close()

	out, err := runApp(t, "-C", repo, "stats", "1").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "DM-1") {
		t.Errorf("stats -C %s 1 found no DM-1:\n%s", repo, out)
	}
}
