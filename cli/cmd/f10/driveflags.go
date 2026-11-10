package main

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

// A driveFlag is one of drive's flags. The parser, its refusals, the record
// match and the usage line all read driveFlags, so a new flag is one row.
type driveFlag struct {
	name  string
	value string // the value's word in the usage, "" for a switch
	// alt is the flag this one is the other form of, shown as one choice
	alt string
	// resume is set when --resume takes the flag: it names none of the
	// drive's tasks, flow or agent profiles
	resume bool
	// tunes is set when the flag changes how a drive runs, not what it runs,
	// so a record is matched without it
	tunes bool
	many  bool // given once per value
}

var driveFlags = []driveFlag{
	{name: "--flow", value: "NAME"},
	{name: "--local", alt: "--flow"},
	{name: "--pace", value: "NAME", resume: true},
	{name: "--agent", value: "PROFILE"},
	{name: "--model", value: "MODEL", alt: "--agent"},
	{name: "--answer", value: "[ID=]TEXT", resume: true, tunes: true, many: true},
	{name: "--every", value: "DURATION", resume: true, tunes: true},
	{name: "--parallel", value: "N", resume: true, tunes: true},
	{name: "--resume", value: "N", resume: true, tunes: true},
	{name: "--foreground", resume: true, tunes: true},
}

func driveFlagNamed(name string) (driveFlag, bool) {
	i := slices.IndexFunc(driveFlags, func(f driveFlag) bool { return f.name == name })
	if i < 0 {
		return driveFlag{}, false
	}

	return driveFlags[i], true
}

// resumeTunes are the flags --resume N takes to change a drive: the ones
// that tune it and take a value.
func resumeTunes() []driveFlag {
	var out []driveFlag

	for _, f := range driveFlags {
		if f.resume && f.tunes && f.value != "" && f.name != "--resume" {
			out = append(out, f)
		}
	}

	return out
}

// flagList names flags as a sentence: --a, --b and --c.
func flagList(flags []driveFlag) string {
	names := make([]string, 0, len(flags))
	for _, f := range flags {
		names = append(names, f.name)
	}

	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// flagUsage is the usage of flags, one bracket each, a flag and its alt
// as one choice.
func flagUsage(flags []driveFlag) string {
	one := func(f driveFlag) string {
		s := f.name
		if f.value != "" {
			s += " " + f.value
		}

		if f.many {
			s += " ..."
		}

		return s
	}

	var parts []string

	for _, f := range flags {
		if f.alt != "" && slices.ContainsFunc(flags, func(g driveFlag) bool { return g.name == f.alt }) {
			continue
		}

		choice := []string{one(f)}

		for _, g := range flags {
			if g.alt == f.name {
				choice = append(choice, one(g))
			}
		}

		parts = append(parts, "["+strings.Join(choice, " | ")+"]")
	}

	return strings.Join(parts, " ")
}

// driveUsage is drive's ArgsUsage, read off driveFlags.
func driveUsage() string {
	run := slices.DeleteFunc(slices.Clone(driveFlags), func(f driveFlag) bool { return f.name == "--resume" })

	return "<id>[:profile]... | <id> -- <id>  " + flagUsage(run) + "\n" +
		"   f10 drive --resume N " + flagUsage(resumeTunes()) + " | list [--all] | cancel N"
}

// driveArgs is the command line, parsed by hand so `--` survives.
type driveArgs struct {
	tasks    []string // as typed, any :profile cut off; with span, the two ends of a range
	profiles []string // each task's :profile as typed, indexed like tasks, "" for none
	agent    string   // --agent as typed, or --model as a model-only profile: every task's unless its own names one
	model    string   // --model as typed, kept apart so both flags on one line are refused
	span     bool
	flow     string   // --flow, or local for --local; "" for default
	pace     string   // --pace, "" for the branch's or the project's
	answers  []string // each --answer as typed: `GH-1=text` for one task, or bare text
	every    time.Duration
	parallel int  // --parallel: the cap on tasks running a skill at once, 0 for the record's or the default
	resume   int  // --resume: the record to pick up
	fore     bool // --foreground: run the loop in this process, not the daemon
	list     bool // `drive list`
	all      bool // `drive list --all`
	cancel   int  // `drive cancel <n>`
}

func parseDriveArgs(args []string) (driveArgs, error) {
	a := driveArgs{every: defaultEvery}
	seen := map[string]bool{}

	if len(args) > 0 {
		switch strings.TrimSpace(args[0]) {
		case "list":
			return parseDriveList(args[1:])
		case "cancel":
			if len(args) != 2 {
				return driveArgs{}, errors.New("cancel takes one drive number: f10 drive cancel 3")
			}

			n, err := driveNumber(args[1])

			return driveArgs{cancel: n}, err
		}
	}

	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])

		name, value, inline := strings.Cut(tok, "=")
		if f, ok := driveFlagNamed(name); ok && f.value != "" {
			seen[name] = true

			if !inline {
				rest := args[i+1:]
				if len(rest) == 0 {
					return driveArgs{}, fmt.Errorf("%s needs a value", name)
				}

				value = rest[0]
				i++
			}

			if err := a.set(name, value); err != nil {
				return driveArgs{}, err
			}

			continue
		}

		switch {
		case tok == "":
			continue
		case tok == "--foreground" || tok == "--local":
			seen[tok] = true

			if err := a.set(tok, ""); err != nil {
				return driveArgs{}, err
			}
		case tok == "--":
			if a.span || len(a.tasks) != 1 {
				return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
			}

			a.span = true
		case strings.HasPrefix(tok, "-"):
			return driveArgs{}, fmt.Errorf("unknown flag %s: drive takes %s", tok, flagList(driveFlags))
		default:
			task, p, err := splitProfile(tok)
			if err != nil {
				return driveArgs{}, err
			}

			a.tasks, a.profiles = append(a.tasks, task), append(a.profiles, p)
		}
	}

	flag, err := runplan.ProfileFlag(a.agent, a.model)
	if err != nil {
		return driveArgs{}, err
	}

	a.agent, a.model = flag, ""

	if _, err := agentctl.ParseProfile(a.agent); err != nil {
		return driveArgs{}, fmt.Errorf("--agent %w", err)
	}

	for i, p := range a.profiles {
		if _, err := agentctl.ParseProfile(p); err != nil {
			return driveArgs{}, fmt.Errorf("%s:%s: %w", a.tasks[i], p, err)
		}
	}

	switch {
	case a.resume > 0 && (len(a.tasks) > 0 || slices.ContainsFunc(driveFlags, func(f driveFlag) bool {
		return seen[f.name] && !f.resume
	})):
		return driveArgs{}, fmt.Errorf("--resume %d runs the drive's own tasks, flow and agent profiles: "+
			"it takes only %s", a.resume, flagList(resumeTunes()))
	case a.resume > 0:
		return a, nil
	case len(a.tasks) == 0:
		return driveArgs{}, errors.New("drive needs the tasks: GH-12 GH-15, or a range GH-12 -- GH-15")
	case a.span && len(a.tasks) != 2:
		return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
	case a.span && slices.ContainsFunc(a.profiles, func(p string) bool { return p != "" }):
		return driveArgs{}, errors.New("a range takes the run-wide --agent, not a profile on its ends")
	}

	return a, nil
}

