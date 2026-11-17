package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

type runService struct{ sup Supervisor }

func (s *runService) GetRunStatus(
	ctx context.Context,
	req *connect.Request[f10v1.GetRunStatusRequest],
) (*connect.Response[f10v1.GetRunStatusResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())
	o := output(c)

	rep, err := s.statusReport(ctx, m, c, &o.Stderr)
	if err != nil {
		return nil, o.Wrap(err)
	}

	view, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}

	var runs []*f10v1.RunStatus

	// a sentence alone carries no run
	if rep.Kind != statusview.ReportLine {
		var buf bytes.Buffer
		if err := statusview.Render(statusview.StatusView{Out: &buf, Warn: io.Discard, JSON: true}, rep); err != nil {
			return nil, err
		}

		if runs, err = runStatuses(buf.Bytes()); err != nil {
			return nil, err
		}
	}

	return connect.NewResponse(&f10v1.GetRunStatusResponse{Runs: runs, View: view, Printed: printedOf(o)}), nil
}

func (s *runService) Start(
	ctx context.Context,
	req *connect.Request[f10v1.StartRequest],
) (*connect.Response[f10v1.StartResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())

	mode, err := startMode(m.GetMode())
	if err != nil {
		return nil, err
	}

	// the exclusive pairs, then Herdr, then the task, in f10 start's order
	if err := start.RefuseFlags(
		start.Request{Mode: mode, Flow: m.GetFlow(), After: m.GetAfter(), Base: m.GetBase()},
	); err != nil {
		return nil, invalid(err.Error())
	}

	if err := requireAgents(c); err != nil {
		return nil, err
	}

	token, task := startToken(m)
	if strings.TrimSpace(token) == "" {
		return nil, invalid("start needs a task id")
	}

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "start")
	if err != nil {
		return nil, err
	}

	ctx = inSession(ctx, s.sup, c, t)

	if argv, flow, err := flowStart(ctx, t, m, token, task, mode); err != nil || flow {
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}

		return s.startDrive(t, c, argv)
	}

	in, err := start.Prepare(ctx, t, start.Request{
		Token: token,
		Mode:  mode,
		Flow:  m.GetFlow(),
		Pace:  m.GetPace(),
		After: m.GetAfter(),
		Base:  m.GetBase(),
		Task:  task,
		Agent: m.GetAgent(),
		Model: m.GetModel(),
		Note:  m.GetNote(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	o := output(c)
	in.Progress = &o.Stderr

	l, err := start.Launch(ctx, in)
	if err != nil {
		return nil, o.Wrap(err)
	}

	if err := start.WriteLaunched(ctx, &o.Stdout, in, l); err != nil {
		return nil, o.Wrap(err)
	}

	s.afterStart(ctx, &o.Stderr, t, c, in.Task.Label()+worktree.SuffixOf(in.Suffix))

	return connect.NewResponse(&f10v1.StartResponse{
		Task:        l.Task.Label(),
		Branch:      l.Branch,
		Worktree:    l.Path,
		WorkspaceId: l.Ws.ID,
		Agent:       l.Agent,
		Printed:     printedOf(o),
	}), nil
}

// flowStart is the drive's command line when the start runs the task
// through its flow: a task id, not a discussion or a picked-up PR, whose
// agent waits for a person and so is prompted once.
func flowStart(
	ctx context.Context, t *target.Target, m *f10v1.StartRequest, token, task, mode string,
) ([]string, bool, error) {
	if mode == launch.ModeDiscuss || task != "" {
		return nil, false, nil
	}

	pick, _, err := start.PickupFor(ctx, t, token)
	if err != nil || pick != nil {
		return nil, false, err
	}

	if strings.TrimSpace(m.GetNote()) != "" {
		return nil, false, errors.New("a note goes to an agent that waits: --discuss, or a picked-up PR")
	}

	argv := []string{token}
	if mode == launch.ModePlan {
		argv = append(argv, "--plan")
	}

	for _, f := range []struct{ name, value string }{
		{"--flow", m.GetFlow()},
		{"--pace", m.GetPace()},
		{"--after", m.GetAfter()},
		{"--base", m.GetBase()},
		{"--agent", m.GetAgent()},
		{"--model", m.GetModel()},
	} {
		if v := strings.TrimSpace(f.value); v != "" {
			argv = append(argv, f.name, v)
		}
	}

	if _, err := drivectl.ParseDriveArgs(argv); err != nil {
		return nil, false, err
	}

	return argv, true, nil
}

// startToken is the start's argument and the task a picked-up PR is for:
// the token as f10 start takes it, else a PR or a task id.
func startToken(m *f10v1.StartRequest) (string, string) {
	if m.GetToken() != "" {
		return m.GetToken(), m.GetTask()
	}

	return cmp.Or(m.GetPr(), m.GetTask()), pickedTask(m)
}

// callerEnv is the environment a caller's watch or drive runs with: its
// own, allowlisted, else what a drive started without one runs with.
func callerEnv(sup Supervisor, c *f10v1.CallerContext, t *target.Target) []string {
	if len(c.GetEnv()) > 0 {
		return daemon.DriveEnv(c.GetEnv(), t.Effective.DriveEnv)
	}

	return driveEnvFor(sup, t)
}

// requireAgents refuses outside Herdr: the local caller's session, else the
// daemon's own. The caller's refusal keeps its words, which name the
// commands that need Herdr.
func requireAgents(c *f10v1.CallerContext) error {
	if c == nil {
		return agents.RequireAgents()
	}

	err := agents.RequireAgentsIn(c.GetEnv())
	if agents.IsOutside(err) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}

	return err
}

// pickedTask is the task a picked-up PR is for, which only a PR start names.
func pickedTask(m *f10v1.StartRequest) string {
	if m.GetPr() == "" {
		return ""
	}

	return m.GetTask()
}

func startMode(m f10v1.StartMode) (string, error) {
	switch m {
	case f10v1.StartMode_START_MODE_UNSPECIFIED, f10v1.StartMode_START_MODE_SHIP:
		return "", nil
	case f10v1.StartMode_START_MODE_PLAN:
		return launch.ModePlan, nil
	case f10v1.StartMode_START_MODE_DISCUSS:
		return launch.ModeDiscuss, nil
	}

	return "", invalid("unknown start mode " + m.String())
}

func (s *runService) Drive(
	ctx context.Context,
	req *connect.Request[f10v1.DriveRequest],
) (*connect.Response[f10v1.DriveResponse], error) {
	m := req.Msg

	dir := m.GetDir()
	if dir == "" {
		d, err := projectDir(ctx, s.sup, m.GetProject())
		if err != nil {
			return nil, err
		}

		dir = d
	}

	argv := m.GetArgs()
	if len(argv) == 0 {
		var err error
		if argv, err = driveArgv(m); err != nil {
			return nil, err
		}
	}

	parsed, err := drivectl.ParseDriveArgs(argv)
	if err != nil {
		return nil, invalid(err.Error())
	}

	// a drive given answers starts its own process, so the answers reach it
	// instead of a running drive that would never read them
	resume := int(m.GetResume())
	if len(parsed.Answers) > 0 {
		resume = 0
	}

	t, terr := target.At(ctx, dir, false, "drive")
	if terr != nil {
		return nil, invalid(terr.Error())
	}

	env := daemon.LocalEnv(req, m.GetEnv())
	if len(env) == 0 {
		env = driveEnvFor(s.sup, t)
	}

	started, err := s.sup.RunDrive(daemon.DriveSpec{
		Argv:   argv,
		Dir:    dir,
		Root:   t.Layout.StorageRoot.Path(),
		Env:    env,
		Resume: resume,
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.DriveResponse{
		Drive:    int32(started.Drive), //nolint:gosec // a drive number
		Log:      started.Log,
		Attached: started.Attach,
	}), nil
}

// driveArgv is the command line f10 start takes for a drive given as fields.
func driveArgv(m *f10v1.DriveRequest) ([]string, error) {
	if m.GetPlanFirst() {
		return nil, connect.NewError(connect.CodeUnimplemented,
			errors.New("plan_first is not the binary's: f10 drive plan proposes, then start what it proposes"))
	}

	var argv []string

	if m.GetResume() > 0 {
		argv = append(argv, "--resume", strconv.Itoa(int(m.GetResume())))
	}

	tasks := m.GetTasks()

	switch {
	case m.GetChain() && len(tasks) != 2:
		return nil, invalid("chain takes the two ends of a range")
	case m.GetChain():
		argv = append(argv, tasks[0].GetId(), "--", tasks[1].GetId())
	default:
		for _, t := range tasks {
			id := t.GetId()
			if t.GetAgent() != "" {
				id += ":" + t.GetAgent()
			}

			argv = append(argv, id)
		}
	}

	for _, f := range []struct{ name, value string }{
		{"--flow", m.GetFlow()}, {"--pace", m.GetPace()}, {"--agent", m.GetAgent()}, {"--model", m.GetModel()},
	} {
		if f.value != "" {
			argv = append(argv, f.name, f.value)
		}
	}

	if m.GetParallel() > 0 {
		argv = append(argv, "--parallel", strconv.Itoa(int(m.GetParallel())))
	}

	if m.GetEvery() != nil {
		argv = append(argv, "--every", m.GetEvery().AsDuration().String())
	}

	for _, a := range m.GetAnswers() {
		argv = append(argv, "--answer", a)
	}

	if len(argv) == 0 {
		return nil, invalid("a drive needs its tasks, or the record to resume")
	}

	return argv, nil
}

// driveEnvFor is the environment a drive started without one runs with:
// the newest local session's, else the daemon's own, allowlisted.
func driveEnvFor(sup Supervisor, t *target.Target) []string {
	if env := sup.SessionEnv(); len(env) > 0 {
		return env
	}

	return daemon.DriveEnv(os.Environ(), t.Effective.DriveEnv)
}

func (s *runService) Finish(
	ctx context.Context,
	req *connect.Request[f10v1.FinishRequest],
) (*connect.Response[f10v1.FinishResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())

	if err := requireAgents(c); err != nil {
		return nil, err
	}

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "finish")
	if err != nil {
		return nil, err
	}

	// a local caller's own branch is the task when it names none
	if m.GetTask() == "" && c == nil {
		return nil, invalid("finish needs a task id")
	}

	ctx = inSession(ctx, s.sup, c, t)

	f := finish.Flags{
		Yes: m.GetYes(), IgnoreChecks: m.GetIgnoreChecks(), Keep: m.GetKeep(), Empty: m.GetEmpty(),
	}

	// a branch never pushed merges locally whatever the mode, as f10 finish does
	switch m.GetMerge() {
	case f10v1.MergeMode_MERGE_MODE_FORCE:
		f.Merge = true
	case f10v1.MergeMode_MERGE_MODE_CLOSE:
		f.LeaveOpen = true
	default:
	}

	// the daemon runs in no workspace, so it closes only a caller's own, and
	// leaves that one to the caller
	r := finish.Request{
		Token: m.GetTask(), Close: m.GetClose(), Flags: f,
		Cwd: cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot), DeferSelfClose: true,
	}
	if c != nil {
		r.Cwd, r.Workspace = c.GetCwd(), c.GetWorkspaceId()
	}

	var fin finish.Finished

	o := output(c)
	if err := finish.Run(ctx, &o.Stdout, &o.Stderr, t, r, &fin); err != nil {
		return nil, o.Wrap(err)
	}

	return connect.NewResponse(&f10v1.FinishResponse{
		Task:             cmp.Or(fin.Task, m.GetTask()),
		Pr:               fin.PR,
		Commit:           fin.Commit,
		RemovedWorktree:  fin.RemovedWorktree,
		RemovedBranch:    fin.RemovedBranch,
		ClosedWorkspace:  fin.ClosedWorkspace,
		Printed:          printedOf(o),
		CloseWorkspaceId: fin.CloseWorkspace,
		FocusWorkspaceId: fin.FocusWorkspace,
	}), nil
}

