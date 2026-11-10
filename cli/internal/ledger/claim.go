package ledger

import "github.com/amberpixels/f10/cli/internal/state"

// Claim is a recorded `f10 state` call: its arguments as the model typed
// them, and the run it left.
type Claim struct {
	Args []string
	Run  state.Run
}

// EncodeClaim is the payload of a claim row. Stored rows are read by these
// keys, so a rename orphans every claim already in the database.
func EncodeClaim(r *state.Run, args []string) Payload {
	p := Payload{
		"args":    args,
		"capture": r.Capture,
		"plan":    r.Plan,
		"ship":    r.Ship,
	}

	for key, v := range map[string]string{
		"leaf": r.Leaf, "final": r.Final, "note": r.Note, "next": r.Next, "ask": r.Ask, "wait": r.Wait, "done": r.Done,
	} {
		if v != "" {
			p[key] = v
		}
	}

	if len(r.Pipeline) > 0 {
		p["pipeline"] = r.Pipeline
	}

	// labels and the place show a finished task's steps by their names
	if r.Labels != nil {
		p["labels"] = r.Labels
	}

	if r.At > 0 {
		p["at"] = r.At
	}

	return p
}

// DecodeClaim reads a claim row back; the row itself gives the session,
// task, checkout and time.
func DecodeClaim(e Event) Claim {
	p := e.Payload.Val
	at, _ := p.Int("at")

	return Claim{
		Args: p.Strings("args"),
		Run: state.Run{
			Session:  e.Session,
			Task:     e.Task,
			Root:     e.Checkout,
			Capture:  state.LegacyStatus(p.String("capture")),
			Plan:     state.LegacyStatus(p.String("plan")),
			Ship:     state.LegacyStatus(p.String("ship")),
			Leaf:     p.String("leaf"),
			Final:    p.String("final"),
			Pipeline: p.Strings("pipeline"),
			Labels:   p.Strings("labels"),
			At:       at,
			Note:     p.String("note"),
			Next:     p.String("next"),
			Ask:      p.String("ask"),
			Wait:     p.String("wait"),
			Done:     p.String("done"),
			Updated:  e.At,
		},
	}
}
