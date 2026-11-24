package drivectl

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// A DriveFlag is one of start's flags. Every parse of start's command line
// reads StartFlags, so a new flag is one row. A flow start reads it through
// DriveFlags, and a discussion or a pickup through the rows marked Once.
type DriveFlag struct {
	Name  string
	Value string // the value's word in the usage, "" for a switch
	// Alt is the flag this one is the other form of, shown as one choice
	Alt string
	// Resume is set when --resume takes the flag: it names none of the
	// drive's tasks, flow or agent profiles
	Resume bool
	// Tunes is set when the flag changes how a drive runs, not what it runs,
	// so a record is matched without it
	Tunes bool
	Many  bool // given once per value
	// Once is set when a discussion or a PR pickup takes the flag too
	Once bool
	// OnceOnly is set for a flag only a discussion or a pickup takes
	OnceOnly bool
}

// StartFlags is every flag f10 start takes.
var StartFlags = []DriveFlag{
	{Name: "--flow", Value: "NAME", Once: true},
	{Name: "--local", Alt: "--flow", Once: true},
	{Name: "--pace", Value: "NAME", Resume: true, Once: true},
	{Name: "--agent", Value: "PROFILE", Once: true},
	{Name: "--model", Value: "MODEL", Alt: "--agent", Once: true},
	{Name: "--plan", Once: true},
	{Name: "--after", Value: "TASK-ID", Once: true},
	{Name: "--base", Value: "BRANCH", Alt: "--after", Once: true},
	{Name: "--answer", Value: "[ID=]TEXT", Resume: true, Tunes: true, Many: true},
	{Name: "--every", Value: "DURATION", Resume: true, Tunes: true},
	{Name: "--parallel", Value: "N", Resume: true, Tunes: true},
	{Name: "--resume", Value: "N", Resume: true, Tunes: true},
	{Name: "--foreground", Resume: true, Tunes: true},
	{Name: "--discuss", Once: true, OnceOnly: true},
	{Name: "--task", Value: "TASK-ID", Once: true, OnceOnly: true},
}

// DriveFlags are the flags a flow start takes: StartFlags less the ones only
// a discussion or a pickup takes.
var DriveFlags = slices.DeleteFunc(slices.Clone(StartFlags), func(f DriveFlag) bool { return f.OnceOnly })

func DriveFlagNamed(name string) (DriveFlag, bool) {
	i := slices.IndexFunc(DriveFlags, func(f DriveFlag) bool { return f.Name == name })
	if i < 0 {
		return DriveFlag{}, false
	}

	return DriveFlags[i], true
}

// A FlagValue is one flag a caller builds a start from: a switch when its
// value is "true", else the name and the value, and left out when empty.
type FlagValue struct{ Name, Value string }

// AppendFlags appends flags to argv in the order given, each checked against
// StartFlags, so a caller building a start from fields keeps no table of its
// own. The order is the caller's because the drive record's key is the
// command line as written, and a rerun attaches to its drive by that key.
func AppendFlags(argv []string, flags ...FlagValue) ([]string, error) {
	for _, fv := range flags {
		i := slices.IndexFunc(StartFlags, func(f DriveFlag) bool { return f.Name == fv.Name })
		if i < 0 {
			return nil, fmt.Errorf("%s is not a start flag", fv.Name)
		}

		v := strings.TrimSpace(fv.Value)

		switch f := StartFlags[i]; {
		case v == "" || (f.Value == "" && v == "false"):
		case f.Value == "" && v == "true":
			argv = append(argv, f.Name)
		case f.Value == "":
			return nil, fmt.Errorf("%s takes no value", f.Name)
		default:
			argv = append(argv, f.Name, v)
		}
	}

	return argv, nil
}

// OnceFlagNamed is the row of a flag a discussion or a pickup takes.
func OnceFlagNamed(name string) (DriveFlag, bool) {
	i := slices.IndexFunc(StartFlags, func(f DriveFlag) bool { return f.Name == name && f.Once })
	if i < 0 {
		return DriveFlag{}, false
	}

	return StartFlags[i], true
}

