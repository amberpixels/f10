package ledger

// Each kind's payload, by version. A key's json tag is its stored name, so
// renaming one is a new version with an upcaster, never an edit here. The
// unversioned aliases name each kind's current version for readers.

// RunStartedV1 is an f10 skill starting, with its arguments as typed.
type RunStartedV1 struct {
	Skill string `json:"skill"`
	Args  string `json:"args,omitempty"`
}

// RunStartedV2 adds what ran the skill.
type RunStartedV2 struct {
	RunContext

	Skill string `json:"skill"`
	Args  string `json:"args,omitempty"`
}

// RunContext is what a run ran on, each key absent when the hook could not
// tell. Its keys are part of every version that embeds it, so changing one
// is a new version of each.
type RunContext struct {
	F10Version string       `json:"f10_version,omitempty"`
	Model      string       `json:"model,omitempty"`
	Agent      string       `json:"agent,omitempty"`
	Profile    AgentProfile `json:"profile,omitzero"`
}

// AgentProfile is the profile f10 started the agent with, as the branch
// records it; an empty segment was left to the agent's default.
type AgentProfile struct {
	Kind   string `json:"kind,omitempty"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// StepEnteredV1 is a step load: the steps it named.
type StepEnteredV1 struct {
	Steps []string `json:"steps"`
}

// TaskFinishedV1 is an `f10 finish` call: its arguments, and who ran it when
// it ran in-process ("drive", "watch").
type TaskFinishedV1 struct {
	Args []string `json:"args"`
	By   string   `json:"by,omitempty"`
}

// SessionStoppedV1 is a turn ending, at the run's phase, status and leaf.
type SessionStoppedV1 struct {
	Task     string `json:"task,omitempty"`
	Phase    string `json:"phase"`
	Status   string `json:"status"`
	Leaf     string `json:"leaf,omitempty"`
	Backstop bool   `json:"backstop,omitempty"`
}

// SessionStoppedV2 adds the run context, set only when it changed since
// the session's last row that carried one.
type SessionStoppedV2 struct {
	RunContext

	Task     string `json:"task,omitempty"`
	Phase    string `json:"phase"`
	Status   string `json:"status"`
	Leaf     string `json:"leaf,omitempty"`
	Backstop bool   `json:"backstop,omitempty"`
}

// TurnUsedV1 is the tokens a turn spent: the transcript's usage since the
// session's previous turn_used row, so a sum over rows is a cost.
type TurnUsedV1 struct {
	Usage Usage `json:"usage"`
}

// Usage is one turn's tokens: the session's own messages, by model when the
// turn used more than one, its subagents' apart, and the last entry counted.
type Usage struct {
	Tokens

	ByModel   map[string]Tokens `json:"by_model,omitempty"`
	Subagents Tokens            `json:"subagents,omitzero"`
	Through   string            `json:"through,omitempty"`
}

// Tokens is the four token counts of an API usage.
type Tokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheWrite int `json:"cache_write"`
	CacheRead  int `json:"cache_read"`
}

// TaskCapturedV1 is a tracker issue created, by the url or id it printed.
type TaskCapturedV1 struct {
	Host string `json:"host"`
	URL  string `json:"url,omitempty"`
	ID   string `json:"id,omitempty"`
}

// PROpenedV1 is a PR or MR opened.
type PROpenedV1 struct {
	Host string `json:"host"`
	URL  string `json:"url"`
}

// CommittedV1 is a commit made.
type CommittedV1 struct {
	Branch  string `json:"branch"`
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// PushedV1 is a push that moved refs.
type PushedV1 struct {
	Remote string   `json:"remote,omitempty"`
	Refs   []string `json:"refs"`
}

// WaitStartedV1 is a run starting to wait on the user.
type WaitStartedV1 struct {
	Reason string `json:"reason"`
	Tool   string `json:"tool,omitempty"`
}

// WaitEndedV1 is that wait over, By the hook event that ended it.
type WaitEndedV1 struct {
	Reason string `json:"reason"`
	Tool   string `json:"tool,omitempty"`
	By     string `json:"by"`
}

// PlanSavedV1 is a plan file written.
type PlanSavedV1 struct {
	Path string `json:"path"`
	Task string `json:"task"`
}

// PlanSupersededV1 is a plan moved into the archive.
type PlanSupersededV1 struct {
	From string `json:"from"`
	To   string `json:"to"`
	Task string `json:"task"`
}

// SpikeSavedV1 is a spike report written.
type SpikeSavedV1 struct {
	Path string `json:"path"`
	Task string `json:"task"`
}

// ReviewRoundOpenedV1 is a findings file's round, once per reviewed sha;
// ParseError alone with Path and Round when the file did not parse.
type ReviewRoundOpenedV1 struct {
	Path       string         `json:"path"`
	Round      string         `json:"round"`
	Reviewed   string         `json:"reviewed,omitempty"`
	Reviewer   string         `json:"reviewer,omitempty"`
	Verdict    string         `json:"verdict,omitempty"`
	Categories map[string]int `json:"categories,omitempty"`
	ParseError string         `json:"parse_error,omitempty"`
}

// FindingVerdictV1 is a finding whose verdict or status changed.
type FindingVerdictV1 struct {
	Round    string `json:"round"`
	Finding  string `json:"finding"`
	Title    string `json:"title"`
	Verdict  string `json:"verdict,omitempty"`
	Category string `json:"category,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Status   string `json:"status,omitempty"`
}

