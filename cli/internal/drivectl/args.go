package drivectl

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/runplan"
)

// A DriveFlag is one of drive's flags. The parser, its refusals, the record
// match and the usage line all read DriveFlags, so a new flag is one row.
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
}

var DriveFlags = []DriveFlag{
	{Name: "--flow", Value: "NAME"},
	{Name: "--local", Alt: "--flow"},
	{Name: "--pace", Value: "NAME", Resume: true},
	{Name: "--agent", Value: "PROFILE"},
	{Name: "--model", Value: "MODEL", Alt: "--agent"},
	{Name: "--answer", Value: "[ID=]TEXT", Resume: true, Tunes: true, Many: true},
	{Name: "--every", Value: "DURATION", Resume: true, Tunes: true},
	{Name: "--parallel", Value: "N", Resume: true, Tunes: true},
	{Name: "--resume", Value: "N", Resume: true, Tunes: true},
	{Name: "--foreground", Resume: true, Tunes: true},
}

func DriveFlagNamed(name string) (DriveFlag, bool) {
	i := slices.IndexFunc(DriveFlags, func(f DriveFlag) bool { return f.Name == name })
	if i < 0 {
		return DriveFlag{}, false
	}

	return DriveFlags[i], true
}

// ResumeTunes are the flags --resume N takes to change a drive: the ones
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

	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// DriveArgs is the command line, parsed by hand so `--` survives.
type DriveArgs struct {
	Tasks    []string // as typed, any :profile cut off; with span, the two ends of a range
	Profiles []string // each task's :profile as typed, indexed like tasks, "" for none
	Agent    string   // --agent as typed, or --model as a model-only profile: every task's unless its own names one
	model    string   // --model as typed, kept apart so both flags on one line are refused
	Span     bool
	Flow     string   // --flow, or local for --local; "" for default
	Pace     string   // --pace, "" for the branch's or the project's
	Answers  []string // each --answer as typed: `GH-1=text` for one task, or bare text
	Every    time.Duration
	Parallel int  // --parallel: the cap on tasks running a skill at once, 0 for the record's or the default
	Resume   int  // --resume: the record to pick up
	Fore     bool // --foreground: run the loop in this process, not the daemon
	List     bool // `drive list`
	All      bool // `drive list --all`
	Cancel   int  // `drive cancel <n>`
}

func (a *DriveArgs) set(name, value string) error {
	switch name {
	case "--foreground":
		a.Fore = true

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

	if len(args) > 0 {
		switch strings.TrimSpace(args[0]) {
		case "list":
			return parseDriveList(args[1:])
		case "cancel":
			if len(args) != 2 {
				return DriveArgs{}, errors.New("cancel takes one drive number: f10 drive cancel 3")
			}

			n, err := DriveNumber(args[1])

			return DriveArgs{Cancel: n}, err
		}
	}

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
		case tok == "--foreground" || tok == "--local":
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
			return DriveArgs{}, fmt.Errorf("unknown flag %s: drive takes %s", tok, flagList(DriveFlags))
		default:
			task, p, err := splitProfile(tok)
			if err != nil {
				return DriveArgs{}, err
			}

			a.Tasks, a.Profiles = append(a.Tasks, task), append(a.Profiles, p)
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
		return DriveArgs{}, fmt.Errorf("--resume %d runs the drive's own tasks, flow and agent profiles: "+
			"it takes only %s", a.Resume, flagList(ResumeTunes()))
	case a.Resume > 0:
		return a, nil
	case len(a.Tasks) == 0:
		return DriveArgs{}, errors.New("drive needs the tasks: GH-12 GH-15, or a range GH-12 -- GH-15")
	case a.Span && len(a.Tasks) != 2:
		return DriveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
	case a.Span && slices.ContainsFunc(a.Profiles, func(p string) bool { return p != "" }):
		return DriveArgs{}, errors.New("a range takes the run-wide --agent, not a profile on its ends")
	}

	return a, nil
}

func parseDriveList(args []string) (DriveArgs, error) {
	a := DriveArgs{List: true}

	for _, tok := range args {
		if tok != "--all" {
			return DriveArgs{}, fmt.Errorf("drive list takes only --all, not %s", tok)
		}

		a.All = true
	}

	return a, nil
}

func DriveNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a drive number: f10 drive list shows them", s)
	}

	return n, nil
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
