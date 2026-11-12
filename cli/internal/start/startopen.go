package start

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// StartInput is everything start needs once the target is settled, so
// the flow can be tested without a checkout.
type StartInput struct {
	Main     string                // the main checkout: worktrees are its siblings
	Driver   *trackerdriver.Driver // nil without one
	Task     ref.Ref
	Suffix   string
	Mode     string
	Flow     runplan.ShipFlow                      // the flow ship runs; zero for default
	FlowKept bool                                  // drive records the flow and the pace itself, keeping what the branch has
	Pace     string                                // the --pace typed, recorded on the branch; "" records none
	After    string                                // task id whose branch is the base and whose plan is the contract, or ""
	Base     string                                // branch whose ref is the base, or ""
	Storage  storage.Root                          // the storage root every checkout shares
	Title    func(context.Context) (string, error) // the task's title, for the default branch's slug; nil when nothing answers
	Notes    []string                              // notes settled before the flow, printed beneath the report
	// Profile is what the agent starts on, resolved; profileFlagged says a
	// flag chose it, which wins over the profile the branch recorded
	Profile        agentctl.Profile
	ProfileFlagged bool
	Progress       io.Writer       // where a wait in progress is reported, or nil for nowhere
	Pick           *hostops.Pickup // the PR picked up, or nil for a task started by its id
	Note           string          // the user's words after the id, for the agent before it waits, or ""
	// Children is the task's subtasks, read to refuse a parent; nil skips the
	// check (see ChecksParent)
	Children func(ctx context.Context, task ref.Ref) ([]stack.ChildRow, error)
	// Started moves a task to tracker.status's started word; nil when none
	// is declared, or for a PR picked up, whose task someone else started
	Started func(ctx context.Context, task ref.Ref) error
}

// Start runs the flow: the checkout and its agent, the prompt, the report.
// A workspace Herdr already showed keeps its agent, and nothing is
// prompted twice; one left with no agent gets its agent and its prompt.
func Start(ctx context.Context, w io.Writer, in StartInput) error {
	o, err := OpenTask(ctx, in)
	if err != nil {
		return err
	}

	if o.Fresh {
		text := launch.Prompt(in.Mode, in.Flow, o.Task.Label(), o.Dep, o.Plan, in.Pick, in.Note)

		sent := time.Now()

		name, typed, err := o.Deliver(ctx, in.Task.ID, in.Suffix, text)
		if err != nil {
			return err
		}

		if typed {
			o.Notes = textx.AppendNote(
				o.Notes,
				launch.WatchEnter(ctx, worktree.Agents(o.Path), o.Path, name, text, sent),
			)
		}

		o.Notes = append(o.Notes, MarkStarted(ctx, in.Started, o.Task)...)
	}

	rows := []render.Fact{render.TaskFact(ctx, w, in.Main, o.Task.Label())}
	if in.Pick != nil {
		rows = append(rows, render.Fact{Label: "pr", Value: in.Pick.URL, URL: true})
	}

	rows = append(rows, render.Fact{Label: "branch", Value: o.Branch})
	if o.Dep != nil {
		rows = append(rows, render.Fact{Label: "after", Value: o.Dep.Task})
	}

	if o.Plan != "" {
		rows = append(rows, render.Fact{Label: "plan", Value: o.Plan, Path: true})
	}

	if o.Fresh && !o.Profile.IsDefault() {
		rows = append(rows, render.Fact{Label: "agent", Value: o.Profile.Short()})
	}

	return render.WriteReport(
		w,
		append(rows, render.Fact{Label: "path", Value: o.Path}, render.Fact{Label: "workspace", Value: o.Ws.ID}),
		o.Notes,
	)
}

// StartedMove moves a task to tracker.status's started word, or is nil when
// none is declared.
func StartedMove(t *target.Target) func(context.Context, ref.Ref) error {
	word := t.Effective.TrackerStatus["started"]
	if word == "" {
		return nil
	}

	return func(ctx context.Context, task ref.Ref) error {
		return hostops.TasksFor(t).Update(ctx, io.Discard, task, hostops.Props{Status: word})
	}
}

// MarkStarted moves every member to its started status. The worktree is
// the result, so a failure is a note, never a refusal.
func MarkStarted(ctx context.Context, started func(context.Context, ref.Ref) error, task ref.Ref) []string {
	if started == nil {
		return nil
	}

	var notes []string

	for _, m := range task.MemberRefs() {
		if err := started(ctx, m); err != nil {
			notes = append(notes, fmt.Sprintf("%s was not moved to its started status: %v", m.ID, err))
		}
	}

	return notes
}

