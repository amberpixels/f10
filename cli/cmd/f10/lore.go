package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/lore"
)

// The lore noun only resolves the root and the paths and hands over to
// internal/lore; docs/lore.md is the spec.
func loreCommand() *cli.Command {
	jsonFlag := func() cli.Flag { return &cli.BoolFlag{Name: "json", Usage: "emit JSON"} }

	return &cli.Command{
		Name:  "lore",
		Usage: "the why behind the code: entries per change, why a line is the way it is, lessons for a file",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "root",
				Usage: "the lore `DIR`; the project's lore setting picks it when absent",
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "add",
				Hidden:    true,
				Category:  plumbingCategory,
				Usage:     "write one entry and print its id",
				ArgsUsage: "<intent|decision|finding|verdict|lesson>",
				// a rejected option's reason is prose, and prose has commas:
				// repeating the flag is the only way to pass several
				DisableSliceFlagSeparator: true,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "change", Usage: "the change `ID` (the task id)", Required: true},
					&cli.StringSliceFlag{
						Name:  "anchor",
						Usage: "`path:line[-end][@commit][#symbol]`; HEAD when no commit",
					},
					&cli.StringFlag{Name: "supersedes", Usage: "the entry `ID` this one replaces"},
					&cli.StringFlag{Name: "chosen", Usage: "a decision: the option taken"},
					&cli.StringSliceFlag{Name: "rejected", Usage: "a decision: `'option: reason'`, repeatable"},
					&cli.StringFlag{Name: "severity", Usage: "a finding: blocker, major, minor or nit"},
					&cli.StringFlag{Name: "status", Usage: "a finding: open, fixed, wontfix or duplicate"},
					&cli.StringFlag{Name: "resolution", Usage: "a finding: the fixing sha or the reason"},
					&cli.StringFlag{Name: "source", Usage: "a finding: local or remote, the review it came from"},
					&cli.StringFlag{
						Name:  "outcome",
						Usage: "a verdict: proceed, proceed with changes, rethink or stop",
					},
					&cli.StringFlag{Name: "gate", Usage: "a verdict: judge, review or human"},
					&cli.StringFlag{Name: "commit", Usage: "a verdict: the `SHA` judged"},
					&cli.StringFlag{Name: "judged", Usage: "a verdict: task, plan or change, what it judged"},
					&cli.BoolFlag{Name: "retired", Usage: "a lesson superseding another: retire it"},
					&cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "the body `TEXT`"},
					&cli.StringFlag{Name: "file", Aliases: []string{"F"}, Usage: "read the body from `FILE`"},
					&cli.StringFlag{Name: "agent-model", Usage: "the agent's model; F10_AGENT_MODEL when absent"},
					&cli.StringFlag{
						Name:  "agent-via",
						Usage: "the agent's harness; F10_AGENT_VIA, else claude-code inside Claude Code",
					},
					&cli.StringFlag{
						Name:  "agent-session",
						Usage: "the agent's session id; F10_AGENT_SESSION when absent",
					},
				},
				Action: runLoreAdd,
			},
			{
				Name:      "show",
				Usage:     "the change's effective entries, the intent first, then by time",
				ArgsUsage: "<change>",
				Flags:     []cli.Flag{jsonFlag()},
				Action:    runLoreShow,
			},
			{
				Name:      "why",
				Usage:     "why a line is the way it is: its change's intent, latest verdict, and the entries on the line",
				ArgsUsage: "<path:line[@rev]>",
				Flags:     []cli.Flag{jsonFlag()},
				Action:    runLoreWhy,
			},
			{
				Name:      "lessons",
				Usage:     "the lessons to read before editing a file",
				ArgsUsage: "<path>",
				Flags:     []cli.Flag{jsonFlag()},
				Action:    runLoreLessons,
			},
			{
				Name:      "check",
				Hidden:    true,
				Category:  plumbingCategory,
				Usage:     "exit 1 naming each reason when the change has no intent, no passing verdict, or an open blocker or major finding",
				ArgsUsage: "<change>",
				Action:    runLoreCheck,
			},
			{
				Name:      "validate",
				Hidden:    true,
				Category:  plumbingCategory,
				Usage:     "lint every entry of the change, or of every change, against the schema",
				ArgsUsage: "[<change>]",
				Action:    runLoreValidate,
			},
		},
	}
}

type loreEnv struct {
	store *lore.Store
	cwd   string
	top   string // the repository root; cwd outside git
}

func loreFor(ctx context.Context, cmd *cli.Command) (*loreEnv, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}

	root := cmd.String("root")

	// outside git only --root can name the store, and paths stay cwd-relative
	top, err := gitx.Exec(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		if root == "" {
			return nil, fmt.Errorf("lore needs a git repository or --root: %w", err)
		}

		top = cwd
	}

	switch {
	case root != "":
		if root, err = filepath.Abs(root); err != nil {
			return nil, err
		}
	default:
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return nil, err
		}

		root = loreRootFor(t)
	}

	return &loreEnv{
		store: &lore.Store{Root: root, Git: lore.CLIGit{Dir: top}},
		cwd:   cwd,
		top:   top,
	}, nil
}

