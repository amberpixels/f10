package agentsrpc_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/agents/agentsrpc"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

type clients struct {
	agents agentsv1connect.AgentServiceClient
	asks   agentsv1connect.AskServiceClient
}

func serveAgents(t *testing.T, o agentsrpc.Options) clients {
	t.Helper()

	mux := http.NewServeMux()
	agentsrpc.Mount(mux, o)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return clients{
		agents: agentsv1connect.NewAgentServiceClient(srv.Client(), srv.URL),
		asks:   agentsv1connect.NewAskServiceClient(srv.Client(), srv.URL),
	}
}

func TestAgentServiceReadsAndPromptsTheBackend(t *testing.T) {
	m := testkit.UseAgents(t)
	m.Spaces = []agentctl.Workspace{{ID: "w1", Label: "GH-1", Path: "/code/repo.GH-1", Status: agentctl.StatusWorking}}
	m.Started = []string{"gh-1-abc"}

	c := serveAgents(t, agentsrpc.Options{})

	res, err := c.agents.ListWorkspaces(t.Context(), connect.NewRequest(&agentsv1.ListWorkspacesRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	if ws := res.Msg.GetWorkspaces(); len(ws) != 1 || ws[0].GetPath() != "/code/repo.GH-1" ||
		ws[0].GetAgentStatus() != agentsv1.AgentStatus_AGENT_STATUS_WORKING {
		t.Errorf("workspaces = %v", ws)
	}

	if _, err := c.agents.SendText(t.Context(), connect.NewRequest(&agentsv1.SendTextRequest{
		Name: "gh-1-abc", Text: "/f10:ship GH-1", Submit: true,
	})); err != nil {
		t.Fatal(err)
	}

	if got := m.Prompts["gh-1-abc"]; len(got) != 1 || got[0] != "/f10:ship GH-1" {
		t.Errorf("prompts = %v", got)
	}

	_, err = c.agents.SendText(t.Context(), connect.NewRequest(&agentsv1.SendTextRequest{
		Name: "nobody", Text: "hi", Submit: true,
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an unknown agent = %v, want NotFound", err)
	}

	// the memory backend lists no agents, which the API says rather than
	// answering an empty list
	_, err = c.agents.ListAgents(t.Context(), connect.NewRequest(&agentsv1.ListAgentsRequest{}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("ListAgents on a backend with no listing = %v, want Unimplemented", err)
	}
}

func TestAskServiceAnswersThroughTheDeliverer(t *testing.T) {
	dir := t.TempDir()
	a := testkit.Raised(t, dir, asks.Ask{Task: "FT-1", Root: "/r", Project: "r", Drive: 3})

	var resumed []int

	c := serveAgents(t, agentsrpc.Options{
		AsksDir: func() string { return dir },
		Deliverer: agents.Deliverer{
			Live: func(asks.Ask) bool { return false },
			Resume: func(_ context.Context, _ io.Writer, a asks.Ask) error {
				resumed = append(resumed, a.Drive)

				return nil
			},
		},
	})

	list, err := c.asks.ListAsks(t.Context(), connect.NewRequest(&agentsv1.ListAsksRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	open := list.Msg.GetOpen()
	if len(open) != 1 || open[0].GetId() != a.Key() || open[0].GetLabels()["task"] != "FT-1" ||
		open[0].GetLabels()["drive"] != "3" || len(open[0].GetQuestions()) != 2 {
		t.Fatalf("open asks = %v", open)
	}

	res, err := c.asks.AnswerAsk(t.Context(), connect.NewRequest(&agentsv1.AnswerAskRequest{
		Id:      a.Key(),
		Answers: []*agentsv1.Answer{{N: 2, Value: "8080"}},
	}))
	if err != nil {
		t.Fatal(err)
	}

	if got := res.Msg.GetAsk(); got.GetAnsweredBy() != agentsv1.AnswerSource_ANSWER_SOURCE_API ||
		got.GetAnswer() == "" || len(resumed) != 1 || resumed[0] != 3 {
		t.Errorf("answered %v, resumed %v; want the API's answer and drive 3 resumed", got, resumed)
	}

	_, err = c.asks.AnswerAsk(t.Context(), connect.NewRequest(&agentsv1.AnswerAskRequest{Id: a.Key()}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a second answer = %v, want FailedPrecondition", err)
	}
}
