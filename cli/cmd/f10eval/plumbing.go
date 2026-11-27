package main

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// Plumbing is the part of a run spent on f10's own machinery rather than on
// the task. Seconds and CostUSD come from the transcript's line times and the
// running cost estimate, so they are a share of the run, not exact figures.
type Plumbing struct {
	Calls      int     `json:"calls"`
	TotalCalls int     `json:"total_calls"`
	Seconds    float64 `json:"seconds"`
	CostUSD    float64 `json:"cost_usd"`
	// RunSeconds is the transcript's span on the same clock as Seconds, so
	// the share never mixes it with claude's duration, which drops a killed turn.
	RunSeconds float64 `json:"run_seconds"`
}

// Share is the plumbing's percentage of the run's time, 0 when unmeasured.
func (p Plumbing) Share() float64 {
	if p.RunSeconds <= 0 {
		return 0
	}

	return p.Seconds / p.RunSeconds * 100
}

// The plumbing rules, which docs/evals.md lists. A command whose first word
// is one of plumbingScripts, or f10 followed by one of plumbingSubcommands,
// is plumbing; a Bash call is plumbing only when every command it chains is.
var (
	plumbingScripts     = []string{"f10-load-*.sh", "f10-state.sh"}
	plumbingSubcommands = []string{"help", "forward", "lore", "trace"}
	plumbingDirs        = []string{"/modes/", "/steps/", "/conventions/"}
)

// captured is `name="$(cmd)"`, judged by cmd, as a commit captures its trailers
var captured = regexp.MustCompile(`^\w+="?\$\((.*)\)"?$`)

// isPlumbing says whether one tool call is f10 plumbing. cwd is the run's
// repo, so a Read of the repo's own steps/ stays work.
func isPlumbing(b eventBlock, cwd string) bool {
	in := toolInput(b.Input)

	switch b.Name {
	case "Skill":
		return strings.HasPrefix(strings.TrimPrefix(in["skill"], "/"), "f10:")
	case "Read":
		file := in["file_path"]
		if file == "" || (cwd != "" && strings.HasPrefix(file, cwd+"/")) {
			return false
		}

		for _, d := range plumbingDirs {
			if strings.Contains(file, d) {
				return true
			}
		}

		return false
	case "Bash":
		return plumbingCommand(in["command"])
	}

	return false
}

func plumbingCommand(cmd string) bool {
	seen := false

	for _, seg := range splitChain(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}

		if m := captured.FindStringSubmatch(seg); m != nil {
			seg = strings.TrimSpace(m[1])
		}

		if !plumbingSegment(seg) {
			return false
		}

		seen = true
	}

	return seen
}

// splitChain cuts a shell line at ;, &&, || and newlines outside quotes. A
// pipe outside quotes makes the whole line one unsplittable segment, so a
// pipeline always counts as work.
func splitChain(cmd string) []string {
	var (
		out   []string
		start int
		quote byte
	)

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' {
				i++
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\':
			i++
		case c == ';' || c == '\n':
			out, start = append(out, cmd[start:i]), i+1
		case (c == '&' || c == '|') && i+1 < len(cmd) && cmd[i+1] == c:
			out, start = append(out, cmd[start:i]), i+2
			i++
		case c == '|':
			return []string{"|"}
		}
	}

	return append(out, cmd[start:])
}

func plumbingSegment(seg string) bool {
	words := strings.Fields(seg)
	if len(words) == 0 {
		return false
	}

	name := path.Base(words[0])

	for _, pat := range plumbingScripts {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}

	return name == "f10" && len(words) > 1 && slices.Contains(plumbingSubcommands, words[1])
}

// plumbingOf measures a run's plumbing from its events and their arrival
// times, and returns the plumbing calls' ids. Each event owns the time and
// estimated cost since the event before it, so parallel calls never count
// twice. An assistant event is plumbing when its message's calls all are,
// and a tool result when the call it answers is.
func plumbingOf(events []event, at []int64) (Plumbing, map[string]bool) {
	var (
		p     Plumbing
		cwd   string
		ids   = map[string]bool{}
		msgOK = map[string]bool{} // a message's calls are all plumbing
	)

	for _, e := range events {
		if e.Type == "system" && e.Subtype == "init" && e.Cwd != "" {
			cwd = e.Cwd
		}

		for _, b := range e.tools() {
			p.TotalCalls++

			plumb := isPlumbing(b, cwd)
			if plumb {
				p.Calls++
				ids[b.ID] = true
			}

			id := e.Message.ID
			if ok, seen := msgOK[id]; !seen || ok {
				msgOK[id] = plumb
			}
		}
	}

	var (
		prog     progress
		lastCost float64
		lastAt   int64
	)

	for i, e := range events {
		prog.apply(e)
		cost, _ := prog.CostUSD()

		if plumbingEvent(e, ids, msgOK) {
			p.Seconds += float64(at[i]-lastAt) / 1000
			p.CostUSD += cost - lastCost
		}

		lastCost, lastAt = cost, at[i]
	}

	p.RunSeconds = float64(lastAt) / 1000

	return p, ids
}

func plumbingEvent(e event, ids, msgOK map[string]bool) bool {
	switch {
	case e.Type == "assistant" && e.Message != nil:
		return msgOK[e.Message.ID]
	case e.Type == "user" && e.Message != nil:
		answered := false

		for _, b := range e.Message.Content {
			if b.Type == "tool_result" {
				if !ids[b.ToolUseID] {
					return false
				}

				answered = true
			}
		}

		return answered
	}

	return false
}

// folderPlumbing measures a run folder's transcript, nil when it has none.
func folderPlumbing(dir string) *Plumbing {
	events, at, err := readTranscript(dir)
	if err != nil || len(events) == 0 {
		return nil
	}

	p, _ := plumbingOf(events, at)

	return &p
}
