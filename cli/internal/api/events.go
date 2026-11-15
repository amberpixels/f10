package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
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

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(EventPoll):
		}
	}
}

// EventOf is a ledger row as f10.v1 carries it. The payload goes through its
// stored JSON into the kind's message, whose fields are the stored keys; a
// kind with no message arrives with kind set and no payload.
//
//nolint:lostfield // the row's machine, source and hot keys stay off the API
func EventOf(e ledger.Event) (*f10v1.Event, error) {
	obj := map[string]any{
		"id": e.ID, "kind": e.Kind, "project": e.Project, "task": e.Task,
		"session": e.Session, "checkout": e.Checkout, "branch": e.Branch,
	}
	if !e.At.IsZero() {
		obj["at"] = e.At.UTC().Format(time.RFC3339Nano)
	}

	if field := payloadField(e.Kind); field != "" {
		raw, err := json.Marshal(e.Payload.Val)
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", e.ID, err)
		}

		obj[field] = json.RawMessage(raw)
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	if ev, ok := decodeEvent(raw); ok {
		return ev, nil
	}

	// a stored row this message cannot read still arrives, without its payload
	return &f10v1.Event{
		Id: e.ID, Kind: e.Kind, Project: e.Project, Task: e.Task, At: timestamppb.New(e.At),
		Session: e.Session, Checkout: e.Checkout, Branch: e.Branch,
	}, nil
}

func decodeEvent(raw []byte) (*f10v1.Event, bool) {
	var ev f10v1.Event

	return &ev, (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &ev) == nil
}

// payloadField is the Event oneof case that carries kind, "" for none: the
// state_* kinds share claim, and every other case is named after its kind.
func payloadField(kind string) string {
	name := kind
	if strings.HasPrefix(kind, "state_") {
		name = "claim"
	}

	fd := (&f10v1.Event{}).ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if fd == nil || fd.ContainingOneof() == nil {
		return ""
	}

	return name
}
