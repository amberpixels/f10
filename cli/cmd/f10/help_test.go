package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func runHelpOutput(t *testing.T, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	if err := app.Run(t.Context(), append([]string{"f10"}, args...)); err != nil {
		t.Fatalf("f10 %s: %v", strings.Join(args, " "), err)
	}

	return out.String()
}

// listed reports whether a help listing has a command row named name.
func listed(help, name string) bool {
	for line := range strings.SplitSeq(help, "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.TrimSuffix(f[0], ",") == name {
			return true
		}
	}

	return false
}

func walkCommands(c *cli.Command, path string, fn func(path string, c *cli.Command)) {
	for _, sub := range c.Commands {
		p := path + " " + sub.Name
		fn(p, sub)
		walkCommands(sub, p, fn)
	}
}

func TestHelpTiers(t *testing.T) {
	human := runHelpOutput(t, "--help")
	all := runHelpOutput(t, "help", "--all")

	for _, c := range newApp().Commands {
		switch {
		case isPlumbing(c):
			if listed(human, c.Name) {
				t.Errorf("f10 --help lists plumbing %q", c.Name)
			}

			if !listed(all, c.Name) {
				t.Errorf("f10 help --all misses plumbing %q", c.Name)
			}
		case isInternal(c):
			if listed(human, c.Name) || listed(all, c.Name) {
				t.Errorf("internal %q is listed", c.Name)
			}
		default:
			if !listed(human, c.Name) {
				t.Errorf("f10 --help misses %q", c.Name)
			}
		}
	}

	// nested plumbing has no category heading of its own, so its full path is listed
	walkCommands(newApp(), "", func(path string, c *cli.Command) {
		if p := strings.TrimSpace(path); strings.Contains(p, " ") && isPlumbing(c) &&
			!strings.Contains(all, "   "+p+"  ") {
			t.Errorf("f10 help --all misses nested plumbing %q", p)
		}
	})

	if !strings.Contains(all, plumbingCategory+":") {
		t.Errorf("f10 help --all has no %s: heading:\n%s", plumbingCategory, all)
	}
}

// A plumbing verb is Hidden and categorised together: Hidden alone makes it
// internal, a category alone lists it beside the human commands.
func TestPlumbingMarkedWhole(t *testing.T) {
	walkCommands(newApp(), "f10", func(path string, c *cli.Command) {
		if c.Category != "" && c.Category != plumbingCategory {
			t.Errorf("%s: category %q, want none or %q", path, c.Category, plumbingCategory)
		}

		if c.Category == plumbingCategory && !c.Hidden {
			t.Errorf("%s: plumbing but not Hidden", path)
		}
	})
}

func TestAgentReference(t *testing.T) {
	ref := runHelpOutput(t, "help", "--agent")

	lines := map[string]bool{}
	for line := range strings.SplitSeq(ref, "\n") {
		if cmd, _, ok := strings.Cut(line, " - "); ok {
			lines[cmd] = true
		}
	}

	has := func(path string) bool {
		for l := range lines {
			if l == path || strings.HasPrefix(l, path+" ") {
				return true
			}
		}

		return false
	}

	var internal []string

	walkCommands(newApp(), "f10", func(path string, c *cli.Command) {
		if isInternal(c) {
			internal = append(internal, path)
			return
		}

		for _, p := range internal {
			if strings.HasPrefix(path, p+" ") {
				return
			}
		}

		if c.Action != nil && c.Name != "help" && !has(path) {
			t.Errorf("f10 help --agent has no line for %q", path)
		}
	})

	for _, p := range internal {
		if has(p) {
			t.Errorf("f10 help --agent lists internal %q", p)
		}
	}

	// a required flag stands outside the brackets, an alias beside its name
	want := "f10 pr status [PR | task-id] --name --state --description|-m [--sha]"
	if !lines[want] {
		t.Errorf("f10 help --agent has no line %q", want)
	}
}
