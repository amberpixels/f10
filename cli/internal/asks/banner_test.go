package asks_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/amberpixels/f10/cli/internal/asks"
)

// testkit sits above asks, so its -update flag is declared here too
var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// BannerCases are the asks the banner's goldens draw. The inbox's parity
// test draws the same asks through the walk.
func BannerCases() map[string]asks.Ask {
	// in the local zone, so the clock the header prints is stable
	at := time.Date(2026, 11, 22, 16, 0, 0, 0, time.Local)

	return map[string]asks.Ask{
		"single": {
			Task: "FT-12", Skill: "ship", AskedAt: at,
			Text: "1. Hold the merge until the data copy finishes? " +
				"[hold - I merge after the copy | merge now - the app reads an empty volume until the copy runs]",
		},
		"several": {
			Task: "FT-12", Skill: "resolve", AskedAt: at,
			Text: "1. Reuse the saved plan? [reuse | re-plan] 2. Deploy to staging once the pull request is " +
				"green, before the review settles? [yes | no - wait for the review]",
		},
		"mixed": {
			Task: "FT-12", Skill: "resolve", AskedAt: at,
			Text: "1. Reuse the saved plan? [reuse | re-plan] 2. Which port should it listen on?",
		},
		"context": {
			Task: "FT-30", Skill: "ci", AskedAt: at,
			Context: "Tests are red on the pull request for a reason that comes from main, not from this change, " +
				"so a fix here would be reverted by the next catchup. Cause: the fixture key was removed on main. " +
				"Log: https://github.com/amberpixels/app/actions/runs/4455/job/1356.",
			Text: "1. What should happen with the red tests? [leave them red until main is fixed | fix them here]",
		},
		"recovered": {
			Task: "FT-50", Skill: "ship", AskedAt: at,
			Text: "1. The pull request is a draft and must not merge until the new volume exists and the data " +
				"is copied to /mnt/data. Hold the drive's merge until you finish the cutover? " +
				"Options: 1) hold, I merge after the cutover (recommended) 2) merge now",
		},
		"free": {Task: "FT-3", Text: "Which port should it listen on?"},
	}
}

func TestBannerGolden(t *testing.T) {
	t.Parallel()

	for name, a := range BannerCases() {
		got := asks.Banner(a, "")

		if strings.Contains(got, "\x1b") {
			t.Errorf("%s: banner holds an escape:\n%q", name, got)
		}

		for line := range strings.SplitSeq(got, "\n") {
			if utf8.RuneCountInString(line) > asks.BannerWidth && !strings.Contains(line, "https://") {
				t.Errorf("%s: line wider than %d: %q", name, asks.BannerWidth, line)
			}
		}

		bannerGolden(t, name, got)
	}
}

func TestBannerName(t *testing.T) {
	t.Parallel()

	got := asks.Banner(asks.Ask{Task: "FT-3", Text: "Which port?"}, "app:FT-3")
	if first, _, _ := strings.Cut(got, "\n"); first != asks.AskIcon+" app:FT-3" {
		t.Errorf("header = %q", first)
	}
}

// bannerGolden compares out with testdata/banner/<name>.golden, rewriting
// it under -update.
func bannerGolden(t *testing.T, name, out string) {
	t.Helper()

	path := filepath.Join("testdata", "banner", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if out != string(want) {
		t.Errorf("%s:\n%s\nwant:\n%s", name, out, want)
	}
}
