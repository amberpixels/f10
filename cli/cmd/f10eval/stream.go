package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// An event is the part of one stream-json line the runner reads. Claude adds
// event types between releases, so a type it does not know is skipped, never
// an error.
type event struct {
	Type    string  `json:"type"`
	Subtype string  `json:"subtype"`
	Parent  *string `json:"parent_tool_use_id"`
	Cwd     string  `json:"cwd"` // the init event's
	Plugins []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"plugins"` // the init event's
	MCPServers []struct {
		Name string `json:"name"`
	} `json:"mcp_servers"` // the init event's
	Message *eventMessage `json:"message"`

	// the result event's fields, the ones --output-format json printed
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	IsError    bool    `json:"is_error"`
	Result     string  `json:"result"`
	SessionID  string  `json:"session_id"`
	NumTurns   int     `json:"num_turns"`
}

type eventMessage struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Content []eventBlock `json:"content"`
	Usage   *eventUsage  `json:"usage"`
}

// An eventBlock is a tool_use in an assistant message or a tool_result in a
// user one. Other blocks are read and ignored.
type eventBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

type eventUsage struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheWrite    int64 `json:"cache_creation_input_tokens"`
	CacheCreation *struct {
		FiveMin int64 `json:"ephemeral_5m_input_tokens"`
		OneHour int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (e event) subagent() bool { return e.Parent != nil && *e.Parent != "" }

func (e event) tools() []eventBlock {
	if e.Type != "assistant" || e.Message == nil {
		return nil
	}

	var out []eventBlock

	for _, b := range e.Message.Content {
		if b.Type == "tool_use" {
			out = append(out, b)
		}
	}

	return out
}

func (e event) turnResult() turnResult {
	return turnResult{
		CostUSD: e.CostUSD, DurationMS: e.DurationMS, IsError: e.IsError, Result: e.Result, SessionID: e.SessionID,
		NumTurns: e.NumTurns,
	}
}

// A turnResult is the part of claude's result event the runner reads.
type turnResult struct {
	CostUSD    float64
	DurationMS int64
	IsError    bool
	Result     string
	SessionID  string
	NumTurns   int
}

// runTurn runs one claude call, streaming its events into tr and to on as
// they arrive. An errored turn's result is claude's error, not a reply worth
// grading, so it comes back as the error.
func runTurn(ctx context.Context, dir string, env, args []string, tr *transcript, on func(event)) (turnResult, error) {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir, cmd.Env = dir, env

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return turnResult{}, err
	}

	if err := cmd.Start(); err != nil {
		return turnResult{}, fmt.Errorf("claude: %w", err)
	}

	res, got, rerr := readStream(stdout, tr, on)
	werr := cmd.Wait()

	switch {
	case got && res.IsError:
		return res, errors.New(lastLine(res.Result))
	case werr != nil:
		return res, fmt.Errorf("claude: %w: %s", werr, lastLine(stderr.String()))
	case rerr != nil:
		return res, fmt.Errorf("reading claude's stream: %w", rerr)
	case !got:
		return res, errors.New("claude printed no result event")
	}

	return res, nil
}

// readStream reads one turn's stream line by line. A bufio.Reader rather than
// a Scanner, because a tool result can pass the Scanner's 64KB token.
func readStream(r io.Reader, tr *transcript, on func(event)) (turnResult, bool, error) {
	br := bufio.NewReader(r)

	var (
		res turnResult
		got bool
	)

	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
			tr.write(line)

			var e event
			if json.Unmarshal(line, &e) == nil {
				if on != nil {
					on(e)
				}

				if e.Type == "result" {
					res, got = e.turnResult(), true
				}
			}
		}

		if errors.Is(err, io.EOF) {
			return res, got, nil
		}

		if err != nil {
			return res, got, err
		}
	}
}

// progress is what the stream has shown of a run so far, for the live view
// and for show.
type progress struct {
	Skill string
	Step  string
	Tool  string // the latest tool call, with its subject
	Sub   bool   // the latest tool call is a subagent's
	Tools int

	cwd     string  // the run's repo, cut from the paths a tool line shows
	doneUSD float64 // the exact cost of the turns that ended
	estUSD  float64 // the running turn's estimate, from its token usage
	seen    map[string]bool
}

// CostUSD is the exact cost of the ended turns plus the running turn's
// estimate. The bool says whether an estimate is in it.
func (p *progress) CostUSD() (float64, bool) { return p.doneUSD + p.estUSD, p.estUSD > 0 }

// beginTurn takes the skill from the turn's prompt, because a slash command
// the prompt starts with reaches the stream as no Skill call.
func (p *progress) beginTurn(prompt string) {
	if cmd, _, _ := strings.Cut(prompt, " "); strings.HasPrefix(cmd, "/") {
		p.Skill, p.Step = cmd, ""
	}
}

func (p *progress) apply(e event) {
	switch e.Type {
	case "system":
		if e.Subtype == "init" && e.Cwd != "" {
			p.cwd = e.Cwd
		}
	case "result":
		p.doneUSD += e.CostUSD
		p.estUSD = 0
		clear(p.seen)
	case "assistant":
		if m := e.Message; m != nil && m.Usage != nil && !p.seen[m.ID] {
			// claude prints a message once per content block, each carrying
			// the same usage, so a message counts once
			if p.seen == nil {
				p.seen = map[string]bool{}
			}

			p.seen[m.ID] = true
			p.estUSD += estimateUSD(m.Model, *m.Usage)
		}

		for _, b := range e.tools() {
			p.Tools++
			p.Tool, p.Sub = p.toolLine(b), e.subagent()
			p.applyTool(b)
		}
	}
}

func (p *progress) applyTool(b eventBlock) {
	in := toolInput(b.Input)

	switch b.Name {
	case "Skill":
		if s := in["skill"]; s != "" {
			p.Skill, p.Step = "/"+strings.TrimPrefix(s, "/"), ""
		}
	case "Bash":
		if step := stepOf(in["command"]); step != "" {
			p.Step = step
		}
	}
}

var (
	stateSet = regexp.MustCompile(`f10-state\.sh set (\w+) \w+(?: '?([\w:+\[\]-]+))?`)
	loadStep = regexp.MustCompile(`f10-load-project\.sh ((?:[a-z-]+ ?)+)`)
)

// stepOf is the step a Bash command names. That is a badge call's leaf, else
// its phase, else the steps a project load named.
func stepOf(cmd string) string {
	if m := stateSet.FindAllStringSubmatch(cmd, -1); len(m) > 0 {
		last := m[len(m)-1]

		return cmp.Or(last[2], last[1])
	}

	if m := loadStep.FindStringSubmatch(cmd); m != nil {
		return strings.TrimSpace(m[1])
	}

	return ""
}

func toolInput(raw json.RawMessage) map[string]string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}

	return out
}

