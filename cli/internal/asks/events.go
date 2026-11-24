package asks

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

// recordRaised and recordAnswered put the inbox's changes in the ledger,
// which the API streams as events. A failed record never fails the ask.
func recordRaised(a Ask) {
	record(a, ledger.AskRaised, &ledger.AskRaisedPayload{Ask: Message(a)})
}

func recordAnswered(a Ask) {
	record(a, ledger.AskAnswered, &ledger.AskAnsweredPayload{
		Id:         a.Key(),
		Answer:     a.Answer,
		AnsweredBy: Source(a.By),
		Delivered:  !a.DeliveredAt.IsZero(),
		Labels:     Labels(a),
	})
}

// Message is an inbox ask as agents.v1 carries it, for the API and the
// ledger alike: what f10 adds (the task, its project and drive) goes in the
// labels, so the core grows no task field.
//
//nolint:lostfield // f10's fields go in Labels; TakenAt is drive bookkeeping, and the inbox records no workspace
func Message(a Ask) *agentsv1.Ask {
	out := &agentsv1.Ask{
		Id:         a.Key(),
		Agent:      agentctl.AgentName(a.Task, a.Suffix, a.Worktree),
		Text:       a.Text,
		AskedAt:    timestamppb.New(a.AskedAt),
		Answer:     a.Answer,
		AnsweredBy: Source(a.By),
		Labels:     Labels(a),
	}

	for _, q := range a.Questions {
		out.Questions = append(out.Questions, &agentsv1.Question{
			N:       int32(q.N), //nolint:gosec // a question number
			Text:    q.Text,
			Options: q.Options,
		})
	}

	if !a.AnsweredAt.IsZero() {
		out.AnsweredAt = timestamppb.New(a.AnsweredAt)
	}

	if !a.DeliveredAt.IsZero() {
		out.DeliveredAt = timestamppb.New(a.DeliveredAt)
	}

	return out
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

// Source is the way in an answer came by, as agents.v1 names it.
func Source(by string) agentsv1.AnswerSource {
	switch by {
	case ByPane:
		return agentsv1.AnswerSource_ANSWER_SOURCE_PANE
	case ByAsks:
		return agentsv1.AnswerSource_ANSWER_SOURCE_ASKS
	case BySkill:
		return agentsv1.AnswerSource_ANSWER_SOURCE_SKILL
	case ByDrive:
		return agentsv1.AnswerSource_ANSWER_SOURCE_DRIVE
	case ByCancelled:
		return agentsv1.AnswerSource_ANSWER_SOURCE_CANCELLED
	case ByAPI:
		return agentsv1.AnswerSource_ANSWER_SOURCE_API
	}

	return agentsv1.AnswerSource_ANSWER_SOURCE_UNSPECIFIED
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
		out["batch"] = strconv.Itoa(a.Drive)
		out["drive"] = out["batch"] // the old label, for clients built against it
	}

	return out
}
