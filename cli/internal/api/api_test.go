package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/oklog/ulid/v2"
	"google.golang.org/protobuf/types/known/durationpb"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/target"
)

// fakeSup is a daemon that records the drives it is asked to run.
type fakeSup struct {
	drives []daemon.DriveSpec
}

func (*fakeSup) Status() daemon.Status { return daemon.Status{} }

func (*fakeSup) WatchProject(string, string, []string) (string, error) { return "", nil }

func (f *fakeSup) RunDrive(spec daemon.DriveSpec) (daemon.Started, error) {
	f.drives = append(f.drives, spec)

	return daemon.Started{Log: "/logs/1.log"}, nil
}

func (*fakeSup) ResumeDrive(string, string, []string, int) (daemon.Started, error) {
	return daemon.Started{}, nil
}

func (*fakeSup) SessionEnv() []string { return []string{"HERDR_ENV=1"} }

func (*fakeSup) Remember([]string) {}

func TestDriveArgvFromFields(t *testing.T) {
	t.Parallel()

	got, err := driveArgv(&f10v1.DriveRequest{
		Tasks:    []*f10v1.DriveTask{{Id: "FT-1"}, {Id: "FT-2", Agent: "fast"}},
		Flow:     "local",
		Parallel: 2,
		Every:    durationpb.New(15 * time.Second),
		Answers:  []string{"FT-1=yes"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"FT-1",
		"FT-2:fast",
		"--flow",
		"local",
		"--parallel",
		"2",
		"--every",
		"15s",
		"--answer",
		"FT-1=yes",
	}
	if !slices.Equal(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}

	if got, _ := driveArgv(
		&f10v1.DriveRequest{Chain: true, Tasks: []*f10v1.DriveTask{{Id: "FT-1"}, {Id: "FT-4"}}},
	); !slices.Equal(
		got,
		[]string{"FT-1", "--", "FT-4"},
	) {
		t.Errorf("a chain = %v, want the range FT-1 -- FT-4", got)
	}

	for _, bad := range []*f10v1.DriveRequest{
		{},
		{PlanFirst: true, Tasks: []*f10v1.DriveTask{{Id: "FT-1"}}},
		{Chain: true, Tasks: []*f10v1.DriveTask{{Id: "FT-1"}}},
	} {
		if _, err := driveArgv(bad); err == nil {
			t.Errorf("%v: no refusal", bad)
		}
	}
}

// A drive from the CLI passes its typed arguments and its own environment,
// which the handler hands the supervisor unchanged.
func TestDriveRunsTheTypedArguments(t *testing.T) {
	t.Parallel()

	sup := &fakeSup{}
	srv := httptest.NewServer(Handler(sup))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewRunServiceClient(srv.Client(), srv.URL)

	res, err := c.Drive(t.Context(), connect.NewRequest(&f10v1.DriveRequest{
		Args: []string{"FT-1", "--pace", "fast"},
		Dir:  t.TempDir(),
		Env:  []string{"A=1"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetLog() != "/logs/1.log" || len(sup.drives) != 1 ||
		!slices.Equal(sup.drives[0].Argv, []string{"FT-1", "--pace", "fast"}) {
		t.Errorf("drive = %v, ran %+v", res.Msg, sup.drives)
	}

	// over httptest the peer has a network address, so the passed
	// environment is not honored and the session's runs the drive
	if !slices.Equal(sup.drives[0].Env, []string{"HERDR_ENV=1"}) {
		t.Errorf("env = %v, want the session's for a non-socket peer", sup.drives[0].Env)
	}

	_, err = c.Drive(t.Context(), connect.NewRequest(&f10v1.DriveRequest{Args: []string{"--parallel", "x"}, Dir: "/"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad flag = %v, want InvalidArgument", err)
	}
}

func TestRunStatusesReadEveryShape(t *testing.T) {
	t.Parallel()

	runs := `[{"session":"s1","task":"FT-1","capture":"prior","plan":"done","ship":"blocked","leaf":"judge",
		"updated":"2026-11-15T10:00:00Z","agent":"fast","gates":[{"step":"judge","state":"blocked",
		"outcome":{"verdict":"stop","changes":0}}],"shipment":{"task":"FT-1","pr":"https://x/1",
		"checks":{"state":"Red","sha":"abc","required":true,"failed":[{"name":"lint"}]},
		"verdicts":[{"subject":"plan","path":"/v/1.md"}]}}]`

	got, err := runStatuses([]byte(runs))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("runs = %v", got)
	}

	r := got[0]
	if r.GetRun().GetShip() != f10v1.PhaseStatus_PHASE_STATUS_BLOCKED || r.GetAgent() != "fast" ||
		r.GetGates()[0].GetOutcome().GetVerdict() != "stop" ||
		r.GetShipment().GetChecks().GetFailed()[0].GetName() != "lint" ||
		r.GetShipment().GetVerdicts()[0].GetName() != "plan" {
		t.Errorf("status = %v", r)
	}

	fallback := `{"task":"FT-2","root":"/r","others":[{"session":"s2","task":"FT-9","capture":"done","plan":"running",
		"ship":"","updated":"2026-11-15T10:00:00Z"}],"plan":"/p/FT-2.md"}`

	got, err = runStatuses([]byte(fallback))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].GetShipment().GetPlan() != "/p/FT-2.md" || got[1].GetRun().GetTask() != "FT-9" ||
		got[1].GetRun().GetPlan() != f10v1.PhaseStatus_PHASE_STATUS_RUNNING {
		t.Errorf("fallback = %v", got)
	}

	rows := `[{"id":"FT-3","title":"Rows","depth":0,"stage":"pr","pr":"https://x/3"}]`
	if got, err = runStatuses([]byte(rows)); err != nil || got[0].GetShipment().GetTask() != "FT-3" {
		t.Errorf("rows = %v, %v", got, err)
	}
}

func TestBoardOfNamesTheView(t *testing.T) {
	t.Parallel()

	b, err := boardOf(lookaround.Board{View: lookaround.ViewFeed, Project: lookaround.Project{Name: "r"}})
	if err != nil {
		t.Fatal(err)
	}

	if b.GetView() != f10v1.BoardView_BOARD_VIEW_CHANGES || b.GetProject().GetName() != "r" {
		t.Errorf("board = %v", b)
	}
}

// The stream sends the rows inserted after the cursor, then rows as they
// land; a kind with no message arrives without a payload.
func TestWatchEventsResumesAfterTheCursor(t *testing.T) {
	seam.Swap(t, &EventPoll, 20*time.Millisecond)

	// minted now, inserted last: a spooled row whose id predates the others
	late := ulid.Make().String()

	// Record fills ids on its own copies, so the test names them
	first := ledger.MustEvent(ledger.WaitStarted, &ledger.WaitStartedPayload{})
	first.ID, first.Task, first.Session = ulid.Make().String(), "FT-1", "s1"
	second := ledger.MustEvent(ledger.AskAnswered, &ledger.AskAnsweredPayload{
		Id: "k", Answer: "yes", AnsweredBy: agentsv1.AnswerSource_ANSWER_SOURCE_PANE,
	})
	second.ID, second.Task = ulid.Make().String(), "FT-1"
	ledger.Record(first, second)

	srv := httptest.NewServer(Handler(&fakeSup{}))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewEventServiceClient(srv.Client(), srv.URL)

	watch := func(after string) *connect.ServerStreamForClient[f10v1.WatchEventsResponse] {
		stream, err := c.WatchEvents(
			t.Context(),
			connect.NewRequest(&f10v1.WatchEventsRequest{After: after, Task: "FT-1"}),
		)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = stream.Close() })

		return stream
	}

	all := watch("")
	if !all.Receive() || all.Msg().GetEvent().GetId() != first.ID || all.Msg().GetEvent().GetSession() != "s1" {
		t.Fatalf("first event = %v, %v", all.Msg(), all.Err())
	}

	stream := watch(all.Msg().GetCursor())
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}

	ev := stream.Msg().GetEvent()
	if ev.GetId() != second.ID || ev.GetAskAnswered().GetAnsweredBy() != agentsv1.AnswerSource_ANSWER_SOURCE_PANE {
		t.Fatalf("event after the cursor = %v, want the second row", ev)
	}

	third := ledger.MustEvent(ledger.WaitEnded, &ledger.WaitEndedPayload{})
	third.ID, third.Task = late, "FT-1"
	ledger.Record(third)

	if !stream.Receive() || stream.Msg().GetEvent().GetId() != late {
		t.Fatalf("a row with an older id, inserted during the stream, did not arrive: %v", stream.Err())
	}

	if ev, err := EventOf(ledger.Event{ID: "x", Kind: "made_up"}); err != nil || ev.GetPayload() != nil {
		t.Errorf("an unknown kind = %v, %v; want no payload", ev, err)
	}
}

// A drive given answers starts its own process even when the request names
// the record to resume, so the answers reach a process that reads them.
func TestDriveWithAnswersNeverAttaches(t *testing.T) {
	t.Parallel()

	sup := &fakeSup{}
	srv := httptest.NewServer(Handler(sup))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewRunServiceClient(srv.Client(), srv.URL)

	if _, err := c.Drive(t.Context(), connect.NewRequest(&f10v1.DriveRequest{
		Args:   []string{"--resume", "4", "--answer", "FT-1=yes"},
		Dir:    t.TempDir(),
		Resume: 4,
	})); err != nil {
		t.Fatal(err)
	}

	if len(sup.drives) != 1 || sup.drives[0].Resume != 0 {
		t.Errorf("ran %+v, want no resume number so the daemon starts a process", sup.drives)
	}
}

// watchSup watches two checkouts that share a directory name.
type watchSup struct{ fakeSup }

func (*watchSup) Status() daemon.Status {
	return daemon.Status{Watches: []daemon.Watch{{Dir: "/a/widgets"}, {Dir: "/b/widgets"}}}
}

func TestProjectDirRefusesAnAmbiguousName(t *testing.T) {
	seam.Swap(t, &target.ProjectOf, func(_ context.Context, dir string) string {
		return "github.com/" + filepath.Base(filepath.Dir(dir)) + "/widgets"
	})

	if dir, err := projectDir(t.Context(), &watchSup{}, "github.com/b/widgets"); err != nil || dir != "/b/widgets" {
		t.Errorf("an exact id = %q, %v; want /b/widgets", dir, err)
	}

	if _, err := projectDir(t.Context(), &watchSup{}, "widgets"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a name two watched checkouts share = %v, want InvalidArgument", err)
	}

	// a full id no watch holds is never another project's checkout of the same name
	if dir, err := projectDir(
		t.Context(),
		&watchSup{},
		"github.com/c/widgets",
	); connect.CodeOf(
		err,
	) != connect.CodeNotFound {
		t.Errorf("an unwatched full id = %q, %v; want NotFound", dir, err)
	}
}

// An ask row written before payloads were messages stores an unanswered
// ask's answered_by as ""; it still streams with its payload.
func TestEventOfReadsALegacyAskRow(t *testing.T) {
	var e ledger.Event
	if err := json.Unmarshal([]byte(`{"id":"01","kind":"ask_raised","payload":{"v":1,"ask":{`+
		`"id":"f10:FT-1","agent":"a","workspace_id":"","text":"1. a?","questions":null,`+
		`"asked_at":"2026-11-14T10:30:15+02:00","answer":"","answered_by":"","labels":{"task":"FT-1"}}}}`), &e); err != nil {
		t.Fatal(err)
	}

	ev, err := EventOf(e)
	if err != nil {
		t.Fatal(err)
	}

	ask := ev.GetAskRaised().GetAsk()
	if ask.GetId() != "f10:FT-1" || ask.GetAnsweredBy() != agentsv1.AnswerSource_ANSWER_SOURCE_UNSPECIFIED ||
		!ask.GetAskedAt().AsTime().Equal(time.Date(2026, 11, 14, 8, 30, 15, 0, time.UTC)) {
		t.Errorf("ask %v", ask)
	}
}

// A tail stream skips the backlog: its first response only places the
// tail, and then it sends only what lands after it opened.
func TestWatchEventsFromTheTail(t *testing.T) {
	seam.Swap(t, &EventPoll, 20*time.Millisecond)

	old := ledger.MustEvent(ledger.WaitStarted, &ledger.WaitStartedPayload{})
	old.ID, old.Task = ulid.Make().String(), "FT-2"
	ledger.Record(old)

	srv := httptest.NewServer(Handler(&fakeSup{}))
	t.Cleanup(srv.Close)

	stream, err := f10v1connect.NewEventServiceClient(srv.Client(), srv.URL).WatchEvents(
		t.Context(), connect.NewRequest(&f10v1.WatchEventsRequest{Task: "FT-2", Tail: true}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = stream.Close() })

	if !stream.Receive() || stream.Msg().GetEvent() != nil || stream.Msg().GetCursor() == "" {
		t.Fatalf("first response from the tail = %v, %v; want a start marker", stream.Msg(), stream.Err())
	}

	// recorded once the marker placed the tail, so it always lands after it
	next := ledger.MustEvent(ledger.WaitEnded, &ledger.WaitEndedPayload{})
	next.ID, next.Task = ulid.Make().String(), "FT-2"
	ledger.Record(next)

	if !stream.Receive() || stream.Msg().GetEvent().GetId() != next.ID {
		t.Fatalf("first event from the tail = %v, %v; want only the new row", stream.Msg(), stream.Err())
	}
}

// projSup runs a drive in a worktree of a watched project.
type projSup struct{ fakeSup }

func (*projSup) Status() daemon.Status {
	return daemon.Status{
		Watches: []daemon.Watch{{Dir: "/code/widgets"}},
		Runs:    []daemon.Run{{Dir: "/code/widgets.FT-1"}, {Dir: "/code/gadgets"}},
	}
}

func TestListProjectsOncePerID(t *testing.T) {
	seam.Swap(t, &target.ProjectOf, func(_ context.Context, dir string) string {
		return "github.com/acme/" + strings.Split(filepath.Base(dir), ".")[0]
	})

	srv := httptest.NewServer(Handler(&projSup{}))
	t.Cleanup(srv.Close)

	res, err := f10v1connect.NewProjectServiceClient(srv.Client(), srv.URL).
		ListProjects(t.Context(), connect.NewRequest(&f10v1.ListProjectsRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, p := range res.Msg.GetProjects() {
		got = append(got, p.GetId()+"="+strconv.FormatBool(p.GetWatched()))
	}

	if strings.Join(got, " ") != "github.com/acme/gadgets=false github.com/acme/widgets=true" {
		t.Errorf("projects = %v", got)
	}

	// every listed id resolves, a drive-only project included
	for _, p := range res.Msg.GetProjects() {
		if _, err := projectDir(t.Context(), &projSup{}, p.GetId()); err != nil {
			t.Errorf("listed %s does not resolve: %v", p.GetId(), err)
		}
	}
}

func TestForwardNeedsText(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(Handler(&fakeSup{}))
	t.Cleanup(srv.Close)

	_, err := f10v1connect.NewRunServiceClient(srv.Client(), srv.URL).
		Forward(t.Context(), connect.NewRequest(&f10v1.ForwardRequest{Project: "/x", Task: "FT-1", Text: "  "}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty text = %v, want InvalidArgument", err)
	}
}

// A start of a task by its id runs as a drive with the start's flags. A
// discussion and a PR named by --task stay one-shot.
func TestFlowStartArgv(t *testing.T) {
	m := &f10v1.StartRequest{Flow: "local", Pace: "fast", Agent: "opus", After: "GH-7"}

	argv, flow, err := flowStart(t.Context(), nil, m, "GH-1", "", "")
	if err != nil || !flow {
		t.Fatalf("flow = %v, %v", flow, err)
	}

	if got := strings.Join(argv, " "); got != "GH-1 --flow local --pace fast --after GH-7 --agent opus" {
		t.Errorf("argv = %q", got)
	}

	planned, _, _ := flowStart(t.Context(), nil, &f10v1.StartRequest{}, "GH-1", "", launch.ModePlan)
	if argv := strings.Join(planned, " "); argv != "GH-1 --plan" {
		t.Errorf("plan argv = %q", argv)
	}

	for _, c := range []struct{ task, mode string }{{"", launch.ModeDiscuss}, {"GH-3", ""}} {
		_, flow, err := flowStart(t.Context(), nil, &f10v1.StartRequest{}, "GH-1", c.task, c.mode)
		if flow || err != nil {
			t.Errorf("task %q mode %q: flow = %v, %v; want one-shot", c.task, c.mode, flow, err)
		}
	}

	if _, _, err := flowStart(t.Context(), nil, &f10v1.StartRequest{Note: "hi"}, "GH-1", "", ""); err == nil {
		t.Error("a note on a flow start was taken")
	}
}