// Forward hands text to the agent working on a task, through the same core
// f10 forward runs, so its refusals (no worktree, no workspace, a dialog
// open in a stopped agent's pane) read the same.
func (s *runService) Forward(
	ctx context.Context,
	req *connect.Request[f10v1.ForwardRequest],
) (*connect.Response[f10v1.ForwardResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())

	text := strings.TrimSpace(m.GetText())
	if text == "" {
		return nil, invalid("forward needs the text to send")
	}

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "forward")
	if err != nil {
		return nil, err
	}

	id, suffix := ref.SplitSuffix(strings.TrimSpace(m.GetTask()))
	if id == "" {
		return nil, invalid("forward needs a task id")
	}

	r, err := t.Reference(ctx, id)
	if err != nil && c != nil {
		return nil, err
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	ctx = inSession(ctx, s.sup, c, t)

	// a bundle's member goes to the bundle's agent, named for its lead
	in := start.ForwardInput{
		Main:     cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Driver:   t.Driver(),
		Task:     r.Head(),
		Suffix:   suffix,
		Text:     text,
		StateDir: state.Dir(),
		Ttl:      state.TTL(),
		Now:      time.Now(),
	}

	// a task whose branch is the caller's own checkout is the caller's to run
	if c != nil {
		in.Here = t.Layout.CheckoutRoot
	}

	o := output(c)

	out, err := start.ForwardReport(ctx, &o.Stdout, in)
	if err != nil {
		if _, ok := errors.AsType[*connect.Error](err); ok || c != nil {
			return nil, err
		}

		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	return connect.NewResponse(&f10v1.ForwardResponse{
		Workspace: out.Workspace,
		Sent:      out.Sent,
		Note:      out.Note,
		Printed:   printedOf(o),
	}), nil
}

func (s *runService) ListDrives(
	ctx context.Context,
	req *connect.Request[f10v1.ListDrivesRequest],
) (*connect.Response[f10v1.ListDrivesResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "drive")
	if err != nil {
		return nil, err
	}

	root := t.Layout.StorageRoot.Path()

	recs, err := drivestore.List(root)
	if err != nil {
		return nil, err
	}

	o := output(c)
	if err := drive.DriveList(
		ctx,
		&o.Stdout,
		render.LinksFor(&o.Stdout, false),
		t.RefProject(ctx),
		root,
		m.GetAll(),
		drive.RowFinishedFor(ctx, t),
	); err != nil {
		return nil, o.Wrap(err)
	}

	out := &f10v1.ListDrivesResponse{Printed: printedOf(o)}
	for _, rec := range recs {
		out.Drives = append(out.Drives, driveRecordOf(root, rec))
	}

	return connect.NewResponse(out), nil
}

// driveRecordOf is a drive record as the API lists it.
//
//nolint:lostfield // a summary: the tasks and answers stay in the record
func driveRecordOf(root string, rec *drivestore.Record) *f10v1.DriveRecord {
	_, running := drivestore.Probe(root, drivestore.DriveKey(rec.Number))

	st := "stopped"

	switch {
	case rec.Cancelled != nil:
		st = "cancelled"
	case running:
		st = "running"
	case rec.Done():
		st = "done"
	}

	return &f10v1.DriveRecord{
		Number:  int32(rec.Number), //nolint:gosec // a drive number
		Args:    rec.Args,
		State:   st,
		Started: timestamppb.New(rec.Created),
	}
}

func (s *runService) CancelDrive(
	ctx context.Context,
	req *connect.Request[f10v1.CancelDriveRequest],
) (*connect.Response[f10v1.CancelDriveResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())

	if m.GetDrive() <= 0 && strings.TrimSpace(m.GetTask()) == "" {
		return nil, invalid("stop needs a drive number or a task id")
	}

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "stop")
	if err != nil {
		return nil, err
	}

	root, n, now := t.Layout.StorageRoot.Path(), int(m.GetDrive()), time.Now()

	if n <= 0 {
		if n, err = taskDrive(ctx, t, root, m.GetTask()); err != nil {
			return nil, err
		}
	}

	o := output(c)
	if err := drive.DriveCancel(&o.Stdout, root, n, now); err != nil {
		return nil, o.Wrap(err)
	}

	drive.CloseDriveAsks(asks.Dir(), root, n, now)

	return connect.NewResponse(&f10v1.CancelDriveResponse{Printed: printedOf(o)}), nil
}

