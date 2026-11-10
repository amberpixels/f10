package asknotify

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
)

// asksConfigFile is the user's ask settings in ~/.f10; f10 only reads it.
const asksConfigFile = "md"

// notifyTimeout bounds a notify command: a hung notifier never holds an
// agent's ask or a drive's tick for long.
const notifyTimeout = 10 * time.Second

// AskLink is askName with the task linked; the url the ask carries is the
// lead's, so a bundle links its members from the ledger instead.
func AskLink(links *render.TaskLinks, a asks.Ask) string {
	if !strings.Contains(a.Task, ref.Separator) {
		links.Known(a.Project, a.Task, a.URL)
	}

	name := links.ID(a.Project, a.Task)
	if a.Project == "" {
		return name
	}

	return a.Project + ":" + name
}

// RaiseAsk puts a in the inbox and, the first time, runs the notify
// command. Neither failing changes the run that asked: a line says so.
func RaiseAsk(
	ctx context.Context,
	w io.Writer,
	dir string,
	a asks.Ask,
	now time.Time,
	notify func(context.Context, asks.Ask) error,
) {
	raised, fresh, err := asks.Raise(dir, a, now)
	if err != nil {
		fmt.Fprintf(w, "  keeping %s's ask in the inbox: %v\n", AskLink(render.LinksFor(w, false), a), err)

		return
	}

	if fresh && notify != nil {
		if err := notify(ctx, raised); err != nil {
			fmt.Fprintf(w, "  notifying %s's ask: %v\n", AskLink(render.LinksFor(w, false), a), err)
		}
	}
}

// AskNotifier tells the user of a new ask; tests turn it off.
var AskNotifier = notifyAsk

// asksConfig is ~/.f10/md's frontmatter.
type asksConfig struct {
	// Notify runs per new ask through sh, {task}, {question} and {url}
	// standing for shell variables; "off" runs nothing, empty the desktop
	// default
	Notify string `yaml:"notify"`
}

func notifyAsk(ctx context.Context, a asks.Ask) error {
	var cfg asksConfig
	if _, err := scope.ReadUserConfig(asksConfigFile, &cfg); err != nil {
		return err
	}

	argv, env := NotifyArgv(cfg.Notify, a, runtime.GOOS, exec.LookPath)
	if len(argv) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()

	//nolint:gosec // the user's own notify command, from their own settings
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))
	}

	return nil
}

// NotifyArgv is the command run for a, and its environment. A placeholder
// becomes a shell variable, never the agent's text, so a question holding
// `$(...)` or a backtick is printed, not run, however the template quotes it.
func NotifyArgv(notify string, a asks.Ask, goos string, look func(string) (string, error)) ([]string, []string) {
	question := a.Text
	if len(a.Questions) > 0 {
		question = a.Questions[0].Text
	}

	switch strings.TrimSpace(notify) {
	case "off":
		return nil, nil
	case "":
	default:
		cmd := strings.NewReplacer(
			"{task}", "${F10_ASK_TASK}",
			"{question}", "${F10_ASK_QUESTION}",
			"{url}", "${F10_ASK_URL}",
		).Replace(notify)

		return []string{"sh", "-c", cmd},
			[]string{"F10_ASK_TASK=" + a.Task, "F10_ASK_QUESTION=" + question, "F10_ASK_URL=" + a.URL}
	}

	title := "f10 · " + a.Task + " asks"

	switch goos {
	case "darwin":
		return []string{
			"osascript",
			"-e",
			"display notification " + appleQuote(question) + " with title " + appleQuote(title),
		}, nil
	case "linux":
		if _, err := look("notify-send"); err == nil {
			return []string{"notify-send", title, question}, nil
		}
	}

	return nil, nil
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// AskFromRun is the ask a driven agent's run records, placed by its cwd.
func AskFromRun(ctx context.Context, r *state.Run, cwd string) (asks.Ask, bool) {
	if r.Ask == "" || r.Task == "" {
		return asks.Ask{}, false
	}

	lay := layout.Locate(ctx, cwd)
	if lay.StorageRoot == "" {
		return asks.Ask{}, false
	}

	task, _, _ := strings.Cut(r.Task, "+")

	return asks.Ask{
		Task:     strings.ToUpper(task),
		URL:      r.URL,
		Root:     lay.StorageRoot.Path(),
		Main:     cmp.Or(lay.MainRoot, lay.CheckoutRoot),
		Project:  lay.Project,
		Worktree: lay.CheckoutRoot,
		Skill:    r.Leaf,
		Text:     r.Ask,
	}, true
}

// PaneAnswered claims the open ask of the checkout at cwd for a prompt
// typed in its pane, and starts the resume of its drive apart from this
// hook, which has seconds to live.
func PaneAnswered(ctx context.Context, cwd, prompt string) {
	root := layout.Locate(ctx, cwd).CheckoutRoot
	if root == "" {
		return
	}

	dir := asks.Dir()

	open, err := asks.Open(dir)
	if err != nil {
		return
	}

	for _, a := range open {
		// finish's question is the drive's, not the agent's
		if a.Worktree != root || a.Skill == "finish" {
			continue
		}

		claimed, err := asks.Claim(dir, a.Key(), asks.ByPane, strings.TrimSpace(prompt), true, time.Now())
		if err != nil || claimed.Drive == 0 {
			continue
		}

		if exe, err := os.Executable(); err == nil {
			_ = shell.Detached(exe, "asks", "resume", claimed.Key())
		}
	}
}
