package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/target"
)

// EventPoll is how often a stream reads the ledger again once its backlog is
// sent. The ledger is SQLite written by many processes, so nothing in this
// one can say a row landed; an idle read costs a stat and one indexed query.
// A variable so tests need not wait.
var EventPoll = 250 * time.Millisecond

type eventService struct{}

// WatchEvents streams the ledger's rows inserted after the request's
// cursor, then every row as it lands, until the client goes. Without a
// cursor it starts at the end, and its first response, with no event, says
// where that is, so a client that drops before any event can still resume.
func (eventService) WatchEvents(
	ctx context.Context,
	req *connect.Request[f10v1.WatchEventsRequest],
	stream *connect.ServerStream[f10v1.WatchEventsResponse],
) error {
	m := req.Msg

	for _, k := range m.GetKinds() {
		if !ledger.Known(k) {
			return invalid(fmt.Sprintf("kind %q is not a ledger kind", k))
		}
	}

	tail := ledger.OpenTail(ledger.Path())
	defer tail.Close()

	after := m.GetAfter()
	if after == "" {
		end, err := tail.End(ctx)
		if err != nil {
			return err
		}

		if err := stream.Send(&f10v1.WatchEventsResponse{Cursor: end}); err != nil {
			return err
		}

		after = end
	}

	n := ledger.Narrow{Project: m.GetProject(), Task: m.GetTask(), Kinds: m.GetKinds()}
	if n.Project != "" {
		n.Orphan = asksIn(ctx, n.Project)
	}

	for {
		page, err := tail.After(ctx, after, n)
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}

		for _, p := range page.Rows {
			ev, err := EventOf(p.Event)
			if err != nil {
				return err
			}

			if err := stream.Send(&f10v1.WatchEventsResponse{Event: ev, Cursor: p.Cursor}); err != nil {
				return err
			}
		}

		after = page.Next

		// a full page means more rows wait: read on before polling
		if page.Full {
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(EventPoll):
		}
	}
}

// asksIn matches an ask row, which has no project column, to project by its
// main label: the ask's main checkout, whose project is read once per stream.
func asksIn(ctx context.Context, project string) func(ledger.Event) bool {
	seen := map[string]string{}

	return func(e ledger.Event) bool {
		main := askMain(e)
		if main == "" {
			return false
		}

		p, ok := seen[main]
		if !ok {
			p = target.ProjectOf(ctx, main)
			seen[main] = p
		}

		return p == project
	}
}

// askMain is an ask row's main label, "" for any other row or one that does
// not decode.
func askMain(e ledger.Event) string {
	var labels map[string]string

	switch e.Kind {
	case ledger.AskRaised:
		p, err := ledger.Decode[*ledger.AskRaisedPayload](e)
		if err != nil && !errors.Is(err, ledger.ErrNewerVersion) {
			return ""
		}

		labels = p.GetAsk().GetLabels()
	case ledger.AskAnswered:
		p, err := ledger.Decode[*ledger.AskAnsweredPayload](e)
		if err != nil && !errors.Is(err, ledger.ErrNewerVersion) {
			return ""
		}

		labels = p.GetLabels()
	}

	return labels["main"]
}

// EventOf is a ledger row as f10.v1 carries it, its payload the kind's
// message as the ledger decodes it. A kind with no payload case, or a row
// that does not decode, arrives with kind set and no payload; a row a newer
// f10 wrote arrives with what this one read of it.
//
//nolint:lostfield // the row's machine, source and hot keys stay off the API
func EventOf(e ledger.Event) (*f10v1.Event, error) {
	ev := &f10v1.Event{
		Id: e.ID, Kind: e.Kind, Project: e.Project, Task: e.Task,
		Session: e.Session, Checkout: e.Checkout, Branch: e.Branch,
	}
	if !e.At.IsZero() {
		ev.At = timestamppb.New(e.At)
	}

	fd := payloadField(e.Kind)
	if fd == nil {
		return ev, nil
	}

	m, err := ledger.DecodeMessage(e)
	if err != nil && !errors.Is(err, ledger.ErrNewerVersion) {
		return ev, nil
	}

	// a case whose message is not the kind's means the two drifted apart
	if m.ProtoReflect().Descriptor() != fd.Message() {
		return nil, fmt.Errorf("event %s: %s decodes to %s, not the %s case's %s",
			e.ID, e.Kind, m.ProtoReflect().Descriptor().FullName(), fd.Name(), fd.Message().FullName())
	}

	ev.ProtoReflect().Set(fd, protoreflect.ValueOfMessage(m.ProtoReflect()))

	return ev, nil
}

// payloadField is the Event oneof case that carries kind, nil for none: the
// state_* kinds share claim, and every other case is named after its kind.
func payloadField(kind string) protoreflect.FieldDescriptor {
	name := kind
	if strings.HasPrefix(kind, "state_") {
		name = "claim"
	}

	fd := (&f10v1.Event{}).ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if fd == nil || fd.ContainingOneof() == nil {
		return nil
	}

	return fd
}