// startDrive runs a flow start as the drive it is, as Drive does.
func (s *runService) startDrive(
	t *target.Target, c *f10v1.CallerContext, argv []string,
) (*connect.Response[f10v1.StartResponse], error) {
	started, err := s.sup.RunDrive(daemon.DriveSpec{
		Argv: argv,
		Dir:  cmp.Or(c.GetCheckout(), c.GetCwd(), t.Dir),
		Root: t.Layout.StorageRoot.Path(),
		Env:  callerEnv(s.sup, c, t),
	})
	if err != nil {
		return nil, err
	}

	o := output(c)
	fmt.Fprintf(&o.Stdout, "drive %d: f10 start %s\nlog: %s\n", started.Drive, strings.Join(argv, " "), started.Log)

	return connect.NewResponse(&f10v1.StartResponse{
		Drive:   int32(started.Drive), //nolint:gosec // a drive number
		Log:     started.Log,
		Printed: printedOf(o),
	}), nil
}

// taskDrive is the number of the drive holding the task's lease: a running
// drive, since a stopped one prompts nothing.
func taskDrive(ctx context.Context, t *target.Target, root, task string) (int, error) {
	r, err := t.Reference(ctx, strings.TrimSpace(task))
	if err != nil {
		return 0, invalid(err.Error())
	}

	id := r.Head().ID

	holder, held := drivestore.Probe(root, id)
	if !held || holder.Drive <= 0 {
		return 0, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("no running drive holds %s: f10 status --drives lists them", id))
	}

	return holder.Drive, nil
}

