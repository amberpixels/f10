package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestFinishRefusesOutsideHerdr(t *testing.T) {
	f := testkit.NewFakes(t)
	t.Setenv("HERDR_ENV", "")

	err := runApp(t, "finish", "42").err
	if !errors.Is(err, herdr.ErrNotInside) {
		t.Fatalf("err = %v, want ErrNotInside", err)
	}

	if !strings.Contains(err.Error(), "f10 start, forward, drive and finish run inside a Herdr session") {
		t.Errorf("the refusal does not name the commands that need Herdr: %v", err)
	}

	if len(f.Calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.Calls)
	}
}