// An OpenedTask is what OpenTask leaves behind; fresh says this call is to
// start the agent, which deliver does with its first prompt.
type OpenedTask struct {
	Task   ref.Ref // the task, with the riders its branch ships
	Branch string
	Path   string
	Ws     agentctl.Opened
	Dep    *launch.Dependency
	Plan   string // the task's plan the worktree reads, or "" when none was saved or no agent was started
	Fresh  bool
	Notes  []string
	// Profile is what the agent was started on, when fresh; a report names
	// it when it is not the kind's default
	Profile agentctl.Profile
	// launch is the agent still to start, when fresh: it starts with the
	// first text deliver sends, so that text never goes through its input box
	launch *pendingLaunch
}

// Deliver sends text to the task's agent, starting it when its launch is
// pending: on its command line when the kind takes a first prompt, else
// typed right after. It names the agent that took the text, and typed says
// the text went through the input box, where its Enter can be lost.
func (o *OpenedTask) Deliver(ctx context.Context, id, suffix, text string) (string, bool, error) {
	l := o.launch
	if l == nil {
		name, err := launch.PromptAgentAs(ctx, o.Path, id, suffix, text)

		return name, true, err
	}

	// the prompt goes first: --add-dir takes every argument after it
	first, argv := l.profile.PromptArgs(text)

	args := slices.Concat(first, l.args)
	if err := worktree.Agents(o.Path).StartAgent(ctx, o.Ws, l.agent, l.profile.Kind, args, l.waiting); err != nil {
		return "", false, err
	}

	o.launch = nil

	if argv {
		return l.agent, false, nil
	}

	return l.agent, true, worktree.Agents(o.Path).Prompt(ctx, l.agent, text)
}

// A pendingLaunch is an agent OpenTask decided to start.
type pendingLaunch struct {
	agent   string
	profile agentctl.Profile
	args    []string
	waiting func(waited, limit time.Duration)
}

// OpenTask runs everything start does short of starting and prompting the
// agent, which deliver does with the first text. Every lookup
// that can refuse runs before the first thing is created, the worktree;
// drive adopts main's plan too but prompts the agent itself, skill by skill.
func OpenTask(ctx context.Context, in StartInput) (OpenedTask, error) {
	settled, err := branchFor(ctx, in)
	if err != nil {
		return OpenedTask{}, err
	}

	name, base, dep, existing := settled.name, settled.base, settled.dep, settled.existing

	if in.Pick == nil {
		if in.Task, err = settleBundle(ctx, in.Main, in.Task, existing.Name); err != nil {
			return OpenedTask{}, err
		}
	}

	notes := slices.Clone(in.Notes)

	// the bare-id note explains a name this run gives; a branch that exists
	// keeps the name it has, whatever the title would have said
	switch {
	case existing.Name != "":
		name = existing.Name

		if base.ref != "" {
			notes = append(notes, ignoredBaseNote(base, dep, name))
			base = baseRef{}
		}
	case settled.slugNote != "":
		notes = append(notes, settled.slugNote)
	}

	path, notes, err := openWorktree(ctx, in, name, base, dep, existing, notes)
	if err != nil {
		return OpenedTask{}, err
	}

	if notes, err = recordBranchFacts(ctx, in, name, dep, existing, notes); err != nil {
		return OpenedTask{}, err
	}

	ws, wsNotes, err := worktree.Agents(in.Main).Open(ctx, path, name)
	if err != nil {
		return OpenedTask{}, err
	}

	notes = append(notes, wsNotes...)

	o := OpenedTask{Task: in.Task, Branch: name, Path: path, Ws: ws, Dep: dep}

	agent := agentctl.AgentName(in.Task.ID, in.Suffix, path)

	launched, note, err := needsAgent(ctx, path, ws)
	if err != nil {
		return OpenedTask{}, err
	}

	profile := in.Profile.Over(agentctl.Profile{})

	if launched {
		if profile, notes, err = prepareLaunch(ctx, in, &o, name, path, agent, ws, profile, notes); err != nil {
			return OpenedTask{}, err
		}
	}

	if note != "" {
		notes = append(notes, note)
	}

	if !launched && !profile.IsDefault() {
		notes = append(notes, "agent profile "+profile.Short()+" ignored: no agent was started")
	}

	o.Notes = notes

	return o, nil
}

