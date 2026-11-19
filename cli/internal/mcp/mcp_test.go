package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	"github.com/amberpixels/f10/cli/gen/agents/v1/agentsv1connect"
	"github.com/amberpixels/f10/cli/gen/mcptools"
	"github.com/amberpixels/f10/cli/internal/mcp"
	"github.com/amberpixels/f10/cli/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// TestEveryUnaryMethodIsATool fails when a method reaches the API without
// its generated tool: the generated file is stale, or the plugin skipped it.
func TestEveryUnaryMethodIsATool(t *testing.T) {
	have := map[string]bool{}
	for _, tool := range mcptools.Tools {
		have[tool.Procedure] = true
	}

	var methods int

	for _, pkg := range []protoreflect.FullName{"f10.v1", "agents.v1"} {
		protoregistry.GlobalFiles.RangeFilesByPackage(pkg, func(f protoreflect.FileDescriptor) bool {
			for i := range f.Services().Len() {
				svc := f.Services().Get(i)
				for j := range svc.Methods().Len() {
					m := svc.Methods().Get(j)
					if m.IsStreamingClient() || m.IsStreamingServer() {
						continue
					}

					methods++

					if proc := "/" + string(svc.FullName()) + "/" + string(m.Name()); !have[proc] {
						t.Errorf("%s has no MCP tool: run just proto::gen", proc)
					}
				}
			}

			return true
		})
	}

	if methods == 0 || methods != len(mcptools.Tools) {
		t.Errorf("%d unary methods, %d tools", methods, len(mcptools.Tools))
	}
}

// asksStub is an ask inbox of one open ask, answered once.
type asksStub struct {
	agentsv1connect.UnimplementedInboxServiceHandler

	answered []*agentsv1.Answer
}

const askID = "FT-1-ask"

func (a *asksStub) ListAsks(
	context.Context, *connect.Request[agentsv1.ListAsksRequest],
) (*connect.Response[agentsv1.ListAsksResponse], error) {
	res := &agentsv1.ListAsksResponse{}
	if a.answered == nil {
		res.Open = []*agentsv1.Ask{{Id: askID, Text: "1. Merge now? [yes | wait]"}}
	}

	return connect.NewResponse(res), nil
}

func (a *asksStub) AnswerAsk(
	_ context.Context, req *connect.Request[agentsv1.AnswerAskRequest],
) (*connect.Response[agentsv1.AnswerAskResponse], error) {
	if req.Msg.GetId() != askID || a.answered != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no open ask "+req.Msg.GetId()))
	}

	a.answered = req.Msg.GetAnswers()

	return connect.NewResponse(&agentsv1.AnswerAskResponse{Ask: &agentsv1.Ask{
		Id: askID, AnsweredBy: agentsv1.AnswerSource_ANSWER_SOURCE_API,
	}}), nil
}

// connectTo is a client session on a server over stub, offering the
// procedures allow names.
func connectTo(t *testing.T, stub *asksStub, allow ...string) *sdk.ClientSession {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(agentsv1connect.NewInboxServiceHandler(stub))

	server := mcp.New(mcp.Options{
		Allow:   func(p string) bool { return slices.Contains(allow, p) },
		HTTP:    mcp.InProcess(mux, ""),
		BaseURL: "http://f10-daemon",
	})

	st, ct := sdk.NewInMemoryTransports()

	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ss.Close() })

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}

	return b.String()
}

func TestToolsAnswerAnOpenAsk(t *testing.T) {
	stub := &asksStub{}
	cs := connectTo(
		t,
		stub,
		agentsv1connect.InboxServiceListAsksProcedure,
		agentsv1connect.InboxServiceAnswerAskProcedure,
	)

	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}

	slices.Sort(names)

	if !slices.Equal(names, []string{"answer_ask", "list_asks"}) {
		t.Fatalf("tools = %v, want only the allowed list_asks and answer_ask", names)
	}

	open, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_asks"})
	if err != nil || open.IsError || !strings.Contains(text(open), askID) {
		t.Fatalf("list_asks = %v %v, want the open ask %s", text(open), err, askID)
	}

	var structured map[string]any
	if raw, _ := json.Marshal(
		open.StructuredContent,
	); json.Unmarshal(raw, &structured) != nil ||
		structured["open"] == nil {
		t.Errorf("structured content = %v, want the response with proto names", open.StructuredContent)
	}

	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "answer_ask",
		Arguments: map[string]any{"id": askID, "answers": []any{map[string]any{"n": 2, "value": "8080"}}},
	})
	if err != nil || res.IsError || !strings.Contains(text(res), `"answered_by":"ANSWER_SOURCE_API"`) {
		t.Fatalf("answer_ask = %v %v, want the ask answered through the API", text(res), err)
	}

	if len(stub.answered) != 1 || stub.answered[0].GetN() != 2 || stub.answered[0].GetValue() != "8080" {
		t.Errorf("the API got answers %v, want question 2 answered 8080", stub.answered)
	}

	again, err := cs.CallTool(
		t.Context(),
		&sdk.CallToolParams{Name: "answer_ask", Arguments: map[string]any{"id": askID}},
	)
	if err != nil || !again.IsError || !strings.HasPrefix(text(again), "failed_precondition: ") {
		t.Errorf("a second answer = %v %v, want a failed_precondition tool error", text(again), err)
	}
}

func TestToolsRefuseBadArgumentsAndUnofferedTools(t *testing.T) {
	cs := connectTo(t, &asksStub{}, agentsv1connect.InboxServiceAnswerAskProcedure)

	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "answer_ask", Arguments: map[string]any{"idd": "x"}})
	if err != nil || !res.IsError || !strings.Contains(text(res), "the arguments do not fit answer_ask") {
		t.Errorf("a misspelled field = %v %v, want a tool error naming the tool", text(res), err)
	}

	if _, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_asks"}); err == nil {
		t.Error("a tool outside Allow was called")
	}
}
