// Package agentsrpc serves agents.v1 over the agents core: the workspaces
// and agents of the multiplexer, text sent to an agent, and the inbox.
package agentsrpc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/printed"
)

// Options are what the owner of the core supplies: how an answer moves on
// to a drive, which the agents core knows nothing about, and the inbox.
type Options struct {
	Deliverer agents.Deliverer
	AsksDir   func() string // the inbox; asks.Dir when nil
	Now       func() time.Time
	// Session runs ctx's commands in a local caller's env, its agent calls in
	// the session that env names; nil leaves ctx as it is
	Session func(ctx context.Context, env []string) context.Context
	// Links are the task urls the owner knows for asks, by display project
	// and task id, so a terminal's list links them; nil knows none
	Links func(ctx context.Context, all []asks.Ask) map[string]map[string]string
}

// Mount adds AgentService and InboxService to mux.
func Mount(mux *http.ServeMux, o Options, opts ...connect.HandlerOption) {
	if o.AsksDir == nil {
		o.AsksDir = asks.Dir
	}

	if o.Now == nil {
		o.Now = time.Now
	}

	if o.Session == nil {
		o.Session = func(ctx context.Context, _ []string) context.Context { return ctx }
	}

	mux.Handle(agentsv1connect.NewAgentServiceHandler(agentService{o: o}, opts...))
	mux.Handle(agentsv1connect.NewInboxServiceHandler(inboxService{o: o}, opts...))
}

type agentService struct{ o Options }

// callerOf is the request's caller context, nil from any listener but the
// unix socket or this process: a unix socket's peer has no network address.
func callerOf(req connect.AnyRequest, c *agentsv1.CallerContext) *agentsv1.CallerContext {
	if addr := req.Peer().Addr; c == nil || (addr != "" && addr != "@") {
		return nil
	}

	return c
}

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

// CloseWorkspace closes the workspace a caller ran in, which f10 finish
// leaves to the caller so the close does not end it mid-call.
func (s agentService) CloseWorkspace(
	ctx context.Context,
	req *connect.Request[agentsv1.CloseWorkspaceRequest],
) (*connect.Response[agentsv1.CloseWorkspaceResponse], error) {
	m := req.Msg
	if m.GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("close workspace needs its id"))
	}

	ctx = s.o.Session(ctx, callerOf(req, m.GetCaller()).GetEnv())

	b := agents.Agents("")

	if f := m.GetFocusId(); f != "" {
		if err := b.Focus(ctx, f); err != nil {
			return nil, fmt.Errorf("focusing the main workspace failed: %w", err)
		}
	}

	if err := b.Close(ctx, m.GetId()); err != nil {
		return nil, fmt.Errorf("closing workspace %s failed: %w", m.GetId(), err)
	}

	return connect.NewResponse(&agentsv1.CloseWorkspaceResponse{}), nil
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

type inboxService struct{ o Options }

func (s inboxService) ListAsks(
	ctx context.Context,
	req *connect.Request[agentsv1.ListAsksRequest],
) (*connect.Response[agentsv1.ListAsksResponse], error) {
	dir := s.o.AsksDir()

	open, err := asks.Open(dir)
	if err != nil {
		return nil, err
	}

	inbox := asks.Inbox{Open: open}

	out := &agentsv1.ListAsksResponse{}
	for _, a := range open {
		out.Open = append(out.Open, asks.Message(a))
	}

	if since := req.Msg.GetAnsweredSince(); since != nil {
		answered, err := asks.Answered(dir, since.AsTime())
		if err != nil {
			return nil, err
		}

		inbox.Answered = answered

		for _, a := range answered {
			out.Answered = append(out.Answered, asks.Message(a))
		}
	}

	if c := callerOf(req, req.Msg.GetCaller()); c.GetTerminal() && s.o.Links != nil {
		inbox.Links = s.o.Links(ctx, append(slices.Clone(inbox.Open), inbox.Answered...))
	}

	if out.View, err = json.Marshal(inbox); err != nil {
		return nil, err
	}

	return connect.NewResponse(out), nil
}

func (s inboxService) AnswerAsk(
	ctx context.Context,
	req *connect.Request[agentsv1.AnswerAskRequest],
) (*connect.Response[agentsv1.AnswerAskResponse], error) {
	// the inbox command's own call: its words, its output, its refusals
	if req.Msg.GetMatch() != "" || len(req.Msg.GetValues()) > 0 {
		return s.answerAsCommand(ctx, req)
	}

	dir := s.o.AsksDir()

	a, err := agents.FindKey(dir, req.Msg.GetId())
	if err != nil {
		if _, closed := errors.AsType[asks.ClosedError](err); closed {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// an ask no drive takes is delivered to its agent, so refuse before the
	// claim closes it when no agent can be reached
	if a.Drive == 0 {
		if err := agents.Agents(a.Worktree).Available(); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
	}

	o := printed.New(false)

	claimed, sent, err := agents.Answer(
		ctx,
		&o.Stdout,
		dir,
		a,
		valuesOf(req.Msg),
		cmp.Or(req.Msg.GetAnsweredBy(), asks.ByAPI),
		s.o.Deliverer,
	)

	switch {
	case err != nil && claimed.Task != "":
		// the answer is recorded and closes the ask; only moving it on failed
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("answer recorded, not delivered: %w", err))
	case err != nil:
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&agentsv1.AnswerAskResponse{
		Ask: asks.Message(claimed), Delivered: sent, Output: o.Stdout.String(),
	}), nil
}

// answerAsCommand answers the open ask match finds, else the one id keys,
// as the inbox command does: its refusals and what it prints read the same
// as in process.
func (s inboxService) answerAsCommand(
	ctx context.Context,
	req *connect.Request[agentsv1.AnswerAskRequest],
) (*connect.Response[agentsv1.AnswerAskResponse], error) {
	m := req.Msg
	c := callerOf(req, m.GetCaller())
	dir := s.o.AsksDir()

	find := func() (asks.Ask, error) { return agents.FindKey(dir, m.GetId()) }
	if m.GetMatch() != "" {
		find = func() (asks.Ask, error) { return agents.FindOpen(dir, m.GetMatch()) }
	}

	a, err := find()
	if err != nil {
		return nil, err
	}

	ctx = s.o.Session(ctx, c.GetEnv())
	o := printed.New(c.GetTerminal())

	claimed, sent, err := agents.Answer(
		ctx,
		&o.Stdout,
		dir,
		a,
		valuesOf(m),
		cmp.Or(m.GetAnsweredBy(), asks.ByAPI),
		s.o.Deliverer,
	)
	if err != nil {
		return nil, o.Wrap(err)
	}

	return connect.NewResponse(&agentsv1.AnswerAskResponse{
		Ask: asks.Message(claimed), Delivered: sent, Output: o.Stdout.String(),
	}), nil
}

// valuesOf is the request's answers as N=VALUE, as the inbox command takes them.
func valuesOf(m *agentsv1.AnswerAskRequest) []string {
	if len(m.GetValues()) > 0 {
		return m.GetValues()
	}

	values := make([]string, 0, len(m.GetAnswers()))
	for _, ans := range m.GetAnswers() {
		values = append(values, strconv.Itoa(int(ans.GetN()))+"="+ans.GetValue())
	}

	return values
}
