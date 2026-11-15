// Package agentsrpc serves agents.v1 over the agents core: the workspaces
// and agents of the multiplexer, text sent to an agent, and the asks inbox.
package agentsrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asks"
)

// Options are what the owner of the core supplies: how an answer moves on
// to a drive, which the agents core knows nothing about, and the inbox.
type Options struct {
	Deliverer agents.Deliverer
	AsksDir   func() string // the inbox; asks.Dir when nil
	Now       func() time.Time
}

// Mount adds AgentService and AskService to mux.
func Mount(mux *http.ServeMux, o Options, opts ...connect.HandlerOption) {
	if o.AsksDir == nil {
		o.AsksDir = asks.Dir
	}

	if o.Now == nil {
		o.Now = time.Now
	}

	mux.Handle(agentsv1connect.NewAgentServiceHandler(agentService{}, opts...))
	mux.Handle(agentsv1connect.NewAskServiceHandler(askService{o: o}, opts...))
}

type agentService struct{}

// backend is the agents backend, refused when it cannot be reached.
func backend() (agentctl.Backend, error) {
	b := agents.Agents("")
	if err := b.Available(); err != nil {
		return nil, err
	}

	return b, nil
}

func (agentService) ListWorkspaces(
	ctx context.Context,
	_ *connect.Request[agentsv1.ListWorkspacesRequest],
) (*connect.Response[agentsv1.ListWorkspacesResponse], error) {
	b, err := backend()
	if err != nil {
		return nil, err
	}

	wss, err := b.Workspaces(ctx)
	if err != nil {
		return nil, err
	}

	out := &agentsv1.ListWorkspacesResponse{}
	for _, w := range wss {
		out.Workspaces = append(out.Workspaces, &agentsv1.Workspace{
			Id:          w.ID,
			Label:       w.Label,
			Path:        w.Path,
			Focused:     w.Focused,
			AgentStatus: statusOf(w.Status),
		})
	}

	return connect.NewResponse(out), nil
}

func (agentService) ListAgents(
	ctx context.Context,
	req *connect.Request[agentsv1.ListAgentsRequest],
) (*connect.Response[agentsv1.ListAgentsResponse], error) {
	all, err := listAgents(ctx)
	if err != nil {
		return nil, err
	}

	out := &agentsv1.ListAgentsResponse{}
	for _, a := range all {
		if ws := req.Msg.GetWorkspaceId(); ws == "" || ws == a.Workspace {
			out.Agents = append(out.Agents, agentOf(a))
		}
	}

	return connect.NewResponse(out), nil
}

func (agentService) GetAgentStatus(
	ctx context.Context,
	req *connect.Request[agentsv1.GetAgentStatusRequest],
) (*connect.Response[agentsv1.GetAgentStatusResponse], error) {
	all, err := listAgents(ctx)
	if err != nil {
		return nil, err
	}

	for _, a := range all {
		if a.Name == req.Msg.GetName() {
			return connect.NewResponse(&agentsv1.GetAgentStatusResponse{Agent: agentOf(a)}), nil
		}
	}

	return nil, connect.NewError(
		connect.CodeNotFound,
		errors.New("no agent is named "+strconv.Quote(req.Msg.GetName())),
	)
}

func (agentService) SendText(
	ctx context.Context,
	req *connect.Request[agentsv1.SendTextRequest],
) (*connect.Response[agentsv1.SendTextResponse], error) {
	b, err := backend()
	if err != nil {
		return nil, err
	}

	m := req.Msg
	if m.GetName() == "" || m.GetText() == "" {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("send text needs an agent name and the text"),
		)
	}

	// the backends type a prompt and press Enter as one action
	if !m.GetSubmit() {
		return nil, connect.NewError(connect.CodeUnimplemented,
			errors.New("typing text without submitting it is not supported: set submit"))
	}

	err = b.Prompt(ctx, m.GetName(), m.GetText())
	if errors.Is(err, agentctl.ErrAgentNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&agentsv1.SendTextResponse{}), nil
}

func listAgents(ctx context.Context) ([]agentctl.AgentInfo, error) {
	b, err := backend()
	if err != nil {
		return nil, err
	}

	l, ok := b.(agentctl.Lister)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New(b.Name()+" does not list its agents"))
	}

	return l.Agents(ctx)
}

