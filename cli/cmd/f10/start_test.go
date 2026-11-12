package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Outside Herdr the command refuses before any git, wt or herdr call, and
// the message points at herdr.dev.
func TestStartRefusesOutsideHerdr(t *testing.T) {
	f := testkit.NewFakes(t)
	t.Setenv("HERDR_ENV", "")

	err := runApp(t, "start", "42").err
	if !errors.Is(err, herdr.ErrNotInside) {
		t.Fatalf("err = %v, want ErrNotInside", err)
	}

	if !strings.Contains(err.Error(), "https://herdr.dev") {
		t.Errorf("the refusal does not name herdr.dev: %v", err)
	}

	if len(f.Calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.Calls)
	}
}

func TestStartModesAreExclusive(t *testing.T) {
	testkit.NewFakes(t)

	for _, flags := range [][]string{{"--plan", "--local"}, {"--discuss", "--plan"}, {"--local", "--discuss"}} {
		args := append(append([]string{"start"}, flags...), "42")

		err := runApp(t, args...).err
		if err == nil || !strings.Contains(err.Error(), "exclusive") {
			t.Errorf("%v: err = %v, want the flags refused together", flags, err)
		}
	}
}

func TestStartBaseAndAfterAreExclusive(t *testing.T) {
	f := testkit.NewFakes(t)

	err := runApp(t, "start", "--base", "main", "--after", "7", "42").err
	if err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Errorf("err = %v, want the flags refused together", err)
	}

	if len(f.Calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.Calls)
	}
}