func loreRootFor(t *target) string {
	if t.eff.Lore == "committed" {
		return filepath.Join(t.lay.CheckoutRoot, ".lore")
	}

	return localLoreRoot(t.lay)
}

// loreNote names each missing kind, so a change that reached finish without a
// ship run shows it; "" when lore cannot be read, since finish never fails
// on it.
func loreNote(store *lore.Store, change string) string {
	entries, err := store.Effective(change)

	switch {
	case errors.Is(err, lore.ErrNoRecord):
		return "lore: no entries for " + change + ": no intent, no decisions, no lessons"
	case err != nil:
		return ""
	}

	count := map[lore.Kind]int{}
	for _, e := range entries {
		count[e.Kind]++
	}

	parts := []string{"no intent"}
	if count[lore.KindIntent] > 0 {
		parts[0] = "intent"
	}

	for _, k := range []struct {
		kind      lore.Kind
		one, many string
	}{{lore.KindDecision, "decision", "decisions"}, {lore.KindLesson, "lesson", "lessons"}} {
		if n := count[k.kind]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+pluralWord(n, k.one, k.many))
		} else {
			parts = append(parts, "no "+k.many)
		}
	}

	return "lore: " + strings.Join(parts, ", ")
}

// localLoreRoot is shared by every checkout: main's, when a worktree still
// keeps its own .f10.
func localLoreRoot(lay *layout.Layout) string {
	if lay.LegacyStorage && lay.MainRoot != "" {
		return filepath.Join(lay.MainRoot, ".f10", "lore")
	}

	return filepath.Join(lay.StorageRoot, "lore")
}

// repoPath is the repo-relative, slash-separated form anchors use.
func (l *loreEnv) repoPath(p string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(l.cwd, p)
	}

	rel, err := filepath.Rel(physical(l.top), physical(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the repository at %s", p, l.top)
	}

	return filepath.ToSlash(rel), nil
}

// physical resolves symlinks in the longest existing prefix of p, so a cwd
// under /tmp compares with git's /private/tmp.
func physical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}

	dir, base := filepath.Split(filepath.Clean(p))
	if dir == "" || filepath.Clean(dir) == p {
		return p
	}

	return filepath.Join(physical(filepath.Clean(dir)), base)
}

func runLoreAdd(ctx context.Context, cmd *cli.Command) error {
	kind := lore.Kind(cmd.Args().First())
	if !slices.Contains(lore.Kinds, kind) || cmd.Args().Len() != 1 {
		return fmt.Errorf("lore add takes one kind: intent, decision, finding, verdict or lesson (got %q)",
			strings.Join(cmd.Args().Slice(), " "))
	}

	l, err := loreFor(ctx, cmd)
	if err != nil {
		return err
	}

	body, err := loreBody(cmd)
	if err != nil {
		return err
	}

	e := lore.Entry{
		Kind:       kind,
		Change:     cmd.String("change"),
		Supersedes: cmd.String("supersedes"),
		Chosen:     cmd.String("chosen"),
		Severity:   cmd.String("severity"),
		Status:     cmd.String("status"),
		Resolution: cmd.String("resolution"),
		Source:     cmd.String("source"),
		Outcome:    cmd.String("outcome"),
		Gate:       cmd.String("gate"),
		Commit:     cmd.String("commit"),
		Judged:     cmd.String("judged"),
		Retired:    cmd.Bool("retired"),
		Agent:      loreAgent(cmd),
		Body:       body,
	}

	for _, r := range cmd.StringSlice("rejected") {
		option, reason, _ := strings.Cut(r, ":")
		e.Rejected = append(
			e.Rejected,
			lore.Rejected{Option: strings.TrimSpace(option), Reason: strings.TrimSpace(reason)},
		)
	}

	for _, s := range cmd.StringSlice("anchor") {
		a, err := lore.ParseAnchor(s)
		if err != nil {
			return err
		}

		if a.Path, err = l.repoPath(a.Path); err != nil {
			return err
		}

		if a.Commit == "" {
			head, err := l.store.Git.Head(ctx)

			switch {
			case errors.Is(err, lore.ErrNoRecord):
				return fmt.Errorf("anchor %s needs @commit: the repository has no commits yet", s)
			case err != nil:
				return err
			}

			a.Commit = head
		}

		e.Anchors = append(e.Anchors, a)
	}

	id, err := l.store.Add(ctx, e)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(cmd.Writer, id)

	return err
}

// loreBody is -m, else -F, else stdin when it is not a terminal.
func loreBody(cmd *cli.Command) (string, error) {
	if m := cmd.String("message"); m != "" {
		return m, nil
	}

	if f := cmd.String("file"); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}

		return string(data), nil
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("an entry needs a body: -m TEXT, -F FILE, or stdin")
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}

	return string(data), nil
}

