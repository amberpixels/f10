package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/daemon"
)

// errorOptions maps every handler's errors onto Connect codes in one place.
func errorOptions() []connect.HandlerOption {
	return []connect.HandlerOption{connect.WithInterceptors(connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				res, err := next(ctx, req)

				return res, codeOf(err)
			}
		},
	))}
}

func codeOf(err error) error {
	var ce *connect.Error

	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		return err
	case errors.Is(err, daemon.ErrStopping):
		return connect.NewError(connect.CodeUnavailable, err)
	case agents.IsOutside(err):
		return connect.NewError(connect.CodeFailedPrecondition, agents.ErrNoSession)
	}

	return connect.NewError(connect.CodeUnknown, err)
}

func invalid(format string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(format))
}
