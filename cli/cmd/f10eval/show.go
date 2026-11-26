package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// showMain prints a run's timeline from its folder's transcript: a heading
// per turn and per skill, then one line per tool call with its elapsed time
// and the running cost.
func showMain(w io.Writer, root string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: f10eval show <report.json>")
	}

	path := resolveDir(root, args[0])

	rep, err := readReport(path)
	if err != nil {
		return err
	}

	if rep.RunDir == "" {
		return fmt.Errorf("%s predates run folders: rerun the scenario to get its timeline", args[0])
	}

	folder := filepath.Join(filepath.Dir(path), rep.RunDir)

	events, at, err := readTranscript(folder)
	if err != nil {
		return err
	}

	// the prompts name the skill a turn starts with; without them the
	// timeline still prints, from the Skill calls alone
	var s Scenario

	if body, err := os.ReadFile(filepath.Join(folder, inputsDir, "scenario.json")); err == nil {
		_ = json.Unmarshal(body, &s)
	}

	writeTimeline(w, newStyles(w), rep, s.turns(), events, at)

	return nil
}

// A call is one tool call as the timeline prints it.
type call struct {
	at   int64
	cost float64
	est  bool
	sub  bool
	line string
	err  bool
}

func writeTimeline(w io.Writer, st *styles, rep Report, prompts []string, events []event, at []int64) {
	name := rep.Scenario
	if rep.Agent != "" {
		name += " (" + rep.Agent + ")"
	}

	fmt.Fprintf(w, "%s %s  $%.2f  %s\n", st.mark(rep.Pass), st.title.Render(name), rep.CostUSD,
		(time.Duration(rep.DurationMS) * time.Millisecond).Round(time.Second))

	// a tool's result arrives after its call, so lines print once the
	// stream is read and an errored result can mark its call
	var (
		lines []string
		calls = map[string]*call{}
		order []*call
		prog  progress
		turn  = 1
		skill string
	)

	flush := func() {
		for _, c := range order {
			mark := ""
			if c.err {
				mark = "  " + st.fail.Render("✗")
			}

			money := fmt.Sprintf("$%.2f", c.cost)
			if c.est {
				money = "~" + money
			}

			sub := ""
			if c.sub {
				sub = st.dim.Render("sub ")
			}

			lines = append(lines, fmt.Sprintf("    %s  %s  %s%s%s", st.timer.Render(clock(c.at)),
				st.cost.Render(money), sub, c.line, mark))
		}

		order = order[:0]
	}

	startTurn := func() {
		lines = append(lines, st.title.Render(fmt.Sprintf("turn %d", turn)))

		if turn <= len(prompts) {
			prog.beginTurn(prompts[turn-1])
		}

		if prog.Skill != "" {
			skill = prog.Skill
			lines = append(lines, "  "+st.title.Render(skill))
		}
	}

	startTurn()

	for i, e := range events {
		prog.apply(e)
		cost, est := prog.CostUSD()

		for _, b := range e.tools() {
			if prog.Skill != skill && b.Name == "Skill" {
				flush()

				skill = prog.Skill
				lines = append(lines, "  "+st.title.Render(skill))
			}

			c := &call{at: at[i], cost: cost, est: est, sub: e.subagent(), line: prog.toolLine(b)}
			calls[b.ID], order = c, append(order, c)
		}

		if e.Type == "user" && e.Message != nil {
			for _, b := range e.Message.Content {
				if c := calls[b.ToolUseID]; c != nil && b.Type == "tool_result" && b.IsError {
					c.err = true
				}
			}
		}

		if e.Type == "result" {
			flush()

			first, _, _ := strings.Cut(strings.TrimSpace(e.Result), "\n")
			lines = append(lines, fmt.Sprintf("  %s  %s  %s  %s", st.dim.Render("end"),
				st.cost.Render(fmt.Sprintf("$%.2f", e.CostUSD)),
				(time.Duration(e.DurationMS)*time.Millisecond).Round(time.Second), first))

			if i < len(events)-1 {
				turn++
				skill, prog.Skill = "", ""

				startTurn()
			}
		}
	}

	flush()

	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// clock is milliseconds as m:ss.
func clock(ms int64) string {
	d := time.Duration(ms) * time.Millisecond

	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}