func agentOf(a agentctl.AgentInfo) *agentsv1.Agent {
	return &agentsv1.Agent{
		Name:        a.Name,
		WorkspaceId: a.Workspace,
		PaneId:      a.Slot,
		Kind:        a.Kind,
		Status:      statusOf(a.Status),
	}
}

func statusOf(s agentctl.Status) agentsv1.AgentStatus {
	switch s {
	case agentctl.StatusIdle:
		return agentsv1.AgentStatus_AGENT_STATUS_IDLE
	case agentctl.StatusWorking:
		return agentsv1.AgentStatus_AGENT_STATUS_WORKING
	case agentctl.StatusDone:
		return agentsv1.AgentStatus_AGENT_STATUS_DONE
	case "":
		return agentsv1.AgentStatus_AGENT_STATUS_UNSPECIFIED
	}

	return agentsv1.AgentStatus_AGENT_STATUS_UNKNOWN
}

type askService struct{ o Options }

func (s askService) ListAsks(
	_ context.Context,
	req *connect.Request[agentsv1.ListAsksRequest],
) (*connect.Response[agentsv1.ListAsksResponse], error) {
	dir := s.o.AsksDir()

	open, err := asks.Open(dir)
	if err != nil {
		return nil, err
	}

	out := &agentsv1.ListAsksResponse{}
	for _, a := range open {
		out.Open = append(out.Open, AskOf(a))
	}

	if since := req.Msg.GetAnsweredSince(); since != nil {
		answered, err := asks.Answered(dir, since.AsTime())
		if err != nil {
			return nil, err
		}

		for _, a := range answered {
			out.Answered = append(out.Answered, AskOf(a))
		}
	}

	return connect.NewResponse(out), nil
}

func (s askService) AnswerAsk(
	ctx context.Context,
	req *connect.Request[agentsv1.AnswerAskRequest],
) (*connect.Response[agentsv1.AnswerAskResponse], error) {
	dir := s.o.AsksDir()

	a, err := agents.FindKey(dir, req.Msg.GetId())
	if err != nil {
		if _, closed := errors.AsType[asks.ClosedError](err); closed {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	values := make([]string, 0, len(req.Msg.GetAnswers()))
	for _, ans := range req.Msg.GetAnswers() {
		values = append(values, strconv.Itoa(int(ans.GetN()))+"="+ans.GetValue())
	}

	claimed, sent, err := agents.Answer(ctx, io.Discard, dir, a, values, asks.ByAPI, s.o.Deliverer)

	switch {
	case err != nil && claimed.Task != "":
		// the answer is recorded and closes the ask; only moving it on failed
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("answer recorded, not delivered: %w", err))
	case err != nil:
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&agentsv1.AnswerAskResponse{Ask: AskOf(claimed), Delivered: sent}), nil
}

// AskOf is an inbox ask as agents.v1 carries it: what f10 adds (the task,
// its project and drive) goes in the labels, so the core grows no task field.
//
//nolint:lostfield // f10's fields go in Labels; TakenAt is drive bookkeeping, and the inbox records no workspace
func AskOf(a asks.Ask) *agentsv1.Ask {
	out := &agentsv1.Ask{
		Id:         a.Key(),
		Agent:      agentctl.AgentName(a.Task, a.Suffix, a.Worktree),
		Text:       a.Text,
		AskedAt:    timestamppb.New(a.AskedAt),
		Answer:     a.Answer,
		AnsweredBy: SourceOf(a.By),
		Labels:     asks.Labels(a),
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

// SourceOf is the way in an answer came by, as agents.v1 names it.
func SourceOf(by string) agentsv1.AnswerSource {
	switch by {
	case asks.ByPane:
		return agentsv1.AnswerSource_ANSWER_SOURCE_PANE
	case asks.ByAsks:
		return agentsv1.AnswerSource_ANSWER_SOURCE_ASKS
	case asks.BySkill:
		return agentsv1.AnswerSource_ANSWER_SOURCE_SKILL
	case asks.ByDrive:
		return agentsv1.AnswerSource_ANSWER_SOURCE_DRIVE
	case asks.ByCancelled:
		return agentsv1.AnswerSource_ANSWER_SOURCE_CANCELLED
	case asks.ByAPI:
		return agentsv1.AnswerSource_ANSWER_SOURCE_API
	}

	return agentsv1.AnswerSource_ANSWER_SOURCE_UNSPECIFIED
}