func (a *driveArgs) set(name, value string) error {
	switch name {
	case "--foreground":
		a.fore = true

		return nil
	case "--local":
		if a.flow != "" && a.flow != runplan.LocalFlow {
			return fmt.Errorf("--local and --flow %s name two flows", a.flow)
		}

		a.flow = runplan.LocalFlow

		return nil
	case "--resume":
		n, err := driveNumber(value)
		a.resume = n

		return err
	case "--parallel":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			return fmt.Errorf("--parallel %s: a positive number of tasks, like 4", value)
		}

		a.parallel = n

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
		if a.agent != "" {
			return errors.New("--agent is given twice: one run-wide profile, and <id>:<profile> for a task's own")
		}

		if a.agent = strings.TrimSpace(value); a.agent == "" {
			return errors.New("--agent needs a value")
		}

		return nil
	case "--answer":
		if value = strings.TrimSpace(value); value != "" {
			a.answers = append(a.answers, value)
		}

		return nil
	case "--pace":
		value = strings.ToLower(strings.TrimSpace(value))

		switch {
		case value == "":
			return errors.New("--pace needs a pace name")
		case a.pace != "" && a.pace != value:
			return fmt.Errorf("--pace %s: the run already names the %s pace", value, a.pace)
		}

		a.pace = value

		return nil
	case "--flow":
		value = strings.ToLower(strings.TrimSpace(value))

		switch {
		case value == "":
			return errors.New("--flow needs a flow name")
		case a.flow != "" && a.flow != value:
			return fmt.Errorf("--flow %s: the run already names the %s flow", value, a.flow)
		}

		a.flow = value

		return nil
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("--every %s: a positive duration, like 15s", value)
	}

	a.every = d

	return nil
}

func parseDriveList(args []string) (driveArgs, error) {
	a := driveArgs{list: true}

	for _, tok := range args {
		if tok != "--all" {
			return driveArgs{}, fmt.Errorf("drive list takes only --all, not %s", tok)
		}

		a.all = true
	}

	return a, nil
}

func driveNumber(s string) (int, error) {
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