// JudgeVerdictV1 is a verdict file's verdict and decision.
type JudgeVerdictV1 struct {
	Path       string `json:"path"`
	Key        string `json:"key"`
	N          string `json:"n"`
	Subject    string `json:"subject,omitempty"`
	Verdict    string `json:"verdict,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Run        string `json:"run,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Blind      bool   `json:"blind,omitempty"`
	Idea       bool   `json:"idea,omitempty"`
	ParseError string `json:"parse_error,omitempty"`
}

// TaskHaltedV1 is a task a drive stopped, with the attempts it counted.
type TaskHaltedV1 struct {
	Skill    string   `json:"skill"`
	Reason   string   `json:"reason"`
	Attempts []string `json:"attempts,omitempty"`
}

// TaskSizedV1 is a branch's diffstat against its base, before it lands.
type TaskSizedV1 struct {
	Lines   int    `json:"lines"`
	MdLines int    `json:"md_lines"`
	Files   int    `json:"files"`
	Base    string `json:"base"`
}

// ClaimV1 is a recorded `f10 state` call, every state_* kind: its arguments
// and the run it left. Session, task, checkout and time are the row's.
type ClaimV1 struct {
	Args     []string `json:"args"`
	Capture  string   `json:"capture"`
	Plan     string   `json:"plan"`
	Ship     string   `json:"ship"`
	Leaf     string   `json:"leaf,omitempty"`
	Final    string   `json:"final,omitempty"`
	Note     string   `json:"note,omitempty"`
	Next     string   `json:"next,omitempty"`
	Ask      string   `json:"ask,omitempty"`
	Wait     string   `json:"wait,omitempty"`
	Done     string   `json:"done,omitempty"`
	Pipeline []string `json:"pipeline,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	At       int      `json:"at,omitempty"`
}

// RegressionFoundV1 is a bug task's root cause traced to the commit and the
// task that introduced it, with how review let it through. Lines are
// `path:start-end`; Round and Finding name the skipped finding.
type RegressionFoundV1 struct {
	OriginTask string   `json:"origin_task"`
	Origin     string   `json:"origin"`
	Commit     string   `json:"commit,omitempty"`
	Lines      []string `json:"lines,omitempty"`
	FoundBy    string   `json:"found_by"`
	Confidence string   `json:"confidence"`
	Class      string   `json:"class,omitempty"`
	Round      string   `json:"round,omitempty"`
	Finding    string   `json:"finding,omitempty"`
	Repro      string   `json:"repro"`
	Note       string   `json:"note,omitempty"`
}

// RegressionClaimedV1 is a task whose own text names the task it is a
// regression of: the user's word, kept apart from what debug finds.
type RegressionClaimedV1 struct {
	OriginTask string `json:"origin_task"`
	Text       string `json:"text,omitempty"`
}

type (
	RunStartedPayload        = RunStartedV2
	StepEnteredPayload       = StepEnteredV1
	TaskFinishedPayload      = TaskFinishedV1
	SessionStoppedPayload    = SessionStoppedV2
	TurnUsedPayload          = TurnUsedV1
	TaskCapturedPayload      = TaskCapturedV1
	PROpenedPayload          = PROpenedV1
	CommittedPayload         = CommittedV1
	PushedPayload            = PushedV1
	WaitStartedPayload       = WaitStartedV1
	WaitEndedPayload         = WaitEndedV1
	PlanSavedPayload         = PlanSavedV1
	PlanSupersededPayload    = PlanSupersededV1
	SpikeSavedPayload        = SpikeSavedV1
	ReviewRoundOpenedPayload = ReviewRoundOpenedV1
	FindingVerdictPayload    = FindingVerdictV1
	JudgeVerdictPayload      = JudgeVerdictV1
	TaskHaltedPayload        = TaskHaltedV1
	TaskSizedPayload         = TaskSizedV1
	ClaimPayload             = ClaimV1
	RegressionFoundPayload   = RegressionFoundV1
	RegressionClaimedPayload = RegressionClaimedV1
)

func init() {
	register[RunStartedV1](kinds, RunStarted)
	register[StepEnteredV1](kinds, StepEntered)
	register[TaskFinishedV1](kinds, TaskFinished)
	register[SessionStoppedV1](kinds, SessionStopped)
	register[TaskCapturedV1](kinds, TaskCaptured)
	register[PROpenedV1](kinds, PROpened)
	register[CommittedV1](kinds, Committed)
	register[PushedV1](kinds, Pushed)
	register[WaitStartedV1](kinds, WaitStarted)
	register[WaitEndedV1](kinds, WaitEnded)
	register[PlanSavedV1](kinds, PlanSaved)
	register[PlanSupersededV1](kinds, PlanSuperseded)
	register[SpikeSavedV1](kinds, SpikeSaved)
	register[ReviewRoundOpenedV1](kinds, ReviewRoundOpened)
	register[FindingVerdictV1](kinds, FindingVerdict)
	register[JudgeVerdictV1](kinds, JudgeVerdict)
	register[TaskHaltedV1](kinds, TaskHalted)
	register[TaskSizedV1](kinds, TaskSized)
	register[RegressionFoundV1](kinds, RegressionFound)
	register[RegressionClaimedV1](kinds, RegressionClaimed)
	register[ClaimV1](
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
	register[TurnUsedV1](kinds, TurnUsed)

	upcast(kinds, RunStarted, func(o RunStartedV1) RunStartedV2 {
		return RunStartedV2{Skill: o.Skill, Args: o.Args}
	})
	upcast(kinds, SessionStopped, func(o SessionStoppedV1) SessionStoppedV2 {
		return SessionStoppedV2{Task: o.Task, Phase: o.Phase, Status: o.Status, Leaf: o.Leaf, Backstop: o.Backstop}
	})
}
