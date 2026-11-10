package hostops

import (
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
)

type Pickup struct {
	Number string
	URL    string
	Branch string // the request's head, as the host names it
	View   string // the f10 command that reads the request with its comments
	Diff   string
	// Sha is where a merged or closed request is checked out, "" for an open
	// one; state says which it is, for the prompt and the report; headRef is
	// the host's ref for a closed request's head, which no branch holds
	SHA     string
	State   string
	HeadRef string
}

// Landed reports a request that merged or closed, checked out at sha.
func (p *Pickup) Landed() bool { return p.SHA != "" }

// landedState is how a merged or closed request is named to the agent and
// in the report: who merged it and when, or that it closed unmerged.
func landedState(p forge.PR) string {
	switch p.Status {
	case forge.Merged:
		state := "merged"
		if by := p.MergedBy; by != "" {
			state += " by " + by
		}

		return state + onDay(p.MergedAt)
	case forge.Closed:
		return "closed without a merge" + onDay(p.ClosedAt)
	}

	return ""
}

func onDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return " on " + t.UTC().Format(time.DateOnly)
}
