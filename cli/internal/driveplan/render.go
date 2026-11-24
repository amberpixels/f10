package driveplan

import (
	"fmt"
	"io"
	"strings"
)

// Tradeoff is said once under a proposal holding a bundle.
const Tradeoff = "a bundle is one PR: one revert, one review over a mixed diff, " +
	"and a later bug is traced to its lead with the riders named"

// Command is the start command line the proposal runs, extra flags appended.
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

// Write prints the proposal: the levels, the order with its reasons, the
// command, the saving, and the trade-off once.
func (p Plan) Write(w io.Writer, notes []string, extra ...string) error {
	var b strings.Builder

	width := 0
	for _, st := range p.Levels {
		for _, e := range st.Entries {
			width = max(width, len(e.Token()))
		}
	}

	for n, st := range p.Levels {
		for k, e := range st.Entries {
			label := ""
			if k == 0 {
				label = fmt.Sprintf("level %d", n+1)
			}

			fmt.Fprintf(&b, "%-9s %-*s  %s\n", label, width, e.Token(), e.Reason)
		}
	}

	for _, note := range notes {
		fmt.Fprintf(&b, "%-9s %s\n", "", note)
	}

	p.writeOrder(&b)

	for _, line := range p.Unordered {
		fmt.Fprintf(&b, "%-9s %s\n", "", line)
	}

	fmt.Fprintf(&b, "\n%s\n", p.Command(extra...))

	if s := p.Saving(); s != "" {
		b.WriteString(s + "\n")
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

// writeOrder prints a row per base, so the reader sees why each @ and , in
// the command is there. A dropped base says why the join was cut.
func (p Plan) writeOrder(b *strings.Builder) {
	type row struct{ pair, reason string }

	var rows []row

	for _, st := range p.Levels {
		for _, e := range st.Entries {
			for _, base := range e.Bases {
				pair := strings.Join(e.IDs, "+") + " after " + base.ID
				if base.Dropped {
					pair = strings.Join(e.IDs, "+") + " not after " + base.ID
				}

				rows = append(rows, row{pair, base.Reason})
			}
		}
	}

	if len(rows) == 0 {
		return
	}

	width := 0
	for _, r := range rows {
		width = max(width, len(r.pair))
	}

	b.WriteString("\n")

	for n, r := range rows {
		label := ""
		if n == 0 {
			label = "order"
		}

		fmt.Fprintf(b, "%-9s %-*s  %s\n", label, width, r.pair, r.reason)
	}
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
