package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// startInput is everything start needs once the target is settled, so
// the flow can be tested without a checkout.
type startInput struct {
	main     string         // the main checkout: worktrees are its siblings
	driver   *driver.Driver // nil without one
	task     ref.Ref
	suffix   string
	mode     string
	flow     shipFlow                              // the flow ship runs; zero for default
	flowKept bool                                  // drive records the flow and the pace itself, keeping what the branch has
	pace     string                                // the --pace typed, recorded on the branch; "" records none
	after    string                                // task id whose branch is the base and whose plan is the contract, or ""
	base     string                                // branch whose ref is the base, or ""
	storage  storage.Root                          // the storage root every checkout shares
	title    func(context.Context) (string, error) // the task's title, for the default branch's slug; nil when nothing answers
	notes    []string                              // notes settled before the flow, printed beneath the report
	// profile is what the agent starts on, resolved; profileFlagged says a
	// flag chose it, which wins over the profile the branch recorded
	profile        agentctl.Profile
	profileFlagged bool
	progress       io.Writer // where a wait in progress is reported, or nil for nowhere
	pick           *pickup   // the PR picked up, or nil for a task started by its id
	note           string    // the user's words after the id, for the agent before it waits, or ""
	// children is the task's subtasks, read to refuse a parent; nil skips the
	// check (see checksParent)
	children func(ctx context.Context, task ref.Ref) ([]childRow, error)
	// started moves a task to tracker.status's started word; nil when none
	// is declared, or for a PR picked up, whose task someone else started
	started func(ctx context.Context, task ref.Ref) error
}

// titleLookup asks the host for the task's title, needed only when no driver
// names the branch. A checkout with no host CLI answers with the reason, so
// the note beneath the report can quote it.
func titleLookup(t *target, number string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		return t.tasks().Title(ctx, ref.Ref{Number: number})
	}
}

// start runs the flow: the checkout and its agent, the prompt, the report.
// A workspace Herdr already showed keeps its agent, and nothing is
// prompted twice; one left with no agent gets its agent and its prompt.
func start(ctx context.Context, w io.Writer, in startInput) error {
	o, err := openTask(ctx, in)
	if err != nil {
		return err
	}

	if o.fresh {
		text := prompt(in.mode, in.flow, o.task.Label(), o.dep, o.plan, in.pick, in.note)

		sent := time.Now()

		name, typed, err := o.deliver(ctx, in.task.ID, in.suffix, text)
		if err != nil {
			return err
		}

		if typed {
			o.notes = appendNote(o.notes, watchEnter(ctx, agents(o.path), o.path, name, text, sent))
		}

		o.notes = append(o.notes, markStarted(ctx, in.started, o.task)...)
	}

	rows := []fact{taskFact(ctx, w, in.main, o.task.Label())}
	if in.pick != nil {
		rows = append(rows, fact{label: "pr", value: in.pick.url, url: true})
	}

	rows = append(rows, fact{label: "branch", value: o.branch})
	if o.dep != nil {
		rows = append(rows, fact{label: "after", value: o.dep.task})
	}

	if o.plan != "" {
		rows = append(rows, fact{label: "plan", value: o.plan, path: true})
	}

	if o.fresh && !o.profile.IsDefault() {
		rows = append(rows, fact{label: "agent", value: o.profile.Short()})
	}

	return writeReport(
		w,
		append(rows, fact{label: "path", value: o.path}, fact{label: "workspace", value: o.ws.ID}),
		o.notes,
	)
}

// startedMove moves a task to tracker.status's started word, or is nil when
// none is declared.
func startedMove(t *target) func(context.Context, ref.Ref) error {
	word := t.eff.TrackerStatus["started"]
	if word == "" {
		return nil
	}

	return func(ctx context.Context, task ref.Ref) error {
		return t.tasks().Update(ctx, io.Discard, task, props{status: word})
	}
}