// RefusePairs refuses the exclusive pairs every start shares, worded once:
// a mode that stops before ship beside a flow, and a base given two ways.
func RefusePairs(flow, mode, after, base string) error {
	if flow != "" && mode != "" {
		return fmt.Errorf(
			"--flow %s and --%s are exclusive: --%s stops before ship, which runs the flow",
			flow,
			mode,
			mode,
		)
	}

	if strings.TrimSpace(after) != "" && strings.TrimSpace(base) != "" {
		return errAfterAndBase
	}

	return nil
}

var errAfterAndBase = errors.New("--after and --base are exclusive: one names a task to depend on, the other a git ref")

// ResumeTunes are the flags a resume takes to change a batch: the ones
// that tune it and take a value.
func ResumeTunes() []DriveFlag {
	var out []DriveFlag

	for _, f := range DriveFlags {
		if f.Resume && f.Tunes && f.Value != "" && f.Name != "--resume" {
			out = append(out, f)
		}
	}

	return out
}

// flagList names flags as a sentence: --a, --b and --c.
func flagList(flags []DriveFlag) string {
	names := make([]string, 0, len(flags))
	for _, f := range flags {
		names = append(names, f.Name)
	}

	return textx.JoinAnd(names)
}

// DriveArgs is the command line, parsed by hand so `--` survives.
type DriveArgs struct {
	Tasks    []string // as typed, any :profile cut off; with span, the two ends of a range
	Profiles []string // each task's :profile as typed, indexed like tasks, "" for none
	// Bases are each task's typed bases, indexed like tasks, nil for none:
	// the entry before it in a chain, then each @base, as typed
	Bases    [][]string
	Agent    string // --agent as typed, or --model as a model-only profile: every task's unless its own names one
	model    string // --model as typed, kept apart so both flags on one line are refused
	Span     bool
	Flow     string   // --flow, or local for --local; "" for default
	Pace     string   // --pace, "" for the branch's or the project's
	Answers  []string // each --answer as typed: `GH-1=text` for one task, or bare text
	Every    time.Duration
	Parallel int    // --parallel: the cap on tasks running a skill at once, 0 for the record's or the default
	Resume   int    // --resume: the record to pick up
	Fore     bool   // --foreground: run the loop in this process, not the daemon
	PlanOnly bool   // --plan: every task is planned and none implemented
	After    string // --after: the one task's base task, over its body's After: line
	Base     string // --base: the one task's base branch
	Plan     *PlanArgs
}

// PlanArgs is `start --propose`'s own flags; the rest parse as a start's.
type PlanArgs struct {
	Facts string   // --facts: the facts sheet the skill wrote
	JSON  bool     // --json
	Hint  bool     // --hint: one line, or nothing
	Pass  []string // the start flags to carry onto the proposed command
}

// Ordered reports a typed base on any task: a chain's , or an @.
func (a *DriveArgs) Ordered() bool {
	return slices.ContainsFunc(a.Bases, func(b []string) bool { return len(b) > 0 })
}

