package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// asksConfigFile is the user's ask settings in ~/.f10; f10 only reads it.
const asksConfigFile = "asks.md"

// notifyTimeout bounds a notify command: a hung notifier never holds an
// agent's ask or a drive's tick for long.
const notifyTimeout = 10 * time.Second

const recentAnswered = 24 * time.Hour

func asksCommand() *cli.Command {
	return &cli.Command{
		Name:  "asks",
		Usage: "the questions driven agents wait on, across projects: list, answer, or walk them in a terminal",
		Description: "A driven agent's ask, or a drive's finish question, waits here until the first answer from " +
			"any way in: its own pane, f10 asks, /f10:asks or f10 drive --answer. An answer to a drive's task " +
			"resumes the drive; one to an agent no drive watches is sent to it. Bare f10 asks walks the open asks " +
			"in a terminal and lists them otherwise. ~/.f10/asks.md sets notify, the command run for each new ask.",
		Action: runAsks,
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "the open asks, then those answered in the last day",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "print JSON, for /f10:asks"}},
				Action: runAsksList,
			},
			{
				Name:      "answer",
				Usage:     "answer a task's open ask; questions not named take their default",
				ArgsUsage: "<task | project:task> [N=VALUE ...]",
				Description: "VALUE is an option's letter (b), its number (2) or its text; free text answers only " +
					"a question with no options.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "by", Value: asks.ByAsks, Usage: "the way in to record: asks or skill"},
				},
				Action: runAsksAnswer,
			},
			{
				Name:      "resume",
				Usage:     "resume the drive an answered ask belongs to",
				ArgsUsage: "<key> | --drive N --root DIR --main DIR [--after-pid PID]",
				Hidden:    true,
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "drive"},
					&cli.StringFlag{Name: "root"},
					&cli.StringFlag{Name: "main"},
					&cli.IntFlag{Name: "after-pid", Usage: "wait for this process to end first"},
				},
				Action: runAsksResume,
			},
			{
				Name:   "hook",
				Usage:  "tell a session the open asks it has not been told; the payload on stdin",
				Hidden: true,
				Action: runAsksHook,
			},
		},
	}
}

func runAsks(ctx context.Context, cmd *cli.Command) error {
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		return walkAsks(ctx, cmd.Writer, bufio.NewReader(os.Stdin), asks.Dir(), liveDeliver())
	}

	return runAsksList(ctx, cmd)
}

type askView struct {
	Key        string          `json:"key"`
	Task       string          `json:"task"`
	Project    string          `json:"project,omitempty"`
	URL        string          `json:"url,omitempty"`
	Skill      string          `json:"skill,omitempty"`
	Drive      int             `json:"drive,omitempty"`
	Text       string          `json:"text"`
	Questions  []asks.Question `json:"questions"`
	AskedAt    time.Time       `json:"asked_at"`
	Answer     string          `json:"answer,omitempty"`
	By         string          `json:"by,omitempty"`
	AnsweredAt *time.Time      `json:"answered_at,omitempty"`
}

type askList struct {
	Open     []askView `json:"open"`
	Answered []askView `json:"answered"`
}

// viewOf is the ask as a reader answers it.
//
//nolint:lostfield // a reduction: the paths and delivery bookkeeping stay in the inbox
func viewOf(a asks.Ask) askView {
	v := askView{
		Key: a.Key(), Task: a.Task, Project: a.Project, URL: a.URL, Skill: a.Skill, Drive: a.Drive,
		Text: a.Text, Questions: a.Questions, AskedAt: a.AskedAt, Answer: a.Answer, By: a.By,
	}
	if !a.AnsweredAt.IsZero() {
		v.AnsweredAt = &a.AnsweredAt
	}

	return v
}

func runAsksList(ctx context.Context, cmd *cli.Command) error {
	return listAsks(ctx, cmd.Writer, asks.Dir(), time.Now(), cmd.Bool("json"))
}

