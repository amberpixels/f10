package api

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/board"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/scope"
)

type boardService struct{ sup Supervisor }

func (s *boardService) GetBoard(
	ctx context.Context,
	req *connect.Request[f10v1.GetBoardRequest],
) (*connect.Response[f10v1.GetBoardResponse], error) {
	m := req.Msg
	stateView := m.GetView() == f10v1.BoardView_BOARD_VIEW_STATE
	now := time.Now()

	t, err := projectTarget(ctx, s.sup, m.GetProject(), "board")
	if err != nil {
		return nil, err
	}

	var span board.Span

	if since := m.GetSince(); since != nil {
		span.Named = &lookaround.Window{Since: since.AsTime(), Chosen: "since " + since.AsTime().Format(time.DateTime)}
		if stateView {
			span.Named = &lookaround.Window{Since: since.AsTime()}
		}
	} else if span, err = board.ResolveSpan("", stateView, now, scope.ReadWorkdays); err != nil {
		return nil, invalid(err.Error())
	}

	cfg, _, err := scope.ReadBoardConfig()
	if err != nil {
		return nil, err
	}

	entries := []scope.ScopeEntry{{Dir: t.Dir, Target: t}}
	b := board.Collect(ctx, entries, io.Discard, now, span, stateView, m.GetRemote(), board.People(cfg.People))[0]

	pb, err := boardOf(b)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.GetBoardResponse{Board: pb}), nil
}

// boardOf is the board as f10 board --json prints it, which the Board
// message follows key for key, read into the message; the view word
// becomes its enum.
//
//nolint:lostfield // every field goes through the board's JSON, which the message follows key for key
func boardOf(b lookaround.Board) (*f10v1.Board, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}

	switch b.View {
	case lookaround.ViewState:
		obj["view"] = f10v1.BoardView_BOARD_VIEW_STATE.String()
	case lookaround.ViewFeed:
		obj["view"] = f10v1.BoardView_BOARD_VIEW_CHANGES.String()
	default:
		delete(obj, "view")
	}

	if raw, err = json.Marshal(obj); err != nil {
		return nil, err
	}

	var out f10v1.Board
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &out); err != nil {
		return nil, err
	}

	return &out, nil
}
