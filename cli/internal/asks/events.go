package asks

import (
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

// recordRaised and recordAnswered put the inbox's changes in the ledger,
// which the API streams as events. A failed record never fails the ask.
func recordRaised(a Ask) {
	qs := make([]ledger.AskQuestionV1, 0, len(a.Questions))
	for _, q := range a.Questions {
		qs = append(qs, ledger.AskQuestionV1{N: q.N, Text: q.Text, Options: q.Options})
	}

	record(a, ledger.AskRaised, ledger.AskRaisedPayload{Ask: ledger.AskV1{
		ID:          a.Key(),
		Agent:       agentctl.AgentName(a.Task, a.Suffix, a.Worktree),
		Text:        a.Text,
		Questions:   qs,
		AskedAt:     a.AskedAt,
		Answer:      a.Answer,
		AnsweredBy:  source(a.By),
		AnsweredAt:  a.AnsweredAt,
		DeliveredAt: a.DeliveredAt,
		Labels:      Labels(a),
	}})
}

func recordAnswered(a Ask) {
	record(a, ledger.AskAnswered, ledger.AskAnsweredPayload{
		ID:         a.Key(),
		Answer:     a.Answer,
		AnsweredBy: source(a.By),
		Delivered:  !a.DeliveredAt.IsZero(),
	})
}

func record(a Ask, kind string, payload any) {
	e, err := ledger.NewEvent(kind, payload)
	if err != nil {
		return
	}

	// the project stays unset: naming it asks git, and an ask is raised from
	// hooks that must stay fast; the labels carry the project's name
	e.Task, e.Source = a.Task, ledger.Observed

	ledger.Record(e)
}

// source is a way in as the agents.v1 AnswerSource enum names it.
func source(by string) string {
	if by == "" {
		return ""
	}

	return "ANSWER_SOURCE_" + strings.ToUpper(by)
}

// Labels are what f10 adds to an ask, kept out of the generic ask's fields.
func Labels(a Ask) map[string]string {
	out := map[string]string{}

	for k, v := range map[string]string{
		"task": a.Task, "url": a.URL, "project": a.Project, "root": a.Root, "main": a.Main,
		"worktree": a.Worktree, "skill": a.Skill,
	} {
		if v != "" {
			out[k] = v
		}
	}

	if a.Drive > 0 {
		out["drive"] = strconv.Itoa(a.Drive)
	}

	return out
}