// openWorktree reuses the branch's worktree or checks the branch out into a
// new one, made from base when the branch is new, and returns its path.
func openWorktree(
	ctx context.Context,
	in StartInput,
	name string,
	base baseRef,
	dep *launch.Dependency,
	existing stack.FoundBranch,
	notes []string,
) (string, []string, error) {
	wts, err := gitx.Worktrees(ctx, in.Main)
	if err != nil {
		return "", nil, err
	}

	if dep != nil {
		notes = append(notes, locatePlan(dep, in.Storage)...)
	}

	path := stack.WorktreeOf(wts, name)

	if in.Pick != nil && !in.Pick.Landed() && !existing.Remote {
		note, err := syncPickup(ctx, in.Main, name, path != "")
		if err != nil {
			return "", nil, err
		}

		notes = textx.AppendNote(notes, note)
	}

	switch {
	case path != "":
		notes = append(notes, "reused the existing worktree")
	case existing.Name != "":
		if existing.Remote {
			if err := gitx.TrackBranch(ctx, in.Main, name, false); err != nil {
				return "", nil, err
			}

			notes = append(notes, "created "+name+" tracking origin/"+name)
		}

		if path, err = worktree.Checkout(ctx, in.Main, name, "", false); err != nil {
			return "", nil, err
		}
	default:
		// a base at the default branch's commit changes nothing, which is
		// otherwise indistinguishable from no base at all
		if n := sameAsDefaultNote(ctx, in.Main, base, dep); n != "" {
			notes = append(notes, n)
		}

		if path, err = worktree.Checkout(ctx, in.Main, name, base.ref, true); err != nil {
			return "", nil, err
		}
	}

	return path, notes, nil
}

// recordBranchFacts writes what the branch carries for later commands: its
// dependency, a picked-up PR's task and a bundle's riders.
func recordBranchFacts(
	ctx context.Context,
	in StartInput,
	name string,
	dep *launch.Dependency,
	existing stack.FoundBranch,
	notes []string,
) ([]string, error) {
	// the dependency is recorded once the branch exists, made or found: a
	// branch started without --after can still be stacked by a second start
	if dep != nil {
		if err := recordDependency(ctx, in.Main, name, dep); err != nil {
			return nil, err
		}
	}

	if in.Pick != nil {
		if err := branchmeta.Set(ctx, in.Main, name, branchmeta.Task, in.Task.ID); err != nil {
			return nil, fmt.Errorf("linking %s to %s: %w", name, in.Task.ID, err)
		}
	}

	if in.Pick != nil && in.Pick.Landed() {
		notes = append(notes, landedNotes(ctx, in, name, existing.Name == "")...)
	}

	if len(in.Task.Riders()) > 0 {
		if err := recordBundle(ctx, in.Main, name, in.Task); err != nil {
			return nil, err
		}
	}

	return notes, nil
}

// prepareLaunch readies the launch a fresh workspace gets, for start to run,
// and records the flow, pace and profile on the branch; it returns the
// profile it settled on.
func prepareLaunch(
	ctx context.Context,
	in StartInput,
	o *OpenedTask,
	name, path, agent string,
	ws agentctl.Opened,
	profile agentctl.Profile,
	notes []string,
) (agentctl.Profile, []string, error) {
	// the agent reads the plan in place: only a prompted agent is told of it
	o.Plan = planIf(in.Storage.Plan(in.Task.ID))

	if err := makeStorageDirs(in.Storage); err != nil {
		return profile, nil, err
	}

	// a rerun keeps the profile the branch records, unless a flag chose one
	if !in.ProfileFlagged {
		recorded, err := runplan.RecordedProfile(ctx, in.Main, name)
		if err != nil {
			return profile, nil, err
		}

		if !recorded.IsDefault() {
			profile = recorded
		}
	}

	o.launch = &pendingLaunch{
		agent:   agent,
		profile: profile,
		args:    AgentArgs(profile, in.Storage, path),
		waiting: shellWait(in.Progress, ws.Slot),
	}
	o.Fresh = true
	o.Profile = profile

	// the flow is the branch's fact from here on: status lists its gates,
	// and a rerun of drive keeps it
	if !in.FlowKept && in.Flow.Recorded() != "" {
		if err := branchmeta.Set(ctx, in.Main, name, branchmeta.Flow, in.Flow.Recorded()); err != nil {
			notes = append(notes, "the flow was not recorded on the branch: "+err.Error())
		}
	}

	if !in.FlowKept && in.Pace != "" {
		if err := branchmeta.Set(ctx, in.Main, name, branchmeta.Pace, in.Pace); err != nil {
			notes = append(notes, "the pace was not recorded on the branch: "+err.Error())
		}
	}

	// the profile is the branch's fact from here on, for a rerun and a
	// reader of the branch (f10 board); a failed write loses nothing
	// the agent needs
	if !profile.IsDefault() {
		if err := branchmeta.Set(ctx, in.Main, name, branchmeta.Agent, profile.String()); err != nil {
			notes = append(notes, "the agent profile was not recorded on the branch: "+err.Error())
		}
	}

	return profile, notes, nil
}

// A settledBranch is what start knows before creating anything.
type settledBranch struct {
	name     string
	slugNote string
	base     baseRef
	dep      *launch.Dependency
	existing stack.FoundBranch
}

