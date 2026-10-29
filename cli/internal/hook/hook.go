// Package hook is f10's one entry point for Claude Code hook events: they
// seed the run's badge, become typed observations and record the run's waits
// on the user. Nothing here fails a run: a payload, state file or observer
// that errors is dropped and the hook exits 0.
package hook

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
)

// Payload is the part of a hook event's JSON the arms read, decoded once.
type Payload struct {
	Event        string          `json:"hook_event_name"`
	Session      string          `json:"session_id"`
	Cwd          string          `json:"cwd"`
	Source       string          `json:"source"`       // SessionStart: startup, resume, clear, compact
	CommandName  string          `json:"command_name"` // UserPromptExpansion
	CommandArgs  string          `json:"command_args"`
	ToolName     string          `json:"tool_name"` // PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest
	ToolUseID    string          `json:"tool_use_id"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Prompt       string          `json:"prompt"` // UserPromptSubmit
}

// Env is everything a hook reaches outside its payload, so a test can point
// it at a temp directory and stub what needs a real project.
type Env struct {
	StateDir   string
	Home       string
	PluginRoot string // where bin/f10-state.sh, the status-line renderer, lives
	Debug      bool   // F10_STATE_DEBUG: append every payload to hooks.log
	Now        func() time.Time

	// StorageRoot is the checkout's storage root for a cwd, in-repo or
	// out-of-tree; empty when it cannot tell.
	StorageRoot func(ctx context.Context, cwd string) string
	// TaskID is the tracker task id a created issue's url names, in the
	// project's id format; empty when it cannot tell.
	TaskID func(ctx context.Context, cwd, url string) string
	// PromptTyped is told of every prompt submitted, with the session's
	// cwd: one typed in an asking agent's pane answers its ask.
	PromptTyped func(ctx context.Context, cwd, prompt string)
	// StatusText is what `f10 status` prints for a session, and whether it
	// holds a question to relay, which needs a model turn.
	StatusText func(ctx context.Context, session string) (string, bool)

	// Observers receive every observation after the badge has applied it.
	Observers []observe.Observer
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}

	return time.Now()
}

// Dispatch handles one hook event and returns what the hook prints on
// stdout: a verdict for a bare `/f10:status`, nothing otherwise.
func Dispatch(ctx context.Context, raw []byte, env *Env) []byte {
	if env.Debug {
		debugLog(env.StateDir, raw)
	}

	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}

	sid := cmp.Or(p.Session, state.Session())

	notify(ctx, env, sid, p.Cwd, waits(env, sid, p)...)

	switch p.Event {
	case "UserPromptSubmit":
		// the mark proves to a drive that the agent took a prompt, even when
		// the turn ended between two of its looks
		if sid != "" && p.Cwd != "" {
			_ = state.MarkPrompt(env.StateDir, sid, state.RootOf(p.Cwd), env.now())
		}

		if env.PromptTyped != nil && p.Cwd != "" {
			env.PromptTyped(ctx, p.Cwd, p.Prompt)
		}
	case "UserPromptExpansion":
		// the typed path, which the Skill tool never sees. Only a bare status
		// is answered here: one with a task id, or holding an ask (answerStatus
		// checks), may relay a question, which needs a model turn
		if (p.CommandName == "f10:status" || p.CommandName == "status") && p.CommandArgs == "" {
			return answerStatus(ctx, env, sid)
		}

		seed(env, sid, skillOf(p.CommandName), p.CommandArgs, p.Cwd)
		notify(ctx, env, sid, p.Cwd, skillStarted(p.CommandName, p.CommandArgs)...)
	case "PreToolUse":
		// the model calling the skill itself, which the expansion never sees
		if p.ToolName != "Skill" {
			return nil
		}

		var in struct {
			Skill string `json:"skill"`
			Args  string `json:"args"`
		}

		_ = json.Unmarshal(p.ToolInput, &in)
		seed(env, sid, skillOf(in.Skill), in.Args, p.Cwd)
		notify(ctx, env, sid, p.Cwd, skillStarted(in.Skill, in.Args)...)
	case "PostToolUse":
		obs := observed(ctx, env, p)
		b := badge{env: env, session: sid, cwd: p.Cwd}

		for _, o := range obs {
			observe.Stamp(o, sid, p.Cwd, env.now())
			_ = b.Observe(ctx, o)
		}

		notify(ctx, env, sid, p.Cwd, obs...)
	case "Stop":
		if sid == "" {
			return nil
		}

		r, err := state.Load(state.Path(env.StateDir, sid))
		if err != nil {
			return nil
		}

		// read before the backstop: the ledger keeps where the run stopped,
		// never the done the backstop infers
		stop := stopped(r)

		if r.Backstop() {
			_ = r.Save(env.StateDir, env.now())

			if stop != nil {
				stop.Backstop = true
			}
		}

		if stop != nil {
			notify(ctx, env, sid, p.Cwd, stop)
		}
	case "SessionStart":
		linkStatusline(env)

		// `<session>.started` is when this session loaded the plugin; doctor
		// compares it with the install's update time. Compact and clear keep
		// the loaded plugin, so only startup and resume mark it.
		if sid != "" && (p.Source == "startup" || p.Source == "resume") {
			if err := os.MkdirAll(env.StateDir, 0o750); err == nil {
				_ = os.WriteFile(state.StartedPath(env.StateDir, sid), nil, 0o600)
			}
		}

		state.Prune(env.StateDir, env.now())
	}

	return nil
}

// skillOf is the phase skill a command or Skill-tool name refers to, "" for
// none. A bare name is accepted, a wrong match costing one badge for one
// turn, except debug: a bare one is as likely another plugin's, and seeding
// clears the run.
func skillOf(name string) string {
	if name == "f10:debug" {
		return "debug"
	}

	name = strings.TrimPrefix(name, "f10:")
	if slices.Contains(state.Phases, name) {
		return name
	}

	return ""
}

// notify stamps observations and hands them to every observer, in order.
func notify(ctx context.Context, env *Env, sid, cwd string, obs ...observe.Observation) {
	for _, o := range obs {
		if observe.MetaOf(o).Session == "" {
			observe.Stamp(o, sid, cwd, env.now())
		}

		for _, ob := range env.Observers {
			_ = ob.Observe(ctx, o)
		}
	}
}

// skillStarted is an f10 skill starting, any of them, not only the badge's
// phases. A bare `/f10:status` is answered without a run and is none.
func skillStarted(name, args string) []observe.Observation {
	skill, ok := strings.CutPrefix(name, "f10:")
	if !ok && skillOf(name) == "" || skill == "" || skill == "status" && args == "" {
		return nil
	}

	return []observe.Observation{&observe.SkillStarted{Skill: skill, Args: args}}
}

// stopped is where a run stands as a turn ends: the latest phase the run
// reached, its status and leaf; nil when no phase has started.
func stopped(r *state.Run) *observe.SessionStopped {
	phase := ""

	for _, ph := range state.Phases {
		if r.Status(ph) != "pending" {
			phase = ph
		}
	}

	if phase == "" {
		return nil
	}

	return &observe.SessionStopped{Task: r.Task, Phase: phase, Status: r.Status(phase), Leaf: r.Leaf}
}

func seed(env *Env, sid, skill, args, cwd string) {
	if skill == "" || sid == "" {
		return
	}

	r, _ := state.LoadOrFresh(env.StateDir, sid)
	if r.Seed(skill, args, cwd) {
		_ = r.Save(env.StateDir, env.now())
	}
}

// observed turns a PostToolUse payload into observations.
func observed(ctx context.Context, env *Env, p Payload) []observe.Observation {
	switch p.ToolName {
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}

		var out struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}

		_ = json.Unmarshal(p.ToolInput, &in)
		_ = json.Unmarshal(p.ToolResponse, &out)

		obs := observe.FromBash(in.Command, out.Stdout, out.Stderr)

		// a plan archived is a move under plans/; spare the storage-root
		// lookup for every other command
		if strings.Contains(in.Command, "mv") && strings.Contains(in.Command, "plans") {
			root := ""
			if env.StorageRoot != nil && p.Cwd != "" {
				root = env.StorageRoot(ctx, p.Cwd)
			}

			obs = append(obs, observe.FromMoves(in.Command, p.Cwd, root)...)
		}

		return obs
	case "Write", "Edit":
		var in struct {
			Path string `json:"file_path"`
		}

		_ = json.Unmarshal(p.ToolInput, &in)

		if !strings.HasSuffix(in.Path, ".md") {
			return nil // every observed file is markdown; spare the storage-root lookup
		}

		root := ""
		if env.StorageRoot != nil && p.Cwd != "" {
			root = env.StorageRoot(ctx, p.Cwd)
		}

		if o := observe.FromWrite(in.Path, root); o != nil {
			return []observe.Observation{o}
		}
	}

	return nil
}

// linkStatusline re-points ~/.claude/f10/statusline at this install's
// renderer, since the plugin root moves with every update and the user's
// statusLine command names the link. Swapped in by rename, so a refresh never
// finds it missing.
func linkStatusline(env *Env) {
	if env.PluginRoot == "" || env.Home == "" {
		return
	}

	target := filepath.Join(env.PluginRoot, "bin", "f10-state.sh")
	if _, err := os.Stat(target); err != nil {
		return
	}

	link := filepath.Join(env.Home, ".claude", "f10", "statusline")
	if dest, err := os.Readlink(link); err == nil && dest == target {
		return
	}

	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		return
	}

	tmp := fmt.Sprintf("%s.%d", link, os.Getpid())
	_ = os.Remove(tmp)

	if err := os.Symlink(target, tmp); err != nil {
		return
	}

	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
	}
}

func debugLog(dir string, raw []byte) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}

	f, err := os.OpenFile(filepath.Join(dir, "hooks.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}

	defer f.Close()

	_, _ = f.Write(append(slices.Clone(raw), '\n'))
}

// answerStatus answers a bare `/f10:status` with a blocking verdict that ends
// the turn before the model runs, so it costs no model turn.
func answerStatus(ctx context.Context, env *Env, sid string) []byte {
	text, asks := "", false
	if env.StatusText != nil {
		text, asks = env.StatusText(ctx, sid)
		text = strings.TrimRight(text, "\n")
	}

	if asks {
		return nil
	}

	if text == "" {
		text = "no f10 run in this session"
	}

	out, err := json.Marshal(map[string]any{
		"decision": "block",
		"reason":   titled(text),
		"hookSpecificOutput": map[string]any{
			"hookEventName":          "UserPromptExpansion",
			"suppressOriginalPrompt": true,
		},
	})
	if err != nil {
		return nil
	}

	return append(out, '\n')
}

// titled puts the block under its own title, so the eye lands there and not
// on Claude Code's `blocked by hook` line; the "no model turn" tag says
// nothing went wrong. The rule follows the widest row, clamped to 44-72.
func titled(text string) string {
	w := 0
	for line := range strings.SplitSeq(text, "\n") {
		w = max(w, utf8.RuneCountInString(line))
	}

	w = min(max(w, 44), 72)
	pad := max(w-25, 2) // 12 columns of title, 13 of tag

	return "f10 · status" + strings.Repeat(" ", pad) + "no model turn\n" + strings.Repeat("─", w) + "\n" + text
}