func listAsks(ctx context.Context, w io.Writer, dir string, now time.Time, asJSON bool) error {
	open, err := asks.Open(dir)
	if err != nil {
		return err
	}

	answered, err := asks.Answered(dir, now.Add(-recentAnswered))
	if err != nil {
		return err
	}

	if asJSON {
		out := askList{Open: []askView{}, Answered: []askView{}}

		for _, a := range open {
			out.Open = append(out.Open, viewOf(a))
		}

		for _, a := range answered {
			out.Answered = append(out.Answered, viewOf(a))
		}

		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		return enc.Encode(out)
	}

	if len(open) == 0 {
		fmt.Fprintln(w, "no open asks")
	}

	links := linksFor(w, false)
	loadAskLinks(ctx, links, append(slices.Clone(open), answered...))

	for i, a := range open {
		if i > 0 {
			fmt.Fprintln(w)
		}

		printAsk(w, links, a, now)
	}

	if len(answered) > 0 {
		fmt.Fprintln(w, "\nanswered in the last day:")

		for _, a := range answered {
			fmt.Fprintf(
				w,
				"  %s answered %s at %s: %s\n",
				askLink(
					links,
					a,
				),
				asks.Way(a.By),
				a.AnsweredAt.Local().Format("15:04"),
				cmp.Or(a.Answer, "(nothing sent)"),
			)
		}
	}

	return nil
}

func printAsk(w io.Writer, links *taskLinks, a asks.Ask, now time.Time) {
	where := ""
	if a.Skill != "" {
		where = " at " + a.Skill
	}

	fmt.Fprintf(w, "%s asks%s, %s ago\n", askLink(links, a), where, now.Sub(a.AskedAt).Round(time.Minute))

	for _, q := range a.Questions {
		fmt.Fprintf(w, "  %d. %s\n", q.N, q.Text)

		if len(q.Options) > 0 {
			fmt.Fprintf(w, "     %s  (default %s)\n", asks.Letters(q.Options), q.Default())
		}
	}

	if a.URL != "" {
		fmt.Fprintf(w, "  %s\n", a.URL)
	}
}

// askName is the task, with its project where the inbox spans several.
func askName(a asks.Ask) string {
	if a.Project == "" {
		return a.Task
	}

	return a.Project + ":" + a.Task
}

// loadAskLinks reads each ask's task urls from the ledger of the project
// its main checkout belongs to, keyed by the ask's display project.
func loadAskLinks(ctx context.Context, links *taskLinks, all []asks.Ask) {
	if links == nil || !links.on {
		return
	}

	projects := map[string]string{}

	for _, a := range all {
		dir := cmp.Or(a.Main, a.Worktree)
		if dir == "" {
			continue
		}

		if _, ok := projects[dir]; !ok {
			projects[dir] = projectOf(ctx, dir)
		}

		links.LoadAs(ctx, projects[dir], a.Project, a.Task)
	}
}

// askLink is askName with the task linked; the url the ask carries is the
// lead's, so a bundle links its members from the ledger instead.
func askLink(links *taskLinks, a asks.Ask) string {
	if !strings.Contains(a.Task, ref.Separator) {
		links.Known(a.Project, a.Task, a.URL)
	}

	name := links.ID(a.Project, a.Task)
	if a.Project == "" {
		return name
	}

	return a.Project + ":" + name
}

// findOpen is the open ask a token names: a task id, or project:task where
// two projects share one.
func findOpen(dir, token string) (asks.Ask, error) {
	open, err := asks.Open(dir)
	if err != nil {
		return asks.Ask{}, err
	}

	project, task, scoped := strings.Cut(token, ":")
	if !scoped {
		task, project = token, ""
	}

	named := func(a asks.Ask) bool {
		return strings.EqualFold(a.Task, task) && (project == "" || strings.EqualFold(a.Project, project))
	}

	var hits []asks.Ask

	for _, a := range open {
		if named(a) {
			hits = append(hits, a)
		}
	}

	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		// the newest answer names who closed it
		answered, _ := asks.Answered(dir, time.Time{})
		for _, a := range slices.Backward(answered) {
			if named(a) {
				return asks.Ask{}, fmt.Errorf(
					"%s has no open ask: %w",
					token,
					asks.ClosedError{By: a.By, At: a.AnsweredAt},
				)
			}
		}

		return asks.Ask{}, fmt.Errorf("%s has no open ask: f10 asks list shows them", token)
	}

	names := make([]string, len(hits))
	for i, a := range hits {
		names[i] = askName(a)
	}

	return asks.Ask{}, fmt.Errorf("%s asks in several projects: name one, %s", task, strings.Join(names, " or "))
}

