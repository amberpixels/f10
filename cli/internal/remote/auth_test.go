package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
)

// Every method of both API packages has a decision: a scope, any token, or
// the socket only. A new RPC fails here until it is placed.
func TestEveryProcedureHasAScope(t *testing.T) {
	t.Parallel()

	// the generated packages register their files when imported
	_ = f10v1.File_f10_v1_daemon_proto
	_ = agentsv1.File_agents_v1_asks_proto

	seen := map[string]bool{}

	for _, pkg := range []protoreflect.FullName{"f10.v1", "agents.v1"} {
		protoregistry.GlobalFiles.RangeFilesByPackage(pkg, func(fd protoreflect.FileDescriptor) bool {
			for i := range fd.Services().Len() {
				svc := fd.Services().Get(i)
				for j := range svc.Methods().Len() {
					proc := "/" + string(svc.FullName()) + "/" + string(svc.Methods().Get(j).Name())
					seen[proc] = true

					_, scoped := procedures[proc]
					if !scoped && !socketOnly[proc] {
						t.Errorf("%s has no scope and is not socket only", proc)
					}
				}
			}

			return true
		})
	}

	if len(seen) == 0 {
		t.Fatal("no services registered: the generated packages did not load")
	}

	for proc := range procedures {
		if !seen[proc] {
			t.Errorf("the scope table names %s, which no service has", proc)
		}
	}
}

// bearer adds a token to every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}

	return http.DefaultTransport.RoundTrip(r)
}

func TestAuthChecksTokenAndScope(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())

	reader, err := store.Add("reader", []Scope{ScopeRead}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	full, err := store.Add("full", Scopes, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// unimplemented handlers: a call the check lets through answers
	// Unimplemented, one it refuses answers its own code
	mux := http.NewServeMux()
	mux.Handle(f10v1connect.NewTaskServiceHandler(f10v1connect.UnimplementedTaskServiceHandler{}))
	mux.Handle(f10v1connect.NewDaemonServiceHandler(f10v1connect.UnimplementedDaemonServiceHandler{}))

	srv := httptest.NewServer(Auth(store, mux))
	t.Cleanup(srv.Close)

	tasks := func(token string) f10v1connect.TaskServiceClient {
		return f10v1connect.NewTaskServiceClient(&http.Client{Transport: bearer{token}}, srv.URL)
	}
	daemons := func(token string) f10v1connect.DaemonServiceClient {
		return f10v1connect.NewDaemonServiceClient(&http.Client{Transport: bearer{token}}, srv.URL)
	}

	ctx := context.Background()

	for _, c := range []struct {
		name string
		call func() error
		code connect.Code
		says string
	}{
		{"no token", func() error {
			_, err := tasks("").GetTask(ctx, connect.NewRequest(&f10v1.GetTaskRequest{}))
			return err
		}, connect.CodeUnauthenticated, "device token"},
		{"unknown token", func() error {
			_, err := tasks("f10_nope").GetTask(ctx, connect.NewRequest(&f10v1.GetTaskRequest{}))
			return err
		}, connect.CodeUnauthenticated, "unknown or revoked"},
		{"read token reads", func() error {
			_, err := tasks(reader).GetTask(ctx, connect.NewRequest(&f10v1.GetTaskRequest{}))
			return err
		}, connect.CodeUnimplemented, ""},
		{"read token creates", func() error {
			_, err := tasks(reader).CreateTask(ctx, connect.NewRequest(&f10v1.CreateTaskRequest{}))
			return err
		}, connect.CodePermissionDenied, "lacks the start scope"},
		{"any token says hello", func() error {
			_, err := daemons(reader).Hello(ctx, connect.NewRequest(&f10v1.HelloRequest{}))
			return err
		}, connect.CodeUnimplemented, ""},
		{"stop is socket only", func() error {
			_, err := daemons(full).StopDaemon(ctx, connect.NewRequest(&f10v1.StopDaemonRequest{}))
			return err
		}, connect.CodePermissionDenied, "unix socket"},
	} {
		err := c.call()
		if got := connect.CodeOf(err); got != c.code || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v (%s), want %s saying %q", c.name, err, got, c.code, c.says)
		}
	}
}
