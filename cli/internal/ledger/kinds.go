package ledger

import (
	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// Each kind's payload is its f10.v1 message (proto/f10/v1/events.proto),
// stored as protojson. Within a version a field is only added, which buf
// breaking enforces; a real break is a new message with an upcaster. The
// aliases name each kind's current message for readers.
type (
	AskRaisedPayload         = agentsv1.AskRaised
	AskAnsweredPayload       = agentsv1.AskAnswered
	RunStartedPayload        = f10v1.RunStarted
	StepEnteredPayload       = f10v1.StepEntered
	TaskFinishedPayload      = f10v1.TaskFinished
	SessionStoppedPayload    = f10v1.SessionStopped
	TurnUsedPayload          = f10v1.TurnUsed
	TaskCapturedPayload      = f10v1.TaskCaptured
	PROpenedPayload          = f10v1.PrOpened
	CommittedPayload         = f10v1.Committed
	PushedPayload            = f10v1.Pushed
	WaitStartedPayload       = f10v1.WaitStarted
	WaitEndedPayload         = f10v1.WaitEnded
	PlanSavedPayload         = f10v1.PlanSaved
	PlanSupersededPayload    = f10v1.PlanSuperseded
	SpikeSavedPayload        = f10v1.SpikeSaved
	ReviewRoundOpenedPayload = f10v1.ReviewRoundOpened
	FindingVerdictPayload    = f10v1.FindingVerdict
	JudgeVerdictPayload      = f10v1.JudgeVerdict
	TaskHaltedPayload        = f10v1.TaskHalted
	TaskSizedPayload         = f10v1.TaskSized
	ClaimPayload             = f10v1.Claim
	RegressionFoundPayload   = f10v1.RegressionFound
	RegressionClaimedPayload = f10v1.RegressionClaimed
	WatchReportedPayload     = f10v1.WatchReported

	// AgentProfile and the four below are the messages payloads nest.
	AgentProfile = f10v1.AgentProfile
	Usage        = f10v1.Usage
	Tokens       = f10v1.Tokens
	Ask          = agentsv1.Ask
	AskQuestion  = agentsv1.Question
)

func init() {
	register[*RunStartedPayload](kinds, RunStarted)
	register[*StepEnteredPayload](kinds, StepEntered)
	register[*TaskFinishedPayload](kinds, TaskFinished)
	register[*SessionStoppedPayload](kinds, SessionStopped)
	register[*TaskCapturedPayload](kinds, TaskCaptured)
	register[*PROpenedPayload](kinds, PROpened)
	register[*CommittedPayload](kinds, Committed)
	register[*PushedPayload](kinds, Pushed)
	register[*WaitStartedPayload](kinds, WaitStarted)
	register[*WaitEndedPayload](kinds, WaitEnded)
	register[*PlanSavedPayload](kinds, PlanSaved)
	register[*PlanSupersededPayload](kinds, PlanSuperseded)
	register[*SpikeSavedPayload](kinds, SpikeSaved)
	register[*ReviewRoundOpenedPayload](kinds, ReviewRoundOpened)
	register[*FindingVerdictPayload](kinds, FindingVerdict)
	register[*JudgeVerdictPayload](kinds, JudgeVerdict)
	register[*TaskHaltedPayload](kinds, TaskHalted)
	register[*TaskSizedPayload](kinds, TaskSized)
	register[*RegressionFoundPayload](kinds, RegressionFound)
	register[*RegressionClaimedPayload](kinds, RegressionClaimed)
	register[*ClaimPayload](
		kinds,
		StateSet,
		StateTask,
		StateNote,
		StateAsk,
		StateWait,
		StateStop,
		StateDone,
		StateFinal,
		StatePipeline,
	)
	register[*TurnUsedPayload](kinds, TurnUsed)
	register[*AskRaisedPayload](kinds, AskRaised)
	register[*AskAnsweredPayload](kinds, AskAnswered)
	register[*WatchReportedPayload](kinds, WatchReported)

	// v2 of each added the run context's keys
	additive(kinds, RunStarted, 2)
	additive(kinds, SessionStopped, 2)
}