func (a *DriveArgs) set(name, value string) error {
	switch name {
	case "--foreground":
		a.Fore = true

		return nil
	case "--plan":
		a.PlanOnly = true

		return nil
	case "--after", "--base":
		if a.After != "" || a.Base != "" {
			return errAfterAndBase
		}

		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s needs a value", name)
		}

		if name == "--after" {
			a.After = value
		} else {
			a.Base = value
		}

		return nil
	case "--local":
		if a.Flow != "" && a.Flow != runplan.LocalFlow {
			return fmt.Errorf("--local and --flow %s name two flows", a.Flow)
		}

		a.Flow = runplan.LocalFlow

		return nil
	case "--resume":
		n, err := DriveNumber(value)
		a.Resume = n

		return err
	case "--parallel":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			return fmt.Errorf("--parallel %s: a positive number of tasks, like 4", value)
		}

		a.Parallel = n

		return nil
	case "--model":
		if a.model != "" {
			return errors.New("--model is given twice: one run-wide model, and <id>:<model> for a task's own")
		}

		if a.model = strings.TrimSpace(value); a.model == "" {
			return errors.New("--model needs a value")
		}

		return nil
	case "--agent":
		if a.Agent != "" {
			return errors.New("--agent is given twice: one run-wide profile, and <id>:<profile> for a task's own")
		}

		if a.Agent = strings.TrimSpace(value); a.Agent == "" {
			return errors.New("--agent needs a value")
		}

		return nil
	case "--answer":
		if value = strings.TrimSpace(value); value != "" {
			a.Answers = append(a.Answers, value)
		}

		return nil
	case "--pace":
		value = strings.ToLower(strings.TrimSpace(value))

		switch {
		case value == "":
			return errors.New("--pace needs a pace name")
		case a.Pace != "" && a.Pace != value:
			return fmt.Errorf("--pace %s: the run already names the %s pace", value, a.Pace)
		}

		a.Pace = value

		return nil
	case "--flow":
		value = strings.ToLower(strings.TrimSpace(value))

		switch {
		case value == "":
			return errors.New("--flow needs a flow name")
		case a.Flow != "" && a.Flow != value:
			return fmt.Errorf("--flow %s: the run already names the %s flow", value, a.Flow)
		}

		a.Flow = value

		return nil
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("--every %s: a positive duration, like 15s", value)
	}

	a.Every = d

	return nil
}

func ParseDriveArgs(args []string) (DriveArgs, error) {
	a := DriveArgs{Every: DefaultEvery}
	seen := map[string]bool{}

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])

		name, value, inline := strings.Cut(tok, "=")
		if f, ok := DriveFlagNamed(name); ok && f.Value != "" {
			seen[name] = true

			if !inline {
				rest := args[i+1:]
				if len(rest) == 0 {
					return DriveArgs{}, fmt.Errorf("%s needs a value", name)
				}

				value = rest[0]
				i++
			}

			if err := a.set(name, value); err != nil {
				return DriveArgs{}, err
			}

			continue
		}

		switch {
		case tok == "":
			continue
		case tok == "--foreground" || tok == "--local" || tok == "--plan":
			seen[tok] = true

			if err := a.set(tok, ""); err != nil {
				return DriveArgs{}, err
			}
		case tok == "--":
			if a.Span || len(a.Tasks) != 1 {
				return DriveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
			}

			a.Span = true
		case strings.HasPrefix(tok, "-"):
			return DriveArgs{}, fmt.Errorf("unknown flag %s: start takes %s", tok, flagList(DriveFlags))
		default:
			entries, err := parseEntries(tok)
			if err != nil {
				return DriveArgs{}, err
			}

			for _, e := range entries {
				a.Tasks, a.Profiles = append(a.Tasks, e.task), append(a.Profiles, e.profile)
				a.Bases = append(a.Bases, e.bases)
			}
		}
	}

	flag, err := runplan.ProfileFlag(a.Agent, a.model)
	if err != nil {
		return DriveArgs{}, err
	}

	a.Agent, a.model = flag, ""

	if _, err := agentctl.ParseProfile(a.Agent); err != nil {
		return DriveArgs{}, fmt.Errorf("--agent %w", err)
	}

	for i, p := range a.Profiles {
		if _, err := agentctl.ParseProfile(p); err != nil {
			return DriveArgs{}, fmt.Errorf("%s:%s: %w", a.Tasks[i], p, err)
		}
	}

	switch {
	case a.Resume > 0 && (len(a.Tasks) > 0 || slices.ContainsFunc(DriveFlags, func(f DriveFlag) bool {
		return seen[f.Name] && !f.Resume
	})):
		return DriveArgs{}, fmt.Errorf("a resume runs the batch's own tasks, flow and agent profiles: "+
			"it takes only %s", flagList(ResumeTunes()))
	case a.Resume > 0:
		return a, nil
	case len(a.Tasks) == 0:
		return DriveArgs{}, errors.New("start needs the tasks: GH-12, GH-12 GH-15, or a range GH-12 -- GH-15")
	case a.PlanOnly && a.Flow != "":
		return DriveArgs{}, RefusePairs(a.Flow, "plan", "", "")
	case (a.After != "" || a.Base != "") && (len(a.Tasks) != 1 || a.Span):
		return DriveArgs{}, errors.New("--after and --base set one task's base: start that task alone")
	case (a.After != "" || a.Base != "") && a.Ordered():
		return DriveArgs{}, fmt.Errorf("--after, --base and @ each name %s's base: give it one way", a.Tasks[0])
	case a.Span && a.Ordered():
		return DriveArgs{}, errors.New("a range takes no order: list the ids, A,B or C@A")
	case a.Span && len(a.Tasks) != 2:
		return DriveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
	case a.Span && slices.ContainsFunc(a.Profiles, func(p string) bool { return p != "" }):
		return DriveArgs{}, errors.New("a range takes the run-wide --agent, not a profile on its ends")
	}

	return a, nil
}

