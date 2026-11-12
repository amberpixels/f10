package testkit

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
)

func Raised(t *testing.T, dir string, a asks.Ask) asks.Ask {
	t.Helper()

	if a.Text == "" {
		a.Text = "1. Merge now? [yes | wait] 2. Which port?"
	}

	got, _, err := asks.Raise(dir, a, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	return got
}
