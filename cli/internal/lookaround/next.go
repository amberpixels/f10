package lookaround

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// The commands a row's next action names. One table, so every renderer
// and the board skill print the same words.
const (
	cmdFinish  = "f10 finish %s"
	cmdStart   = "f10 start %s"
	cmdStatus  = "/f10:status %s"
	cmdShip    = "/f10:ship %s"
	cmdReview  = "/f10:review %s"
	cmdResolve = "/f10:resolve %s"
	cmdPlan    = "/f10:plan %s"
	cmdCI      = "/f10:resolve %s ci"
)

// next fills the row's next action from its stage and the evidence
// behind it. A row with no task id gets a sentence where a command would
// need one.
func (r *row) next() {
	id := ""
	if !strings.HasPrefix(r.Key, "#") {
		id = r.Key
	}

	command := func(format string) Next {
		if id == "" {
			return Next{}
		}

		return Next{Command: fmt.Sprintf(format, id)}
	}

	text := func(s string) Next { return Next{Text: s} }

	switch r.Stage {
	case StageDone:
		r.Next = Next{}
	case StageMergedUnfinished, StageReady:
		r.Next = command(cmdFinish)
	case StageAsksWaiting:
		if r.StageFrom == FromLocal {
			r.Next = command(cmdStatus)
		} else {
			r.Next = text("waiting on an answer")
		}
	case StageBlocked:
		switch {
		case len(r.BlockedBy) > 0:
			r.Next = text("waiting on " + strings.Join(r.BlockedBy, ", "))
		case r.stopped():
			r.Next = text(stoppedText(r))
		case r.verdict == "rethink":
			r.Next = command(cmdPlan)
		default:
			r.Next = text("judged " + r.verdict)
		}
	case StageInReview:
		switch {
		case r.Review != nil && r.Review.RemoteOpen:
			r.Next = command(cmdCI)
		case r.Review != nil && r.Review.Open > 0:
			r.Next = command(cmdResolve)
		case r.Review != nil && r.Review.ChangesRequested > 0:
			r.Next = text("changes requested")
		case r.running():
			r.Next = text("at " + r.run.Leaf)
		default:
			r.Next = text("waiting on review")
		}
	case StagePROpen:
		r.Next = command(cmdReview)
	case StageImplementing:
		switch {
		case r.running():
			r.Next = text("at " + cmp.Or(r.run.Leaf, "implement"))
		case r.worktree != nil:
			r.Next = command(cmdShip)
		default:
			r.Next = text("in progress")
		}
	case StagePlanned:
		switch {
		case r.running():
			r.Next = command(cmdStatus)
		case r.worktree != nil:
			r.Next = command(cmdShip)
		default:
			r.Next = command(cmdStart)
		}
	case StageNext:
		r.Next = command(cmdStart)
	}

	// a row with no task id gets the sentence a command would have been
	if r.Next == (Next{}) && r.Stage != StageDone {
		r.Next = text(map[string]string{
			StageMergedUnfinished: "merged, branch left behind",
			StageReady:            "ready to merge",
			StagePROpen:           "open, no review yet",
			StagePlanned:          "planned",
			StageNext:             "not started",
		}[r.Stage])
	}
}

func stoppedText(r *row) string {
	phase := "ship"
	for _, p := range []string{"capture", "plan"} {
		if st := r.run.Status(p); st == "failed" || st == "partial" || st == "blocked" {
			phase = p
		}
	}

	out := phase + " " + r.run.PhaseText(phase)

	if r.run.Note != "" {
		out += ": " + r.run.Note
	}

	return out
}

// AgentFor is the name start gave the task's agent in the worktree: the
// id and the suffix its branch carries, hashed with the path.
func AgentFor(id, branch, path string) string {
	head, _, _ := strings.Cut(branch, "/")
	suffix := ""

	if _, s := ref.SplitSuffix(head); s != "" && !strings.Contains(head, ref.Separator) {
		suffix = s
	}

	return herdr.AgentName(id, suffix, path)
}
