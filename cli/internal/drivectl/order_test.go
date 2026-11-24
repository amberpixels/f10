package drivectl_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
)

// bases renders each task's typed bases, | between tasks, - for none.
func bases(a drivectl.DriveArgs) string {
	out := make([]string, 0, len(a.Bases))
	for _, b := range a.Bases {
		if len(b) == 0 {
			out = append(out, "-")

			continue
		}

		out = append(out, strings.Join(b, "@"))
	}

	return strings.Join(out, "|")
}

// A comma chains entries and @ names bases. A plus stays a bundle, and the
// profile comes last.
func TestParseOrder(t *testing.T) {
	cases := []struct {
		line, tasks, profiles, bases string
	}{
		{"A-1,A-2", "A-1 A-2", "|", "-|A-1"},
		{"A-1,A-2,A-3", "A-1 A-2 A-3", "||", "-|A-1|A-2"},
		{"A-1+A-2,A-3", "A-1+A-2 A-3", "|", "-|A-1+A-2"},
		{"A-1,A-2 C-1@A-1", "A-1 A-2 C-1", "||", "-|A-1|A-1"},
		{"A-1 B-1 C-1@A-1@B-1", "A-1 B-1 C-1", "||", "-|-|A-1@B-1"},
		{"A-1,A-2@C-1", "A-1 A-2", "|", "-|A-1@C-1"},
		{"C-1@A-1:opus", "C-1", "opus", "A-1"},
		{"A-1-x,B-1", "A-1-x B-1", "|", "-|A-1"},
		{"A-1:claude:opus:high,B-1@C-1:sonnet", "A-1 B-1", "claude:opus:high|sonnet", "-|A-1@C-1"},
		{"https://github.com/o/r/issues/2:opus", "https://github.com/o/r/issues/2", "opus", "-"},
	}

	for _, c := range cases {
		a, err := drivectl.ParseDriveArgs(strings.Fields(c.line))
		if err != nil {
			t.Errorf("%s: %v", c.line, err)

			continue
		}

		if got := strings.Join(a.Tasks, " "); got != c.tasks {
			t.Errorf("%s: tasks %q, want %q", c.line, got, c.tasks)
		}

		if got := strings.Join(a.Profiles, "|"); got != c.profiles {
			t.Errorf("%s: profiles %q, want %q", c.line, got, c.profiles)
		}

		if got := bases(a); got != c.bases {
			t.Errorf("%s: bases %q, want %q", c.line, got, c.bases)
		}
	}
}

func TestParseOrderRefusals(t *testing.T) {
	for line, want := range map[string]string{
		"A-1,,A-2":              "an empty entry",
		"A-1,":                  "an empty entry",
		",A-1":                  "an empty entry",
		"C-1@":                  "names no base",
		"@A-1":                  "names no task",
		"C-1@A-1-x":             "with no -x",
		"C-1@A-1@A-1":           "as a base twice",
		"C-1:opus@A-1":          "the profile comes last: C-1@A-1:opus",
		"C-1@A-1 --after B-1":   "give it one way",
		"C-1@A-1 --base main":   "give it one way",
		"A-1,A-2 --after B-1":   "start that task alone",
		"A-1@B-1 -- A-4":        "a range takes no order",
		"A-1,A-2 --propose":     "sets the order itself",
		"A-1 C-1@A-1 --hint":    "sets the order itself",
		"A-1,A-2 --discuss":     "proposes the order itself",
		"B-1 C-1@B-1 --discuss": "proposes the order itself",
	} {
		_, err := drivectl.ParseStart(strings.Fields(line))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", line, err, want)
		}
	}

	// a note after the ids may hold a comma
	sa, err := drivectl.ParseStart(strings.Fields("A-1 --discuss read this, then wait"))
	if err != nil || sa.Kind != drivectl.DiscussStart || sa.Once.Note != "read this, then wait" {
		t.Errorf("a discussion with a comma in its note = %+v, %v", sa, err)
	}
}

// No part of the notation needs quoting: each shell hands every word back
// as typed, extendedglob included, since zsh users often set it. A #n id
// is left out: a shell reads # at a word's start as a comment either way.
func TestOrderNeedsNoQuoting(t *testing.T) {
	words := "A-1,A-3 B-1,B-2 C-1@A-3@B-2 A-1+A-2,A-3 FT-1-x@FT-2:claude:opus:high"
	want := strings.Join(strings.Fields(words), "\n") + "\n"

	shells := map[string][]string{
		"bash":             {"bash", "-c", "printf '%s\\n' " + words},
		"zsh":              {"zsh", "-f", "-c", "printf '%s\\n' " + words},
		"zsh extendedglob": {"zsh", "-f", "-c", "setopt extendedglob; printf '%s\\n' " + words},
	}

	for name, argv := range shells {
		if _, err := exec.LookPath(argv[0]); err != nil {
			t.Logf("%s: not on PATH, skipped", argv[0])

			continue
		}

		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err != nil || string(out) != want {
			t.Errorf("%s: %q, %v; want every word as typed", name, out, err)
		}
	}
}
