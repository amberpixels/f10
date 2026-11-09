package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

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
		if name == "--model" || name == "--agent" || name == "--answer" || name == "--every" || name == "--flow" ||
			name == "--resume" || name == "--parallel" || name == "--pace" {
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
		case tok == "--foreground":
			a.fore = true
		case tok == "--local":
			if a.flow != "" && a.flow != localFlow {
				return driveArgs{}, fmt.Errorf("--local and --flow %s name two flows", a.flow)
			}

			a.flow = localFlow
		case tok == "--":
			if a.span || len(a.tasks) != 1 {
				return driveArgs{}, errors.New("a range is one id, --, one id: GH-12 -- GH-15")
			}

			a.span = true
		case strings.HasPrefix(tok, "-"):
			return driveArgs{}, fmt.Errorf(
				"unknown flag %s: drive takes --flow, --local, --pace, --agent, --model, --parallel, --answer, --every, --resume "+
					"and --foreground",
				tok,
			)
		default:
			task, p, err := splitProfile(tok)
			if err != nil {
				return driveArgs{}, err
			}

			a.tasks, a.profiles = append(a.tasks, task), append(a.profiles, p)
		}
	}

	flag, err := profileFlag(a.agent, a.model)
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
	case a.resume > 0 && (len(a.tasks) > 0 || a.flow != "" || a.agent != ""):
		return driveArgs{}, fmt.Errorf("--resume %d runs the drive's own tasks, flow and agent profiles: "+
			"it takes only --answer, --every and --parallel", a.resume)
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
