package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/daemon"
)

// envRuns records the environment a Start call would run under.
type envRuns struct {
	f10v1connect.UnimplementedRunServiceHandler

	env  []string
	seen bool
}

func (r *envRuns) Start(
	_ context.Context,
	req *connect.Request[f10v1.StartRequest],
) (*connect.Response[f10v1.StartResponse], error) {
	r.env, r.seen = daemon.LocalEnv(req, req.Msg.GetCaller().GetEnv()), true

	return connect.NewResponse(&f10v1.StartResponse{}), nil
}

func mcpSession(t *testing.T, url, token string) (*sdk.ClientSession, error) {
	t.Helper()

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).
		Connect(t.Context(), &sdk.StreamableClientTransport{
			Endpoint:   url + MCPPath,
			HTTPClient: &http.Client{Transport: bearer{token}},
			MaxRetries: -1,
		}, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}

	return cs, err
}

func TestListenerServesMCPByTokenScope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := WriteConfig(dir, Config{Enabled: true, MCP: true}); err != nil {
		t.Fatal(err)
	}

	l := &Listener{Dir: dir}

	reader, err := l.storeOf().Add("reader", []Scope{ScopeRead}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	starter, err := l.storeOf().Add("starter", []Scope{ScopeStart}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	runs := &envRuns{}
	mux := http.NewServeMux()
	mux.Handle(f10v1connect.NewRunServiceHandler(runs))

	srv := httptest.NewServer(l.Handler(mux))
	t.Cleanup(srv.Close)

	if _, err := mcpSession(t, srv.URL, ""); err == nil {
		t.Error("an MCP session opened with no token")
	}

	cs, err := mcpSession(t, srv.URL, reader)
	if err != nil {
		t.Fatal(err)
	}

	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}

	slices.Sort(names)

	want := []string{
		"get_agent_status", "get_artifact", "get_board", "get_daemon_status", "get_run_status",
		"get_task", "list_agents", "list_artifacts", "list_asks", "list_batches", "list_projects", "list_workspaces",
	}
	if !slices.Equal(names, want) {
		t.Errorf("a read token's tools = %v, want %v", names, want)
	}

	if _, err := cs.CallTool(
		t.Context(),
		&sdk.CallToolParams{Name: "answer_ask", Arguments: map[string]any{"id": "x"}},
	); err == nil {
		t.Error("a read token called answer_ask")
	}

	cs, err = mcpSession(t, srv.URL, starter)
	if err != nil {
		t.Fatal(err)
	}

	// StartBatch stays an rpc for a local client and is no tool
	if _, err := cs.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "start_batch", Arguments: map[string]any{"project": "p"},
	}); err == nil {
		t.Error("start_batch is still a tool")
	}

	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "start",
		Arguments: map[string]any{
			"project": "p", "token": "FT-1", "caller": map[string]any{"env": []string{"HERDR_SOCKET=/tmp/x"}},
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("start = %v %v", res, err)
	}

	if !runs.seen || runs.env != nil {
		t.Errorf("a batch through MCP ran with env %v, want none: a tailnet caller has no local session", runs.env)
	}
}

func TestListenerRefusesMCPWhileTheSettingIsOff(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := WriteConfig(dir, Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	l := &Listener{Dir: dir}

	full, err := l.storeOf().Add("full", Scopes, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(l.Handler(http.NewServeMux()))
	t.Cleanup(srv.Close)

	_, err = mcpSession(t, srv.URL, full)
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("MCP with the setting off = %v, want /mcp not served", err)
	}
}
