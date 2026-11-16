package api

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
)

type runService struct{ sup Supervisor }

func (s *runService) GetRunStatus(
	ctx context.Context,
	req *connect.Request[f10v1.GetRunStatusRequest],
) (*connect.Response[f10v1.GetRunStatusResponse], error) {
	m := req.Msg

	var buf bytes.Buffer

	v := statusview.StatusView{Out: &buf, Warn: io.Discard, JSON: true}

	switch {
	case m.GetAll():
		dir, _ := os.Getwd()
		if _, err := statusview.BareStatus(ctx, v, dir, true, "", true); err != nil {
			return nil, err
		}
	default:
		t, err := projectTarget(ctx, s.sup, m.GetProject(), "status")
		if err != nil {
			return nil, err
		}

		if m.GetTask() == "" {
			if _, err := statusview.BareStatus(ctx, v, t.Dir, true, "", false); err != nil {
				return nil, err
			}

			break
		}

		rd, err := statusview.NewStatusReader(t, time.Now())
		if err != nil {
			return nil, err
		}

		if _, err := rd.Task(ctx, v, m.GetTask(), false); err != nil {
			return nil, err
		}
	}

	runs, err := runStatuses(buf.Bytes())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.GetRunStatusResponse{Runs: runs}), nil
}

func (s *runService) Start(
	ctx context.Context,
	req *connect.Request[f10v1.StartRequest],
) (*connect.Response[f10v1.StartResponse], error) {
	m := req.Msg

	if err := agents.RequireAgents(); err != nil {
		return nil, err
	}

	t, err := projectTarget(ctx, s.sup, m.GetProject(), "start")
	if err != nil {
		return nil, err
	}

	mode, err := startMode(m.GetMode())
	if err != nil {
		return nil, err
	}

	in, err := start.Prepare(ctx, t, start.Request{
		Token: cmp.Or(m.GetPr(), m.GetTask()),
		Mode:  mode,
		Flow:  m.GetFlow(),
		Pace:  m.GetPace(),
		After: m.GetAfter(),
		Base:  m.GetBase(),
		Task:  pickedTask(m),
		Agent: m.GetAgent(),
		Model: m.GetModel(),
		Note:  m.GetNote(),
	})
	if err != nil {
		return nil, invalid(err.Error())
	}

	l, err := start.Launch(ctx, in)
	if err != nil {
		return nil, err
	}

	// the started task is watched like one a command started; a watch is
	// upkeep, so the start stands without it
	if t.Effective.Watch != "off" {
		_, _ = s.sup.WatchProject(
			t.Layout.StorageRoot.Path(),
			cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
			driveEnvFor(s.sup, t),
		)
	}

	return connect.NewResponse(&f10v1.StartResponse{
		Task:        l.Task.Label(),
		Branch:      l.Branch,
		Worktree:    l.Path,
		WorkspaceId: l.Ws.ID,
		Agent:       l.Agent,
	}), nil
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

// driveArgv is the command line f10 drive takes for a drive given as fields.
func driveArgv(m *f10v1.DriveRequest) ([]string, error) {
	if m.GetPlanFirst() {
		return nil, connect.NewError(connect.CodeUnimplemented,
			errors.New("plan_first is the /f10:drive skill's, not the binary's: plan each task, then drive"))
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

	if err := agents.RequireAgents(); err != nil {
		return nil, err
	}

	t, err := projectTarget(ctx, s.sup, m.GetProject(), "finish")
	if err != nil {
		return nil, err
	}

	if m.GetTask() == "" {
		return nil, invalid("finish needs a task id")
	}

	f := finish.Flags{IgnoreChecks: m.GetIgnoreChecks(), Keep: m.GetKeep()}

	// a branch never pushed merges locally whatever the mode, as f10 finish does
	switch m.GetMerge() {
	case f10v1.MergeMode_MERGE_MODE_FORCE:
		f.Merge = true
	case f10v1.MergeMode_MERGE_MODE_CLOSE:
		f.LeaveOpen = true
	default:
	}

	var out finish.Finished

	// the daemon runs in no workspace, so it never closes its own
	if err := finish.Run(ctx, io.Discard, io.Discard, t, finish.Request{
		Token: m.GetTask(),
		Flags: f,
		Cwd:   cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
	}, &out); err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.FinishResponse{
		Task:            cmp.Or(out.Task, m.GetTask()),
		Pr:              out.PR,
		Commit:          out.Commit,
		RemovedWorktree: out.RemovedWorktree,
		RemovedBranch:   out.RemovedBranch,
		ClosedWorkspace: out.ClosedWorkspace,
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

	text := strings.TrimSpace(m.GetText())
	if text == "" {
		return nil, invalid("forward needs the text to send")
	}

	t, err := projectTarget(ctx, s.sup, m.GetProject(), "forward")
	if err != nil {
		return nil, err
	}

	id, suffix := ref.SplitSuffix(strings.TrimSpace(m.GetTask()))
	if id == "" {
		return nil, invalid("forward needs a task id")
	}

	r, err := t.Reference(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	out, err := start.Forward(ctx, start.ForwardInput{
		Main:     cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Driver:   t.Driver(),
		Task:     r.Head(), // a bundle's member goes to the bundle's agent
		Suffix:   suffix,
		Text:     text,
		StateDir: state.Dir(),
		Ttl:      state.TTL(),
		Now:      time.Now(),
	})
	if err != nil {
		if _, ok := errors.AsType[*connect.Error](err); ok {
			return nil, err
		}

		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	return connect.NewResponse(&f10v1.ForwardResponse{
		Workspace: out.Workspace,
		Sent:      out.Sent,
		Note:      out.Note,
	}), nil
}