func answersFor(a asks.Ask, values []string) (string, error) {
	given := map[int]string{}

	for _, v := range values {
		num, value, ok := strings.Cut(v, "=")
		n, err := strconv.Atoi(strings.TrimSpace(num))

		if !ok || err != nil {
			return "", fmt.Errorf("%q is not N=VALUE", v)
		}

		i := slices.IndexFunc(a.Questions, func(q asks.Question) bool { return q.N == n })
		if i < 0 {
			return "", fmt.Errorf("%s's ask has no question %d", a.Task, n)
		}

		resolved, err := asks.Resolve(a.Questions[i], value)
		if err != nil {
			return "", err
		}

		given[n] = resolved
	}

	for _, q := range a.Questions {
		if given[q.N] == "" && q.Default() == "" {
			return "", fmt.Errorf("question %d (%s) has no default: answer it as %d=VALUE", q.N, q.Text, q.N)
		}
	}

	return asks.Format(a.Questions, given), nil
}

// A deliverer moves a claimed answer on: prompt reaches an agent no drive
// watches, resume restarts a stopped drive, and live says whether a drive
// still runs.
type deliverer struct {
	prompt func(ctx context.Context, a asks.Ask) error
	resume func(ctx context.Context, w io.Writer, a asks.Ask) error
	live   func(a asks.Ask) bool
}

func liveDeliver() deliverer {
	return deliverer{
		prompt: func(ctx context.Context, a asks.Ask) error {
			return promptAgent(ctx, a.Worktree, a.Task, a.Suffix, a.Answer)
		},
		resume: resumeAskDrive,
		live: func(a asks.Ask) bool {
			_, held := drives.Probe(a.Root, drives.DriveKey(a.Drive))

			return held
		},
	}
}

// deliver moves a claimed answer to where it acts. A drive's ask goes to its
// drive, a running one taking it as it exits so nothing runs twice; any
// other is sent to its agent, unless typed in the pane already.
func (d deliverer) deliver(ctx context.Context, w io.Writer, dir string, a asks.Ask) error {
	switch {
	case a.Drive > 0 && d.live(a):
		fmt.Fprintf(w, "drive %d takes it as it stops\n", a.Drive)

		return nil
	case a.Drive > 0:
		return d.resume(ctx, w, a)
	case !a.DeliveredAt.IsZero():
		return nil
	}

	if err := d.prompt(ctx, a); err != nil {
		return fmt.Errorf("sending the answer to %s's agent: %w; type it in its pane: %s", a.Task, err, a.Answer)
	}

	// no drive takes an answer sent this way: it is done once sent
	if err := asks.Update(
		dir,
		a.Key(),
		func(x *asks.Ask) { x.DeliveredAt, x.TakenAt = time.Now(), time.Now() },
	); err != nil {
		fmt.Fprintf(w, "  recording the answer sent: %v\n", err)
	}

	fmt.Fprintf(w, "sent to %s's agent\n", askLink(linksFor(w, false), a))

	return nil
}

func runAsksAnswer(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) == 0 {
		return errors.New("answer needs a task: f10 asks answer <task> [N=VALUE ...]")
	}

	by := cmd.String("by")
	if by != asks.ByAsks && by != asks.BySkill {
		return fmt.Errorf("--by takes %s or %s", asks.ByAsks, asks.BySkill)
	}

	return answerAsk(ctx, cmd.Writer, asks.Dir(), args[0], args[1:], by, liveDeliver())
}

func answerAsk(ctx context.Context, w io.Writer, dir, token string, values []string, by string, d deliverer) error {
	a, err := findOpen(dir, token)
	if err != nil {
		return err
	}

	text, err := answersFor(a, values)
	if err != nil {
		return err
	}

	claimed, err := asks.Claim(dir, a.Key(), by, text, false, time.Now())
	if err != nil {
		return fmt.Errorf("%s: %w", askName(a), err)
	}

	fmt.Fprintf(w, "%s answered: %s\n", askName(claimed), text)

	return d.deliver(ctx, w, dir, claimed)
}

func walkAsks(ctx context.Context, w io.Writer, in *bufio.Reader, dir string, d deliverer) error {
	open, err := asks.Open(dir)
	if err != nil {
		return err
	}

	if len(open) == 0 {
		fmt.Fprintln(w, "no open asks")

		return nil
	}

	links := linksFor(w, false)
	loadAskLinks(ctx, links, open)

	for i, a := range open {
		if i > 0 {
			fmt.Fprintln(w)
		}

		printAsk(w, links, a, time.Now())

		given := map[int]string{}
		skip := false

		for _, q := range a.Questions {
			value, err := askLine(w, in, q)
			if err != nil {
				return err
			}

			if value == "s" {
				skip = true

				break
			}

			given[q.N] = value
		}

		if skip {
			fmt.Fprintln(w, "skipped")

			continue
		}

		text := asks.Format(a.Questions, given)

		claimed, err := asks.Claim(dir, a.Key(), asks.ByAsks, text, false, time.Now())
		if err != nil {
			fmt.Fprintf(w, "%s: %v\n", askName(a), err)

			continue
		}

		fmt.Fprintf(w, "answered: %s\n", text)

		if err := d.deliver(ctx, w, dir, claimed); err != nil {
			fmt.Fprintln(w, err)
		}
	}

	return nil
}

