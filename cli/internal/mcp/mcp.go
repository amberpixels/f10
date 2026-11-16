// Package mcp serves the API's methods as MCP tools: one tool per unary
// method, generated from the proto schema into gen/mcptools. It knows
// nothing of tokens; the caller says which procedures a client may reach.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/amberpixels/f10/cli/gen/mcptools"
)

// Options says which tools a server offers and where their calls go.
type Options struct {
	// Allow reports whether a procedure is offered as a tool; nil offers none.
	Allow func(procedure string) bool
	// HTTP and BaseURL are where every call goes.
	HTTP    connect.HTTPClient
	BaseURL string
	// Before runs ahead of each call; an error ends the call with it.
	Before  func(ctx context.Context) error
	Version string
}

// New is an MCP server offering the tools Allow admits.
func New(o Options) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "f10", Version: o.Version}, nil)

	for _, t := range mcptools.Tools {
		if o.Allow == nil || !o.Allow(t.Procedure) {
			continue
		}

		s.AddTool(&sdk.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(t.InputSchema),
		}, o.handler(t))
	}

	return s
}

var (
	unmarshal = protojson.UnmarshalOptions{}
	marshal   = protojson.MarshalOptions{UseProtoNames: true}
)

// handler reads the arguments into the request message, calls the method,
// and returns the response as JSON. A refused call is a tool error, not a
// protocol one, so the model reads why and can correct the call.
func (o Options) handler(t mcptools.Tool) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		args := []byte(req.Params.Arguments)
		if len(args) == 0 || string(args) == "null" {
			args = []byte("{}")
		}

		in := t.NewRequest()
		if err := unmarshal.Unmarshal(args, in); err != nil {
			return failed(fmt.Errorf("the arguments do not fit %s: %w", t.Name, err)), nil
		}

		dropCaller(in.ProtoReflect())

		if o.Before != nil {
			if err := o.Before(ctx); err != nil {
				return failed(err), nil
			}
		}

		out, err := t.Call(ctx, o.HTTP, o.BaseURL, in)
		if err != nil {
			return failed(err), nil
		}

		raw, err := marshal.Marshal(out)
		if err != nil {
			return nil, err
		}

		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: string(raw)}},
			StructuredContent: json.RawMessage(raw),
		}, nil
	}
}

// dropCaller clears the caller's context a model passed anyway: the tool's
// schema leaves it out, and its calls reach the unix socket, which would
// otherwise honor a checkout and an environment the model made up.
func dropCaller(m protoreflect.Message) {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		if f := fields.Get(i); f.Message() != nil && f.Message().Name() == "CallerContext" {
			m.Clear(f)
		}
	}
}

func failed(err error) *sdk.CallToolResult {
	text := err.Error()

	if ce := new(connect.Error); errors.As(err, &ce) {
		text = fmt.Sprintf("%s: %s", ce.Code(), ce.Message())
	}

	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// InProcess is a client that serves each call on h in this process. peer
// becomes the call's remote address, so a handler still tells a tailnet
// caller from the unix socket.
func InProcess(h http.Handler, peer string) connect.HTTPClient {
	return inProcess{h: h, peer: peer}
}

type inProcess struct {
	h    http.Handler
	peer string
}

func (c inProcess) Do(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.RemoteAddr = c.peer
	r.RequestURI = r.URL.RequestURI()

	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)

	res := rec.Result()
	res.Request = r

	return res, nil
}