// loreAgent is nil when neither flags nor environment name a model or a
// session.
func loreAgent(cmd *cli.Command) *lore.Agent {
	pick := func(flag, env string) string {
		if v := cmd.String(flag); v != "" {
			return v
		}

		return os.Getenv(env)
	}

	a := &lore.Agent{
		Model:   pick("agent-model", "F10_AGENT_MODEL"),
		Via:     pick("agent-via", "F10_AGENT_VIA"),
		Session: pick("agent-session", "F10_AGENT_SESSION"),
	}

	if a.Model == "" && a.Session == "" {
		return nil
	}

	if a.Via == "" && os.Getenv("CLAUDECODE") == "1" {
		a.Via = "claude-code"
	}

	return a
}

func runLoreShow(ctx context.Context, cmd *cli.Command) error {
	change, err := oneArg(cmd, "lore show", "<change>")
	if err != nil {
		return err
	}

	l, err := loreFor(ctx, cmd)
	if err != nil {
		return err
	}

	entries, err := l.store.Effective(change)

	switch {
	case errors.Is(err, lore.ErrNoRecord):
		entries = []lore.Entry{}
	case err != nil:
		return err
	}

	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, entries)
	}

	return lore.RenderShow(cmd.Writer, change, entries)
}

func runLoreWhy(ctx context.Context, cmd *cli.Command) error {
	arg, err := oneArg(cmd, "lore why", "<path:line[@rev]>")
	if err != nil {
		return err
	}

	// the anchor's own spelling: @rev asks about the line as that revision
	// has it, the base of a diff for a reviewer
	file, n, ok := strings.Cut(arg, ":")
	n, rev, hasRev := strings.Cut(n, "@")
	line, convErr := strconv.Atoi(n)

	// a revision starting with a dash would reach git blame as an option
	if !ok || convErr != nil || line < 1 || (hasRev && (rev == "" || strings.HasPrefix(rev, "-"))) {
		return fmt.Errorf("lore why takes path:line[@rev] (got %q)", arg)
	}

	l, err := loreFor(ctx, cmd)
	if err != nil {
		return err
	}

	path, err := l.repoPath(file)
	if err != nil {
		return err
	}

	res, err := l.store.Why(ctx, path, line, rev)

	switch {
	case errors.Is(err, lore.ErrNoRecord):
		if cmd.Bool("json") {
			return encodeJSON(cmd.Writer, lore.WhyResult{Path: path, Line: line, Rev: rev, Records: []lore.Record{}})
		}

		_, err = fmt.Fprintln(cmd.Writer, "no record")

		return err
	case err != nil:
		return err
	}

	if cmd.Bool("json") {
		return encodeJSON(cmd.Writer, res)
	}

	return lore.RenderWhy(cmd.Writer, res)
}

func runLoreLessons(ctx context.Context, cmd *cli.Command) error {
	arg, err := oneArg(cmd, "lore lessons", "<path>")
	if err != nil {
		return err
	}

	l, err := loreFor(ctx, cmd)
	if err != nil {
		return err
	}

	path, err := l.repoPath(arg)
	if err != nil {
		return err
	}

	lessons, err := l.store.Lessons(path)
	if err != nil {
		return err
	}

	if cmd.Bool("json") {
		if lessons == nil {
			lessons = []lore.Entry{}
		}

		return encodeJSON(cmd.Writer, lessons)
	}

	return lore.RenderLessons(cmd.Writer, path, lessons)
}

func runLoreCheck(ctx context.Context, cmd *cli.Command) error {
	change, err := oneArg(cmd, "lore check", "<change>")
	if err != nil {
		return err
	}

	l, err := loreFor(ctx, cmd)
	if err != nil {
		return err
	}

	res, err := l.store.Check(change)
	if err != nil {
		return err
	}

	if !res.Pass() {
		return fmt.Errorf("lore check %s: %s", change, strings.Join(res.Reasons, "; "))
	}

	_, err = fmt.Fprintf(cmd.Writer, "%s passes\n", change)

	return err
}

func runLoreValidate(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() > 1 {
		return errors.New("lore validate takes at most one change")
	}

	l, err := loreFor(ctx, cmd)
	if err != nil {
		return err
	}

	changes := cmd.Args().Slice()
	if len(changes) == 0 {
		if changes, err = l.store.Changes(); err != nil {
			return err
		}
	}

	var problems []lore.Problem

	for _, change := range changes {
		found, err := l.store.Validate(change)
		if errors.Is(err, lore.ErrNoRecord) {
			return fmt.Errorf("lore validate: no entries for %s", change)
		}

		if err != nil {
			return err
		}

		problems = append(problems, found...)
	}

	for _, p := range problems {
		fmt.Fprintln(cmd.Writer, p)
	}

	if len(problems) > 0 {
		return fmt.Errorf("lore validate: %d %s", len(problems), pluralWord(len(problems), "problem", "problems"))
	}

	_, err = fmt.Fprintf(cmd.Writer, "%d %s valid\n", len(changes), pluralWord(len(changes), "change", "changes"))

	return err
}

func oneArg(cmd *cli.Command, verb, usage string) (string, error) {
	if cmd.Args().Len() != 1 || strings.TrimSpace(cmd.Args().First()) == "" {
		return "", fmt.Errorf("%s takes %s", verb, usage)
	}

	return cmd.Args().First(), nil
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