// markStarted moves every member to its started status. The worktree is
// the result, so a failure is a note, never a refusal.
func markStarted(ctx context.Context, started func(context.Context, ref.Ref) error, task ref.Ref) []string {
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

// An openedTask is what openTask leaves behind; fresh says this call is to
// start the agent, which deliver does with its first prompt.
type openedTask struct {
	task   ref.Ref // the task, with the riders its branch ships
	branch string
	path   string
	ws     agentctl.Opened
	dep    *dependency
	plan   string // the task's plan the worktree reads, or "" when none was saved or no agent was started
	fresh  bool
	notes  []string
	// profile is what the agent was started on, when fresh; a report names
	// it when it is not the kind's default
	profile agentctl.Profile
	// launch is the agent still to start, when fresh: it starts with the
	// first text deliver sends, so that text never goes through its input box
	launch *pendingLaunch
}

// A pendingLaunch is an agent openTask decided to start.
type pendingLaunch struct {
	agent   string
	profile agentctl.Profile
	args    []string
	waiting func(waited, limit time.Duration)
}

// deliver sends text to the task's agent, starting it when its launch is
// pending: on its command line when the kind takes a first prompt, else
// typed right after. It names the agent that took the text, and typed says
// the text went through the input box, where its Enter can be lost.
func (o *openedTask) deliver(ctx context.Context, id, suffix, text string) (string, bool, error) {
	l := o.launch
	if l == nil {
		name, err := promptAgentAs(ctx, o.path, id, suffix, text)

		return name, true, err
	}

	// the prompt goes first: --add-dir takes every argument after it
	first, argv := l.profile.PromptArgs(text)

	args := slices.Concat(first, l.args)
	if err := agents(o.path).StartAgent(ctx, o.ws, l.agent, l.profile.Kind, args, l.waiting); err != nil {
		return "", false, err
	}

	o.launch = nil

	if argv {
		return l.agent, false, nil
	}

	return l.agent, true, agents(o.path).Prompt(ctx, l.agent, text)
}

// openTask runs everything start does short of starting and prompting the
// agent, which deliver does with the first text. Every lookup
// that can refuse runs before the first thing is created, the worktree;
// drive adopts main's plan too but prompts the agent itself, skill by skill.
func openTask(ctx context.Context, in startInput) (openedTask, error) {
	settled, err := branchFor(ctx, in)
	if err != nil {
		return openedTask{}, err
	}

	name, base, dep, existing := settled.name, settled.base, settled.dep, settled.existing

	if in.pick == nil {
		if in.task, err = settleBundle(ctx, in.main, in.task, existing.name); err != nil {
			return openedTask{}, err
		}
	}

	notes := slices.Clone(in.notes)

	// the bare-id note explains a name this run gives; a branch that exists
	// keeps the name it has, whatever the title would have said
	switch {
	case existing.name != "":
		name = existing.name

		if base.ref != "" {
			notes = append(notes, ignoredBaseNote(base, dep, name))
			base = baseRef{}
		}
	case settled.slugNote != "":
		notes = append(notes, settled.slugNote)
	}

	path, notes, err := openWorktree(ctx, in, name, base, dep, existing, notes)
	if err != nil {
		return openedTask{}, err
	}

	if notes, err = recordBranchFacts(ctx, in, name, dep, existing, notes); err != nil {
		return openedTask{}, err
	}

	ws, wsNotes, err := agents(in.main).Open(ctx, path, name)
	if err != nil {
		return openedTask{}, err
	}

	notes = append(notes, wsNotes...)

	o := openedTask{task: in.task, branch: name, path: path, ws: ws, dep: dep}

	agent := agentctl.AgentName(in.task.ID, in.suffix, path)

	launch, note, err := needsAgent(ctx, path, ws)
	if err != nil {
		return openedTask{}, err
	}

	profile := in.profile.Over(agentctl.Profile{})

	if launch {
		if profile, notes, err = prepareLaunch(ctx, in, &o, name, path, agent, ws, profile, notes); err != nil {
			return openedTask{}, err
		}
	}

	if note != "" {
		notes = append(notes, note)
	}

	if !launch && !profile.IsDefault() {
		notes = append(notes, "agent profile "+profile.Short()+" ignored: no agent was started")
	}

	o.notes = notes

	return o, nil
}

// openWorktree reuses the branch's worktree or checks the branch out into a
// new one, made from base when the branch is new, and returns its path.
func openWorktree(
	ctx context.Context,
	in startInput,
	name string,
	base baseRef,
	dep *dependency,
	existing foundBranch,
	notes []string,
) (string, []string, error) {
	wts, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return "", nil, err
	}

	if dep != nil {
		notes = append(notes, locatePlan(dep, in.storage)...)
	}

	path := worktreeOf(wts, name)

	if in.pick != nil && !in.pick.landed() && !existing.remote {
		note, err := syncPickup(ctx, in.main, name, path != "")
		if err != nil {
			return "", nil, err
		}

		notes = appendNote(notes, note)
	}

	switch {
	case path != "":
		notes = append(notes, "reused the existing worktree")
	case existing.name != "":
		if existing.remote {
			if err := gitx.TrackBranch(ctx, in.main, name, false); err != nil {
				return "", nil, err
			}

			notes = append(notes, "created "+name+" tracking origin/"+name)
		}

		if path, err = checkout(ctx, in.main, name, "", false); err != nil {
			return "", nil, err
		}
	default:
		// a base at the default branch's commit changes nothing, which is
		// otherwise indistinguishable from no base at all
		if n := sameAsDefaultNote(ctx, in.main, base, dep); n != "" {
			notes = append(notes, n)
		}

		if path, err = checkout(ctx, in.main, name, base.ref, true); err != nil {
			return "", nil, err
		}
	}

	return path, notes, nil
}