// askLine reads one question's answer until it resolves: Enter is the
// default, s skips the ask.
func askLine(w io.Writer, in *bufio.Reader, q asks.Question) (string, error) {
	for {
		prompt := fmt.Sprintf("%d> ", q.N)
		if d := q.Default(); d != "" {
			prompt = fmt.Sprintf("%d [%s]> ", q.N, d)
		}

		fmt.Fprint(w, prompt)

		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}

		line = strings.TrimSpace(line)

		switch {
		case line == "s":
			return "s", nil
		case line == "" && q.Default() != "":
			return q.Default(), nil
		}

		resolved, err := asks.Resolve(q, line)
		if err == nil {
			return resolved, nil
		}

		fmt.Fprintln(w, err)
	}
}

// runAsksResume resumes a drive for an answer: the one an answered ask
// names, or, from a drive that is ending, that drive once its process is
// gone, since the daemon attaches a resume to a drive still running.
func runAsksResume(ctx context.Context, cmd *cli.Command) error {
	if n := cmd.Int("drive"); n > 0 {
		if pid := cmd.Int("after-pid"); pid > 0 {
			waitGone(pid, time.Minute)
		}

		a := asks.Ask{Drive: n, Root: cmd.String("root"), Main: cmd.String("main")}
		if d := liveDeliver(); !d.live(a) {
			return d.resume(ctx, cmd.Writer, a)
		}

		return nil
	}

	dir := asks.Dir()

	a, isOpen, err := asks.Get(dir, cmd.Args().First())
	if err != nil {
		return err
	}

	if isOpen || a.Drive == 0 {
		return nil
	}

	return liveDeliver().deliver(ctx, cmd.Writer, dir, *a)
}

func resumeAskDrive(ctx context.Context, w io.Writer, a asks.Ask) error {
	n := strconv.Itoa(a.Drive)

	if !daemon.Supported {
		fmt.Fprintf(w, "drive %d is stopped: f10 drive --resume %s in %s takes the answer\n", a.Drive, n, a.Main)

		return nil
	}

	var extra []string
	if t, err := targetAt(ctx, a.Main, false, "asks"); err == nil {
		extra = t.eff.DriveEnv
	}

	dir := daemon.RunDir()

	e := &daemon.Ensurer{Dir: dir, Version: daemonVersion()}
	if _, err := e.Ensure(ctx); err != nil {
		return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
	}

	_, err := daemon.Call(ctx, dir, daemon.Request{
		Op:     daemon.OpRun,
		Argv:   []string{"--resume", n},
		Dir:    a.Main,
		Root:   a.Root,
		Env:    driveEnv(os.Environ(), extra),
		Resume: a.Drive,
	}, 0)
	if err != nil {
		return fmt.Errorf("resuming drive %d: %w", a.Drive, err)
	}

	fmt.Fprintf(w, "drive %d resumed: f10 daemon logs %d -f follows it\n", a.Drive, a.Drive)

	return nil
}

// raiseAsk puts a in the inbox and, the first time, runs the notify
// command. Neither failing changes the run that asked: a line says so.
func raiseAsk(
	ctx context.Context,
	w io.Writer,
	dir string,
	a asks.Ask,
	now time.Time,
	notify func(context.Context, asks.Ask) error,
) {
	raised, fresh, err := asks.Raise(dir, a, now)
	if err != nil {
		fmt.Fprintf(w, "  keeping %s's ask in the inbox: %v\n", askLink(linksFor(w, false), a), err)

		return
	}

	if fresh && notify != nil {
		if err := notify(ctx, raised); err != nil {
			fmt.Fprintf(w, "  notifying %s's ask: %v\n", askLink(linksFor(w, false), a), err)
		}
	}
}

// askNotifier tells the user of a new ask; tests turn it off.
var askNotifier = notifyAsk

// asksConfig is ~/.f10/asks.md's frontmatter.
type asksConfig struct {
	// Notify runs per new ask through sh, {task}, {question} and {url}
	// standing for shell variables; "off" runs nothing, empty the desktop
	// default
	Notify string `yaml:"notify"`
}

