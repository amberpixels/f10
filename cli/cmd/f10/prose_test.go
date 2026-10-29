package main

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// The plugin prose tells agents which commands to run, so a verb the binary
// lacks, or a raw host CLI call nobody listed, fails here instead of mid-run.

var (
	proseDirs  = []string{"steps", "skills", "conventions", "modes"}
	codeSpanRe = regexp.MustCompile("`([^`]+)`")
	// a word boundary before the binary: `bin/f10` and `f10-state.sh` are not it
	f10CallRe  = regexp.MustCompile(`(?:^|[\s(;|&"])f10((?: [a-z][a-z-]*)+)`)
	hostCallRe = regexp.MustCompile(`(?:^|[\s(;|&"])(gh|glab)((?: [a-z][a-z-]*){1,2})`)
)

// allowNotCommand holds `file: phrase` code spans that start with f10 but
// name no command.
var allowNotCommand = map[string]string{
	"steps/review.md: f10 never performs it": "prose: what a manual pick name means",
}

const (
	adapterExample = "an example of a project adapter, which stays"
	movesToBinary  = "FT-254: moves into an f10 command"
)

// hostCLIAllowlist is every raw gh or glab call the prose may make, keyed
// `file: gh pr diff`. An entry whose call left the prose fails too, so the
// list only shrinks as the calls move into the binary.
var hostCLIAllowlist = map[string]string{
	"conventions/context.md: gh issue create": adapterExample,
	"conventions/context.md: gh issue view":   adapterExample,
	"conventions/context.md: gh pr create":    adapterExample,
	"conventions/context.md: gh pr edit":      adapterExample,
	"conventions/context.md: glab mr create":  adapterExample,
	"conventions/context.md: glab mr update":  adapterExample,
	"modes/dry-run.md: gh issue view":         adapterExample,
	"steps/fetch.md: gh issue view":           adapterExample,
	"skills/resolve/SKILL.md: gh pr comment":  movesToBinary,
	"skills/resolve/SKILL.md: glab mr note":   movesToBinary,
	"steps/capture.md: gh api repos":          movesToBinary,
	"steps/capture.md: gh issue create":       movesToBinary,
	"steps/capture.md: glab api":              movesToBinary,
	"steps/capture.md: glab issue create":     movesToBinary,
	"steps/catchup.md: gh pr edit":            movesToBinary,
	"steps/catchup.md: gh pr view":            movesToBinary,
	"steps/catchup.md: glab mr update":        movesToBinary,
	"steps/catchup.md: glab mr view":          movesToBinary,
	"steps/judge.md: gh api repos":            movesToBinary,
	"steps/judge.md: gh pr comment":           movesToBinary,
	"steps/judge.md: gh pr view":              movesToBinary,
	"steps/judge.md: glab api projects":       movesToBinary,
	"steps/judge.md: glab mr note":            movesToBinary,
	"steps/judge.md: glab mr view":            movesToBinary,
	"steps/pr.md: gh api user":                movesToBinary,
	"steps/pr.md: gh pr create":               movesToBinary,
	"steps/pr.md: gh pr edit":                 movesToBinary,
	"steps/pr.md: gh pr view":                 movesToBinary,
	"steps/pr.md: glab api user":              movesToBinary,
	"steps/pr.md: glab mr create":             movesToBinary,
	"steps/pr.md: glab mr update":             movesToBinary,
	"steps/pr.md: glab mr view":               movesToBinary,
	"steps/resolve.md: gh api repos":          movesToBinary,
	"steps/resolve.md: gh pr comment":         movesToBinary,
	"steps/resolve.md: gh pr view":            movesToBinary,
	"steps/resolve.md: glab api projects":     movesToBinary,
	"steps/resolve.md: glab mr note":          movesToBinary,
	"steps/resolve.md: glab mr view":          movesToBinary,
}

type codeText struct {
	file string // relative to the plugin root
	line int
	text string
}

// proseCode returns the code in the plugin prose: every inline code span,
// and every line of a fenced block.
func proseCode(t *testing.T) []codeText {
	t.Helper()

	root := filepath.Join("..", "..", "..")

	var out []codeText

	for _, dir := range proseDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
				return err
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			fenced := false

			for i, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "```") {
					fenced = !fenced
					continue
				}

				if fenced {
					out = append(out, codeText{rel, i + 1, line})
					continue
				}

				for _, m := range codeSpanRe.FindAllStringSubmatch(line, -1) {
					out = append(out, codeText{rel, i + 1, m[1]})
				}
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	return out
}

// resolveCommand walks words down the tree. Under a leaf with its own Action
// the next word is a positional argument, which ends the walk; under a command
// with subcommands it must be one of them, so a typo fails.
func resolveCommand(root *cli.Command, words []string) string {
	cmd := root

	for i, w := range words {
		sub := findSub(cmd, w)
		if sub != nil {
			cmd = sub
			continue
		}

		if i > 0 && cmd.Action != nil && len(cmd.Commands) == 0 {
			return ""
		}

		return "no " + strings.Join(append([]string{"f10"}, words[:i+1]...), " ")
	}

	return ""
}

func findSub(c *cli.Command, name string) *cli.Command {
	for _, sub := range c.Commands {
		if sub.HasName(name) {
			return sub
		}
	}

	return nil
}

func TestProseNamesRealCommands(t *testing.T) {
	root := newApp()
	used := map[string]bool{}

	for _, c := range proseCode(t) {
		for _, m := range f10CallRe.FindAllStringSubmatch(c.text, -1) {
			words := strings.Fields(m[1])

			miss := resolveCommand(root, words)
			if miss == "" {
				continue
			}

			key := c.file + ": f10 " + strings.Join(words, " ")
			if _, ok := allowNotCommand[key]; ok {
				used[key] = true
				continue
			}

			t.Errorf("%s:%d: %s (`%s`)", c.file, c.line, miss, strings.TrimSpace(c.text))
		}
	}

	for key := range allowNotCommand {
		if !used[key] {
			t.Errorf("allowNotCommand: %q no longer appears; delete it", key)
		}
	}
}

func TestProseHostCLICallsListed(t *testing.T) {
	found := map[string]string{}

	for _, c := range proseCode(t) {
		for _, m := range hostCallRe.FindAllStringSubmatch(c.text, -1) {
			key := c.file + ": " + m[1] + m[2]
			if _, seen := found[key]; !seen {
				found[key] = c.file + ":" + strconv.Itoa(c.line)
			}
		}
	}

	for _, key := range slices.Sorted(maps.Keys(found)) {
		if _, ok := hostCLIAllowlist[key]; !ok {
			t.Errorf("%s: raw host CLI call %q; name an f10 command instead", found[key], key)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(hostCLIAllowlist)) {
		if _, ok := found[key]; !ok {
			t.Errorf("hostCLIAllowlist: %q no longer appears; delete it", key)
		}
	}
}
