package drivectl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// StartKind is what a start's command line runs.
type StartKind int

const (
	// FlowStart runs tasks through their flow in a drive.
	FlowStart StartKind = iota
	// DiscussStart opens one agent that reads the tasks and waits.
	DiscussStart
	// PickupStart picks up a PR named by url, by !n, or with --task.
	PickupStart
	// MaybePickup is a bare #n, a PR or a task: the host decides, and a
	// task runs as FlowStart.
	MaybePickup
)

// OnceArgs is a discussion's or a pickup's command line.
type OnceArgs struct {
	Mode  string   // launch.ModeDiscuss, launch.ModePlan, or "" for a pickup that ships
	Token string   // the first id, or the PR
	Tasks []string // a discussion's other ids
	Note  string
	Flow  string
	Pace  string
	After string
	Base  string
	Task  string // the task a picked-up PR is for
	Agent string
	Model string
}

// StartArgs is start's command line, parsed once: its kind, and the
// arguments of that kind.
type StartArgs struct {
	Kind  StartKind
	Once  OnceArgs  // a discussion's, a pickup's, or a MaybePickup's
	Drive DriveArgs // a flow start's
	argv  []string
}

// Flow is the command line read as a flow start, for a MaybePickup the host
// named a task.
func (s StartArgs) Flow() (DriveArgs, error) { return ParseDriveArgs(s.argv) }

// ParseStart reads start's command line. --discuss, --task or a first word
// naming a PR make a one-shot start. Anything else is a flow start, which
// takes ids, a range and the drive's flags.
func ParseStart(argv []string) (StartArgs, error) {
	named := false

	for _, tok := range argv {
		if f, ok := OnceFlagNamed(cutName(tok)); ok && f.OnceOnly {
			named = true
		}
	}

	once, words, err := parseOnce(argv)

	flow := func() (StartArgs, error) {
		a, err := ParseDriveArgs(argv)

		return StartArgs{Kind: FlowStart, Drive: a, argv: argv}, err
	}

	switch {
	case errors.Is(err, errDriveFlag) && !named:
		return flow()
	case err != nil:
		return StartArgs{}, err
	case len(words) == 0 && named:
		return StartArgs{}, errors.New("start needs a task id")
	case len(words) == 0:
		return flow()
	}

	kind := DiscussStart

	switch {
	case once.Mode == launch.ModeDiscuss:
	case once.Task != "":
		kind = PickupStart
	default:
		number, probe := worktree.PullToken(words[0])

		switch {
		case number == "":
			return flow()
		case probe:
			kind = MaybePickup
		default:
			kind = PickupStart
		}
	}

	once.Token, words = words[0], words[1:]

	// a shape is enough here, because the API resolves each id
	if kind == DiscussStart {
		n := listed(words)
		once.Tasks, words = words[:n], words[n:]
	}

	once.Note = strings.Join(words, " ")

	return StartArgs{Kind: kind, Once: once, argv: argv}, nil
}

func cutName(tok string) string {
	name, _, _ := strings.Cut(tok, "=")

	return name
}

// listed counts the leading words shaped as task ids: a discussion's other
// tasks, before its note.
func listed(words []string) int {
	for i, w := range words {
		if !ref.IsID(w) {
			return i
		}
	}

	return len(words)
}

// errDriveFlag is a flag only a flow start takes.
var errDriveFlag = errors.New("a flow start's flag")

// parseOnce reads argv as a one-shot start: the flags StartFlags marks Once,
// then the token and the note's words.
func parseOnce(argv []string) (OnceArgs, []string, error) {
	var (
		once  OnceArgs
		words []string
	)

	for i := 0; i < len(argv); i++ {
		name, value, inline := strings.Cut(argv[i], "=")

		f, ok := OnceFlagNamed(name)
		if !ok {
			if name == "--" {
				return OnceArgs{}, nil, fmt.Errorf("a range: %w; --discuss takes the ids one by one", errDriveFlag)
			}

			if strings.HasPrefix(name, "-") {
				return OnceArgs{}, nil, fmt.Errorf(
					"%s: %w; --discuss and a PR pickup take ids and a note",
					name,
					errDriveFlag,
				)
			}

			words = append(words, argv[i])

			continue
		}

		if f.Value != "" && !inline {
			if i+1 >= len(argv) {
				return OnceArgs{}, nil, fmt.Errorf("%s needs a value", name)
			}

			i++
			value = argv[i]
		}

		if err := once.set(name, strings.TrimSpace(value)); err != nil {
			return OnceArgs{}, nil, err
		}
	}

	return once, words, RefusePairs(once.Flow, once.Mode, once.After, once.Base)
}

func (o *OnceArgs) set(name, value string) error {
	switch name {
	case "--discuss", "--plan":
		mode := strings.TrimPrefix(name, "--")
		if o.Mode != "" && o.Mode != mode {
			return errors.New("--plan and --discuss are exclusive: each names how far the agent runs")
		}

		o.Mode = mode
	case "--local":
		o.Flow = "local"
	case "--flow":
		o.Flow = value
	case "--pace":
		o.Pace = value
	case "--after":
		o.After = value
	case "--base":
		o.Base = value
	case "--task":
		o.Task = value
	case "--agent":
		o.Agent = value
	case "--model":
		o.Model = value
	}

	return nil
}
