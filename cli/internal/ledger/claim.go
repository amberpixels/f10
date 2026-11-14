package ledger

import "github.com/amberpixels/f10/cli/internal/state"

// Claim is a recorded `f10 state` call: its arguments as the model typed
// them, and the run it left.
type Claim struct {
	Args []string
	Run  state.Run
}

// EncodeClaim is the payload of a claim row.
func EncodeClaim(r *state.Run, args []string) ClaimPayload {
	return ClaimPayload{
		Args: args, Capture: r.Capture, Plan: r.Plan, Ship: r.Ship,
		Leaf: r.Leaf, Final: r.Final, Note: r.Note, Next: r.Next, Ask: r.Ask, Wait: r.Wait, Done: r.Done,
		Pipeline: r.Pipeline, Labels: r.Labels, At: r.At,
	}
}

// DecodeClaim reads a claim row back; the row itself gives the session,
// task, checkout and time.
func DecodeClaim(e Event) Claim {
	p, _ := Decode[ClaimPayload](e)

	return Claim{
		Args: p.Args,
		Run: state.Run{
			Session:  e.Session,
			Task:     e.Task,
			Root:     e.Checkout,
			Capture:  state.LegacyStatus(p.Capture),
			Plan:     state.LegacyStatus(p.Plan),
			Ship:     state.LegacyStatus(p.Ship),
			Leaf:     p.Leaf,
			Final:    p.Final,
			Pipeline: p.Pipeline,
			Labels:   p.Labels,
			At:       p.At,
			Note:     p.Note,
			Next:     p.Next,
			Ask:      p.Ask,
			Wait:     p.Wait,
			Done:     p.Done,
			Updated:  e.At,
		},
	}
}