// subjectKeys are the input fields that say what a tool call is about, the
// first one present winning.
var subjectKeys = []string{"file_path", "path", "pattern", "command", "skill", "url", "description", "prompt"}

// toolLine is a tool call as one short line: its name and its subject, a
// path inside the run's repo shown relative to it.
func (p *progress) toolLine(b eventBlock) string {
	in := toolInput(b.Input)

	for _, k := range subjectKeys {
		if s := strings.TrimSpace(in[k]); s != "" {
			s, _, _ = strings.Cut(s, "\n")
			if p.cwd != "" {
				s = strings.ReplaceAll(s, p.cwd+"/", "")
			}

			return b.Name + "  " + s
		}
	}

	return b.Name
}

// prices are list dollars per million tokens, input then output, by model
// family. They only feed the live estimate: the report holds claude's own
// figure from each turn's result.
var prices = map[string][2]float64{
	"opus":   {4, 20},
	"sonnet": {2, 10},
	"haiku":  {0.10, 0.50},
}

// estimateUSD prices one message's usage. Cache reads cost a twentieth of
// input, a 5-minute cache write 1.25 times it and a 1-hour write twice it.
func estimateUSD(model string, u eventUsage) float64 {
	var price [2]float64

	for family, p := range prices {
		if strings.Contains(model, family) {
			price = p
		}
	}

	in, out := price[0]/1e6, price[1]/1e6

	write := float64(u.CacheWrite) * 1.25 * in
	if c := u.CacheCreation; c != nil && c.FiveMin+c.OneHour > 0 {
		write = float64(c.FiveMin)*1.25*in + float64(c.OneHour)*2*in
	}

	return float64(u.Input)*in + float64(u.Output)*out + float64(u.CacheRead)*in/20 + write
}
