package api

import (
	"context"
	"encoding/json"
	"errors"
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
	c := callerOf(req, m.GetCaller())
	stateView := m.GetView() == f10v1.BoardView_BOARD_VIEW_STATE
	now := time.Now()

	span, err := boardSpan(m, c, stateView, now)
	if err != nil {
		return nil, err
	}

	cfg, cfgPath, err := scope.ReadBoardConfig()
	if err != nil {
		return nil, err
	}

	// one pipeline whatever the scope: a checkout alone is a scope of one
	var entries []scope.ScopeEntry

	if len(m.GetCheckouts())+len(m.GetGroups()) > 0 || m.GetAll() {
		items, err := scope.ScopeItemsOf(
			m.GetCheckouts(),
			m.GetGroups(),
			m.GetAll(),
			scope.BoardConfigFile,
			cfg.Repos,
			cfgPath,
		)
		if err != nil {
			return nil, err
		}

		if entries = scope.BindScope(ctx, items, "board"); len(entries) == 0 {
			return nil, errors.New("the scope names no project: the groups and roots given matched none")
		}
	} else {
		t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "board")
		if err != nil {
			return nil, err
		}

		entries = []scope.ScopeEntry{{Dir: t.Dir, Target: t}}
	}

	o := output(c)
	boards := board.Collect(ctx, entries, &o.Stderr, now, span, stateView, m.GetRemote(), board.People(cfg.People))

	pb, err := boardOf(boards[0])
	if err != nil {
		return nil, err
	}

	view, err := json.Marshal(boards)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.GetBoardResponse{Board: pb, View: view, Printed: printedOf(o)}), nil
}

// boardSpan is the window a board reads: one named as f10 board names it,
// read against the user's workdays, else since, else today or the last
// working day.
func boardSpan(m *f10v1.GetBoardRequest, c *f10v1.CallerContext, stateView bool, now time.Time) (board.Span, error) {
	if since := m.GetSince(); since != nil && m.GetWindow() == "" && c == nil {
		span := board.Span{
			Named: &lookaround.Window{Since: since.AsTime(), Chosen: "since " + since.AsTime().Format(time.DateTime)},
		}
		if stateView {
			span.Named = &lookaround.Window{Since: since.AsTime()}
		}

		return span, nil
	}

	span, err := board.ResolveSpan(m.GetWindow(), stateView, now, scope.ReadWorkdays)
	if err != nil && c == nil {
		return span, invalid(err.Error())
	}

	return span, err
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
