package ledger

import "github.com/amberpixels/f10/cli/internal/state"

// Claim is a recorded `f10 state` call: its arguments as the model typed
// them, and the run it left.
type Claim struct {
	Args []string
	Run  state.Run
}

// EncodeClaim is the payload of a claim row.
func EncodeClaim(r *state.Run, args []string) *ClaimPayload {
	return &ClaimPayload{
		Args: args, Capture: r.Capture, Plan: r.Plan, Ship: r.Ship,
		Leaf: r.Leaf, Final: r.Final, Note: r.Note, Next: r.Next, Ask: r.Ask, Wait: r.Wait, Done: r.Done,
		Pipeline: r.Pipeline, Labels: r.Labels, At: int32(r.At), //nolint:gosec // a pipeline index
	}
}

// DecodeClaim reads a claim row back; the row itself gives the session,
// task, checkout and time.
func DecodeClaim(e Event) Claim {
	p, _ := Decode[*ClaimPayload](e)

	return Claim{
		Args: p.GetArgs(),
		Run: state.Run{
			Session:  e.Session,
			Task:     e.Task,
			Root:     e.Checkout,
			Capture:  state.LegacyStatus(p.GetCapture()),
			Plan:     state.LegacyStatus(p.GetPlan()),
			Ship:     state.LegacyStatus(p.GetShip()),
			Leaf:     p.GetLeaf(),
			Final:    p.GetFinal(),
			Pipeline: p.GetPipeline(),
			Labels:   p.GetLabels(),
			At:       int(p.GetAt()),
			Note:     p.GetNote(),
			Next:     p.GetNext(),
			Ask:      p.GetAsk(),
			Wait:     p.GetWait(),
			Done:     p.GetDone(),
			Updated:  e.At,
		},
	}
}