func notifyAsk(ctx context.Context, a asks.Ask) error {
	var cfg asksConfig
	if _, err := readUserConfig(asksConfigFile, &cfg); err != nil {
		return err
	}

	argv, env := notifyArgv(cfg.Notify, a, runtime.GOOS, exec.LookPath)
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

// notifyArgv is the command run for a, and its environment. A placeholder
// becomes a shell variable, never the agent's text, so a question holding
// `$(...)` or a backtick is printed, not run, however the template quotes it.
func notifyArgv(notify string, a asks.Ask, goos string, look func(string) (string, error)) ([]string, []string) {
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

// askFromRun is the ask a driven agent's run records, placed by its cwd.
func askFromRun(ctx context.Context, r *state.Run, cwd string) (asks.Ask, bool) {
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
		Root:     sharedRootOf(lay),
		Main:     cmp.Or(lay.MainRoot, lay.CheckoutRoot),
		Project:  lay.Project,
		Worktree: lay.CheckoutRoot,
		Skill:    r.Leaf,
		Text:     r.Ask,
	}, true
}

// sharedRootOf is target.sharedRoot for a layout alone.
func sharedRootOf(lay *layout.Layout) string {
	if lay.LegacyStorage && lay.MainRoot != "" {
		return filepath.Join(lay.MainRoot, ".f10")
	}

	return lay.StorageRoot
}

// asksTold is the mark of the asks a session was told, beside its run file.
func asksTold(stateDir, session string) string {
	return strings.TrimSuffix(state.Path(stateDir, session), filepath.Ext(state.Path(stateDir, session))) + ".asks"
}

// runAsksHook prints, as context, the open asks this session has not been
// told, leaving out the asking agent's own session.
func runAsksHook(ctx context.Context, cmd *cli.Command) error {
	// a hook never fails the prompt it runs for
	defer func() { _ = recover() }()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil
	}

	var p struct {
		Event   string `json:"hook_event_name"`
		Session string `json:"session_id"`
		Cwd     string `json:"cwd"`
	}

	if json.Unmarshal(raw, &p) != nil || p.Session == "" {
		return nil
	}

	if p.Event == "SessionStart" {
		asks.Prune(asks.Dir(), time.Now())
	}

	// every prompt in every session runs this: an empty inbox costs no git
	if open, _ := asks.Open(asks.Dir()); len(open) == 0 {
		return nil
	}

	root := ""
	if p.Cwd != "" {
		root = layout.Locate(ctx, p.Cwd).CheckoutRoot
	}

	_, _ = cmd.Writer.Write(asksContext(asks.Dir(), state.Dir(), p.Session, p.Event, root))

	return nil
}

// asksContext is the hook's output: the untold open asks, outside the
// session's own checkout, as additional context; nothing when none.
func asksContext(dir, stateDir, session, event, root string) []byte {
	open, err := asks.Open(dir)
	if err != nil || len(open) == 0 {
		return nil
	}

	markPath := asksTold(stateDir, session)
	raw, _ := os.ReadFile(markPath)
	told := strings.Fields(string(raw))

	var lines, marks []string

	for _, a := range open {
		mark := a.Key() + "@" + strconv.FormatInt(a.AskedAt.Unix(), 10)
		if a.Worktree == root && root != "" {
			continue
		}

		marks = append(marks, mark)
		if slices.Contains(told, mark) {
			continue
		}

		where := ""
		if a.Skill != "" {
			where = " at " + a.Skill
		}

		lines = append(lines, fmt.Sprintf("- %s asks%s: %s", askName(a), where, a.Text))
	}

	if len(lines) == 0 {
		return nil
	}

	_ = os.MkdirAll(stateDir, 0o750)
	_ = os.WriteFile(markPath, []byte(strings.Join(marks, "\n")+"\n"), 0o600)

	text := "f10: driven agents are waiting on these questions. Tell the user about them once; " +
		"/f10:asks answers them here, or `f10 asks answer <task> N=VALUE` in a terminal.\n" + strings.Join(lines, "\n")

	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     event,
			"additionalContext": text,
		},
	})
	if err != nil {
		return nil
	}

	return append(out, '\n')
}

// paneAnswered claims the open ask of the checkout at cwd for a prompt
// typed in its pane, and starts the resume of its drive apart from this
// hook, which has seconds to live.
func paneAnswered(ctx context.Context, cwd, prompt string) {
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
			_ = startDetached(exe, "asks", "resume", claimed.Key())
		}
	}
}