// branchFor settles the branch before anything is created. An open PR's
// branch and base are the PR's, so only whether it is local yet is open; a
// landed one gets the task's branch at the commit it landed as.
func branchFor(ctx context.Context, in StartInput) (settledBranch, error) {
	switch {
	case in.Pick != nil && in.Pick.Landed():
		return landedBranchFor(ctx, in)
	case in.Pick != nil:
		name := in.Pick.Branch
		local := stack.RefSHA(ctx, in.Main, "refs/heads/"+name) != ""

		return settledBranch{name: name, existing: stack.FoundBranch{Name: name, Remote: !local}}, nil
	}

	name, slugNote, err := stack.BranchName(ctx, in.Driver, in.Task, in.Suffix, in.Title)
	if err != nil {
		return settledBranch{}, err
	}

	base, dep, err := baseFor(ctx, in)
	if err != nil {
		return settledBranch{}, err
	}

	x, err := stack.LoadBranchIndex(ctx, in.Main)
	if err != nil {
		return settledBranch{}, err
	}

	existing, err := x.Find(ctx, name, in.Task.ID, in.Suffix, stack.PolicyLocalOrOrigin)
	if err != nil {
		return settledBranch{}, err
	}

	return settledBranch{name: name, slugNote: slugNote, base: base, dep: dep, existing: existing}, nil
}

// landedBranchFor names a merged or closed PR's branch as a task start
// would, so worktree, workspace and agent name follow one rule. Only this
// checkout's branches count: origin's copy of the PR's branch is stale since
// it landed, and a branch the task has here is reused as it is. The base
// carries no sha: the landed commit at the default branch's tip is nothing
// to note.
func landedBranchFor(ctx context.Context, in StartInput) (settledBranch, error) {
	name, slugNote, err := stack.BranchName(ctx, in.Driver, in.Task, "", in.Title)
	if err != nil {
		return settledBranch{}, err
	}

	x, err := stack.LoadBranchIndex(ctx, in.Main)
	if err != nil {
		return settledBranch{}, err
	}

	existing, err := x.Find(ctx, name, in.Task.ID, "", stack.PolicyLocal)
	if err != nil {
		return settledBranch{}, err
	}

	return settledBranch{
		name:     name,
		slugNote: slugNote,
		base:     baseRef{ref: in.Pick.SHA, flag: in.Pick.URL + "'s landed commit"},
		existing: existing,
	}, nil
}

// landedNotes hands a branch this start made back from the watcher, as
// `f10 watch off` does: the PR landed before the branch existed, and a pass
// would report that as news every time. A branch the task already had keeps
// its watch, since its own work may still be live. A failed write is a note,
// since the worktree is the result.
func landedNotes(ctx context.Context, in StartInput, name string, created bool) []string {
	if !created {
		return []string{fmt.Sprintf("%s is %s: %s is reused at its own tip and stays watched",
			in.Pick.URL, in.Pick.State, name)}
	}

	var notes []string

	if err := branchmeta.Set(ctx, in.Main, name, branchmeta.Watch, "false"); err != nil {
		notes = append(notes, "the watcher was not told to leave "+name+" alone: "+err.Error())
	}

	return append(notes, fmt.Sprintf("%s is %s: %s is local only at %s, and the watcher leaves it alone "+
		"(f10 watch on %s undoes that)", in.Pick.URL, in.Pick.State, name, gitx.ShortSHA(in.Pick.SHA), in.Task.ID))
}

// shellWait reports a wait for the pane's shell to finish starting, so a
// slow rc file reads as progress rather than a hang. A nil w reports nothing.
func shellWait(w io.Writer, pane string) func(waited, limit time.Duration) {
	if w == nil {
		return nil
	}

	return func(waited, limit time.Duration) {
		fmt.Fprintf(w, "waiting for the shell in pane %s to finish starting: %.1fs of %s\n",
			pane, waited.Seconds(), limit)
	}
}

// needsAgent decides whether the workspace gets an agent in its root pane:
// one Herdr already showed keeps its agent whatever its name, and with none
// (a start that failed on a busy pane) only an idle shell gets one.
func needsAgent(ctx context.Context, dir string, ws agentctl.Opened) (bool, string, error) {
	if !ws.AlreadyOpen {
		return true, "", nil
	}

	occ, err := worktree.Agents(dir).Occupancy(ctx, ws)
	if err != nil {
		return false, "", err
	}

	switch occ {
	case agentctl.AgentPresent:
		return false, "workspace already open: its agent left as it was, nothing prompted", nil
	case agentctl.Busy:
		return false, fmt.Sprintf("workspace already open with no agent, and pane %s runs something "+
			"other than an idle shell: left alone, nothing prompted", ws.Slot), nil
	case agentctl.Free:
	}

	return true, "workspace already open with no agent: started one in its idle shell", nil
}