// Proposes reports a command line asking for a proposal, which starts
// nothing: --propose asks for one, and --hint alone for its one line.
// --facts and --json alone are refused, because they shape a proposal
// nobody asked for.
func Proposes(args []string) (bool, error) {
	asked, shaped := false, ""

	for _, tok := range args {
		switch name, _, _ := strings.Cut(strings.TrimSpace(tok), "="); name {
		case "--propose", "--hint":
			asked = true
		case "--facts", "--json":
			shaped = name
		}
	}

	if shaped != "" && !asked {
		return false, fmt.Errorf("%s shapes a proposal: add --propose", shaped)
	}

	return asked, nil
}

// ParsePropose reads a proposal's command line: ids or a parent, the start
// flags that shape the proposed command, and the proposal's own flags.
func ParsePropose(args []string) (DriveArgs, error) {
	pa := &PlanArgs{}

	var rest []string

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])
		name, value, inline := strings.Cut(tok, "=")

		switch name {
		case "--propose":
		case "--json":
			pa.JSON = true
		case "--hint":
			pa.Hint = true
		case "--facts":
			if !inline {
				if i+1 >= len(args) {
					return DriveArgs{}, errors.New("--facts needs the facts sheet's path")
				}

				i++
				value = args[i]
			}

			pa.Facts = value
		default:
			rest = append(rest, tok)
		}
	}

	a, err := ParseDriveArgs(rest)
	if err != nil {
		return DriveArgs{}, fmt.Errorf("start --propose: %w", err)
	}

	switch {
	case a.Span:
		return DriveArgs{}, errors.New("start --propose takes ids or a parent, not a range")
	case a.Ordered():
		return DriveArgs{}, errors.New("start --propose sets the order itself: list the ids with spaces, no , or @")
	case a.Resume > 0, len(a.Answers) > 0, a.Fore, a.Every != DefaultEvery,
		a.PlanOnly, a.After != "", a.Base != "":
		return DriveArgs{}, errors.New("start --propose takes ids or a parent, --facts, --json, --hint " +
			"and the flags that shape a start: --flow, --local, --pace, --agent, --model and --parallel")
	}

	for i := 0; i < len(rest); i++ {
		name, _, inline := strings.Cut(rest[i], "=")

		f, ok := DriveFlagNamed(name)
		if !ok || name == "--parallel" {
			if ok && !inline {
				i++
			}

			continue
		}

		pa.Pass = append(pa.Pass, rest[i])

		if f.Value != "" && !inline {
			i++
			pa.Pass = append(pa.Pass, rest[i])
		}
	}

	a.Plan = pa

	return a, nil
}

// ResumeArgs is `f10 resume`'s command line. A task id names the batch, and
// for one more release so does its number.
type ResumeArgs struct {
	Token string   // "" for the one stopped batch
	Flags []string // as typed, to put after --resume N
	Fore  bool
}

