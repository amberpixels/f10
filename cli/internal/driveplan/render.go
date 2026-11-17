package driveplan

import (
	"fmt"
	"io"
	"strings"
)

// Tradeoff is said once under a proposal holding a bundle.
const Tradeoff = "a bundle is one PR: one revert, one review over a mixed diff, " +
	"and a later bug is traced to its lead with the riders named"

// Command is the drive command line the plan runs, extra flags appended.
func (p Plan) Command(extra ...string) string {
	return "f10 start " + strings.Join(append(append([]string{}, p.Args...), extra...), " ")
}

// Saving is the line naming what bundling saves, "" when it saves nothing.
func (p Plan) Saving() string {
	if p.Finishes == 0 {
		return ""
	}

	return fmt.Sprintf("saves about %s and %s against starting them one by one",
		count(p.Waits, "CI wait"), count(p.Finishes, "finish"))
}

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}

	if strings.HasSuffix(what, "sh") {
		return fmt.Sprintf("%d %ses", n, what)
	}

	return fmt.Sprintf("%d %ss", n, what)
}

// Write prints the proposal: the stages, the command, the saving, the
// After: edits waiting on a yes, and the trade-off once.
func (p Plan) Write(w io.Writer, notes []string, extra ...string) error {
	var b strings.Builder

	width := 0
	for _, st := range p.Stages {
		for _, e := range st.Entries {
			width = max(width, len(e.Token()))
		}
	}

	for n, st := range p.Stages {
		for k, e := range st.Entries {
			label := ""
			if k == 0 {
				label = fmt.Sprintf("stage %d", n+1)
			}

			fmt.Fprintf(&b, "%-9s %-*s  %s\n", label, width, e.Token(), e.Reason)
		}
	}

	for _, note := range notes {
		fmt.Fprintf(&b, "%-9s %s\n", "", note)
	}

	fmt.Fprintf(&b, "\n%s\n", p.Command(extra...))

	if s := p.Saving(); s != "" {
		b.WriteString(s + "\n")
	}

	if len(p.Edits) > 0 {
		b.WriteString("\nAfter: lines to add to the tasks, with your yes:\n")

		for _, e := range p.Edits {
			fmt.Fprintf(&b, "  %s  After: %s  %s\n", e.Task, e.After, e.Reason)
		}
	}

	if p.Bundles > 0 {
		b.WriteString("\n" + Tradeoff + "\n")
	}

	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("writing the proposal: %w", err)
	}

	return nil
}

// Hint is the one line a plain start prints when the rules alone would save
// at least one CI wait, "" otherwise: the start command that saves them.
func (p Plan) Hint() string {
	f := p.Formed
	if f.Waits < 1 {
		return ""
	}

	return fmt.Sprintf("bundling %s into %s would save about %s: %s",
		count(f.Bundled, "task"), count(f.Bundles, "bundle"), count(f.Waits, "CI wait"), p.Command())
}