// statusReport reads what f10 status shows: every live run with all, the
// caller's own status bare, else a project's runs or one task's.
func (s *runService) statusReport(
	ctx context.Context, m *f10v1.GetRunStatusRequest, c *f10v1.CallerContext, warn io.Writer,
) (statusview.Report, error) {
	if m.GetTask() == "" && (m.GetAll() || (m.GetProject() == "" && c != nil)) {
		dir, scoped := cmp.Or(c.GetCheckout(), c.GetCwd()), c.GetCheckout() != ""
		if dir == "" {
			dir, _ = os.Getwd()
			scoped = true
		}

		return statusview.ReadBare(ctx, warn, dir, scoped, c.GetSession(), m.GetAll())
	}

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "status")
	if err != nil {
		return statusview.Report{}, err
	}

	if m.GetTask() == "" {
		return statusview.ReadBare(ctx, warn, t.Dir, true, "", false)
	}

	rd, err := statusview.NewStatusReader(t, time.Now())
	if err != nil {
		return statusview.Report{}, err
	}

	return rd.ReadTask(ctx, warn, m.GetTask(), false)
}

// afterStart is start's upkeep once the task is open, each step a warning
// at most: the launch status lists for the caller's session, the watch, and
// the janitor sweep when one is due.
func (s *runService) afterStart(
	ctx context.Context,
	warn io.Writer,
	t *target.Target,
	c *f10v1.CallerContext,
	task string,
) {
	if sid := c.GetSession(); sid != "" {
		if err := state.AppendLaunch(state.Dir(), sid, state.Launch{Kind: state.LaunchStart, Value: task}); err != nil {
			ui.Warnf(warn, "recording the launch: %v", err)
		}
	}

	if t.Effective.Watch != "off" {
		if _, err := s.sup.WatchProject(
			t.Layout.StorageRoot.Path(),
			cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
			callerEnv(s.sup, c, t),
		); err != nil && c != nil {
			fmt.Fprintf(warn, "watch: warning: %v\n", err)
		}
	}

	finish.SweepIfDue(ctx, warn, filepath.Join(t.Layout.StorageRoot.Path(), finish.JanitorStamp), time.Now(),
		func(ctx context.Context) ([]string, error) { return finish.Janitor(ctx, finish.JanitorFor(t)) })
}