// ParseResume reads `f10 resume`'s arguments. It takes the flags --resume
// took, so the two never drift.
func ParseResume(args []string) (ResumeArgs, error) {
	var r ResumeArgs

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])
		name, _, inline := strings.Cut(tok, "=")

		f, ok := DriveFlagNamed(name)

		switch {
		case tok == "":
		case ok && f.Resume && f.Tunes && name != "--resume":
			r.Flags = append(r.Flags, tok)
			r.Fore = r.Fore || name == "--foreground"

			if f.Value != "" && !inline {
				if i+1 >= len(args) {
					return ResumeArgs{}, fmt.Errorf("%s needs a value", name)
				}

				i++
				r.Flags = append(r.Flags, args[i])
			}
		case strings.HasPrefix(tok, "-"):
			return ResumeArgs{}, fmt.Errorf("resume runs the batch's own tasks, flow and agent profiles: "+
				"it takes only %s and --foreground", flagList(ResumeTunes()))
		case r.Token != "":
			return ResumeArgs{}, errors.New("resume takes one task id: any task of the batch names it")
		default:
			r.Token = tok
		}
	}

	return r, nil
}

func DriveNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a batch number: f10 status --batches shows them", s)
	}

	return n, nil
}

// An entry is one task of a token, with its profile and its typed bases.
type entry struct {
	task, profile string
	bases         []string
}

// parseEntries reads a token as a chain of entries, each
// <task>[-suffix][@<base>...][:profile]: `,` stacks an entry on the one
// before it, and each @ names one more base. A url is taken whole, because
// it may hold either mark.
func parseEntries(tok string) ([]entry, error) {
	if strings.Contains(tok, "://") {
		task, p, err := splitProfile(tok)

		return []entry{{task: task, profile: p}}, err
	}

	var (
		out  []entry
		prev string
	)

	for link := range strings.SplitSeq(tok, ",") {
		if link == "" {
			return nil, fmt.Errorf("%s: an empty entry beside a comma: A,B stacks B on A", tok)
		}

		head, p, err := splitProfile(link)
		if err != nil {
			return nil, err
		}

		if prof, bases, ok := strings.Cut(p, "@"); ok {
			return nil, fmt.Errorf("%s: the profile comes last: %s@%s:%s", link, head, bases, prof)
		}

		parts := strings.Split(head, "@")
		if parts[0] == "" {
			return nil, fmt.Errorf("%s names no task before @: C@A", link)
		}

		var bases []string
		if prev != "" {
			bases = append(bases, prev)
		}

		for _, b := range parts[1:] {
			switch _, suffix := ref.SplitSuffix(b); {
			case b == "":
				return nil, fmt.Errorf("%s: an @ names no base: C@A", link)
			case suffix != "":
				return nil, fmt.Errorf("%s: a base is a task id, with no -%s", link, suffix)
			case slices.ContainsFunc(bases, func(x string) bool { return strings.EqualFold(x, b) }):
				return nil, fmt.Errorf("%s names %s as a base twice", link, b)
			}

			bases = append(bases, b)
		}

		prev, _ = ref.SplitSuffix(parts[0])
		out = append(out, entry{task: parts[0], profile: p, bases: bases})
	}

	return out, nil
}

// splitProfile cuts a task token's :profile off: the first colon whose
// remainder holds no slash, so `FT-1:claude:opus:high` splits after the
// id, and a url's scheme and port stay part of the task.
func splitProfile(tok string) (string, string, error) {
	for i := range len(tok) {
		if tok[i] != ':' || strings.Contains(tok[i+1:], "/") {
			continue
		}

		if tok[i+1:] == "" {
			return "", "", fmt.Errorf("%s names no profile after the colon: GH-12:opus", tok)
		}

		return tok[:i], tok[i+1:], nil
	}

	return tok, "", nil
}

// Renamed is the one line an old spelling prints on stderr, for the one
// release it still works.
func Renamed(old, now string) string {
	return fmt.Sprintf("%s still works for one release: use %s", old, now)
}

// OldNumber reads a token as a batch number, the old way to name a batch.
// A task id names it now, so a positive integer is read this way for one
// more release only.
func OldNumber(token string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(token))

	return n, err == nil && n > 0
}
