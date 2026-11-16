package api

import (
	"context"
	"errors"
	"strconv"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/printed"
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

// codeOf is err as a Connect error, with the exit code and the output a
// command printed before it failed, when it has them.
func codeOf(err error) error {
	if err == nil {
		return nil
	}

	ce := connectOf(err)

	if ec, ok := errors.AsType[cli.ExitCoder](err); ok && ec.ExitCode() != 0 {
		ce.Meta().Set(printed.ExitHeader, strconv.Itoa(ec.ExitCode()))
	}

	if agents.IsOutside(err) {
		ce.Meta().Set(printed.KindHeader, printed.KindOutside)
	}

	if pe, ok := printed.Of(err); ok {
		if d, derr := connect.NewErrorDetail(&f10v1.Printed{Stdout: pe.Stdout, Stderr: pe.Stderr}); derr == nil {
			ce.AddDetail(d)
		}
	}

	return ce
}

func connectOf(err error) *connect.Error {
	if ce, ok := errors.AsType[*connect.Error](err); ok {
		return ce
	}

	switch {
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
