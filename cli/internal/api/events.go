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
)

// EventPoll is how often a stream reads the ledger again once its backlog is
// sent. The ledger is SQLite written by many processes, so nothing in this
// one can say a row landed. A variable so tests need not wait.
var EventPoll = time.Second

type eventService struct{}

// WatchEvents streams the ledger's rows inserted after the request's
// cursor, then every row as it lands, until the client goes.
func (eventService) WatchEvents(
	ctx context.Context,
	req *connect.Request[f10v1.WatchEventsRequest],
	stream *connect.ServerStream[f10v1.WatchEventsResponse],
) error {
	m := req.Msg
	after := m.GetAfter()

	for {
		placed, err := ledger.ReadAfter(ctx, ledger.Path(), after, m.GetProject(), m.GetTask())
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}

		for _, p := range placed {
			ev, err := EventOf(p.Event)
			if err != nil {
				return err
			}

			if err := stream.Send(&f10v1.WatchEventsResponse{Event: ev, Cursor: p.Cursor}); err != nil {
				return err
			}

			after = p.Cursor
		}

		// a full page means more rows wait: read on before polling
		if len(placed) == ledger.AfterPage {
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(EventPoll):
		}
	}
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