// recordBranchFacts writes what the branch carries for later commands: its
// dependency, a picked-up PR's task and a bundle's riders.
func recordBranchFacts(
	ctx context.Context,
	in startInput,
	name string,
	dep *dependency,
	existing foundBranch,
	notes []string,
) ([]string, error) {
	// the dependency is recorded once the branch exists, made or found: a
	// branch started without --after can still be stacked by a second start
	if dep != nil {
		if err := recordDependency(ctx, in.main, name, dep); err != nil {
			return nil, err
		}
	}

	if in.pick != nil {
		if err := branchmeta.Set(ctx, in.main, name, branchmeta.Task, in.task.ID); err != nil {
			return nil, fmt.Errorf("linking %s to %s: %w", name, in.task.ID, err)
		}
	}

	if in.pick != nil && in.pick.landed() {
		notes = append(notes, landedNotes(ctx, in, name, existing.name == "")...)
	}

	if len(in.task.Riders()) > 0 {
		if err := recordBundle(ctx, in.main, name, in.task); err != nil {
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
	in startInput,
	o *openedTask,
	name, path, agent string,
	ws agentctl.Opened,
	profile agentctl.Profile,
	notes []string,
) (agentctl.Profile, []string, error) {
	// the agent reads the plan in place: only a prompted agent is told of it
	o.plan = planIf(in.storage.Plan(in.task.ID))

	if err := makeStorageDirs(in.storage); err != nil {
		return profile, nil, err
	}

	// a rerun keeps the profile the branch records, unless a flag chose one
	if !in.profileFlagged {
		recorded, err := recordedProfile(ctx, in.main, name)
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
		args:    agentArgs(profile, in.storage, path),
		waiting: shellWait(in.progress, ws.Slot),
	}
	o.fresh = true
	o.profile = profile

	// the flow is the branch's fact from here on: status lists its gates,
	// and a rerun of drive keeps it
	if !in.flowKept && in.flow.recorded() != "" {
		if err := branchmeta.Set(ctx, in.main, name, branchmeta.Flow, in.flow.recorded()); err != nil {
			notes = append(notes, "the flow was not recorded on the branch: "+err.Error())
		}
	}

	if !in.flowKept && in.pace != "" {
		if err := branchmeta.Set(ctx, in.main, name, branchmeta.Pace, in.pace); err != nil {
			notes = append(notes, "the pace was not recorded on the branch: "+err.Error())
		}
	}

	// the profile is the branch's fact from here on, for a rerun and a
	// reader of the branch (f10 board); a failed write loses nothing
	// the agent needs
	if !profile.IsDefault() {
		if err := branchmeta.Set(ctx, in.main, name, branchmeta.Agent, profile.String()); err != nil {
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
	dep      *dependency
	existing foundBranch
}

// branchFor settles the branch before anything is created. An open PR's
// branch and base are the PR's, so only whether it is local yet is open; a
// landed one gets the task's branch at the commit it landed as.
func branchFor(ctx context.Context, in startInput) (settledBranch, error) {
	switch {
	case in.pick != nil && in.pick.landed():
		return landedBranchFor(ctx, in)
	case in.pick != nil:
		name := in.pick.branch
		local := refSHA(ctx, in.main, "refs/heads/"+name) != ""

		return settledBranch{name: name, existing: foundBranch{name: name, remote: !local}}, nil
	}

	name, slugNote, err := branchName(ctx, in.driver, in.task, in.suffix, in.title)
	if err != nil {
		return settledBranch{}, err
	}

	base, dep, err := baseFor(ctx, in)
	if err != nil {
		return settledBranch{}, err
	}

	x, err := loadBranchIndex(ctx, in.main)
	if err != nil {
		return settledBranch{}, err
	}

	existing, err := x.find(ctx, name, in.task.ID, in.suffix, policyLocalOrOrigin)
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
func landedBranchFor(ctx context.Context, in startInput) (settledBranch, error) {
	name, slugNote, err := branchName(ctx, in.driver, in.task, "", in.title)
	if err != nil {
		return settledBranch{}, err
	}

	x, err := loadBranchIndex(ctx, in.main)
	if err != nil {
		return settledBranch{}, err
	}

	existing, err := x.find(ctx, name, in.task.ID, "", policyLocal)
	if err != nil {
		return settledBranch{}, err
	}

	return settledBranch{
		name:     name,
		slugNote: slugNote,
		base:     baseRef{ref: in.pick.sha, flag: in.pick.url + "'s landed commit"},
		existing: existing,
	}, nil
}

// landedNotes hands a branch this start made back from the watcher, as
// `f10 watch off` does: the PR landed before the branch existed, and a pass
// would report that as news every time. A branch the task already had keeps
// its watch, since its own work may still be live. A failed write is a note,
// since the worktree is the result.
func landedNotes(ctx context.Context, in startInput, name string, created bool) []string {
	if !created {
		return []string{fmt.Sprintf("%s is %s: %s is reused at its own tip and stays watched",
			in.pick.url, in.pick.state, name)}
	}

	var notes []string

	if err := branchmeta.Set(ctx, in.main, name, branchmeta.Watch, "false"); err != nil {
		notes = append(notes, "the watcher was not told to leave "+name+" alone: "+err.Error())
	}

	return append(notes, fmt.Sprintf("%s is %s: %s is local only at %s, and the watcher leaves it alone "+
		"(f10 watch on %s undoes that)", in.pick.url, in.pick.state, name, gitx.ShortSHA(in.pick.sha), in.task.ID))
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

	occ, err := agents(dir).Occupancy(ctx, ws)
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
