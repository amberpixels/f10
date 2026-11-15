//go:build unix

package daemon

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// daemonService serves DaemonService over the server itself, which the
// daemon owns whatever API the rest of the socket serves.
type daemonService struct{ sup *Server }

func (d *daemonService) Hello(
	_ context.Context,
	_ *connect.Request[f10v1.HelloRequest],
) (*connect.Response[f10v1.HelloResponse], error) {
	info := d.sup.Info()

	return connect.NewResponse(&f10v1.HelloResponse{
		Version:    info.Version,
		ApiVersion: int32(info.API), //nolint:gosec // a small constant
		Pid:        int32(info.PID), //nolint:gosec // a process id
		StartedAt:  timestamppb.New(info.Started),
		Stopping:   info.Stopping,
	}), nil
}

func (d *daemonService) GetDaemonStatus(
	_ context.Context,
	_ *connect.Request[f10v1.GetDaemonStatusRequest],
) (*connect.Response[f10v1.GetDaemonStatusResponse], error) {
	st := d.sup.Status()
	out := &f10v1.GetDaemonStatusResponse{}

	for _, r := range st.Runs {
		out.Drives = append(out.Drives, &f10v1.SupervisedDrive{
			Root:  r.Root,
			Dir:   r.Dir,
			Drive: int32(r.Drive), //nolint:gosec // a drive number
			Pid:   int32(r.PID),   //nolint:gosec // a process id
			Since: timestamppb.New(r.Since),
			Log:   r.Log,
		})
	}

	for _, w := range st.Watches {
		wp := &f10v1.WatchedProject{
			Root: w.Root,
			Dir:  w.Dir,
			Exit: int32(w.Exit), //nolint:gosec // an exit code
			Log:  w.Log,
		}
		if !w.Last.IsZero() {
			wp.LastPassAt = timestamppb.New(w.Last)
		}

		out.Watches = append(out.Watches, wp)
	}

	return connect.NewResponse(out), nil
}

func (d *daemonService) StopDaemon(
	_ context.Context,
	req *connect.Request[f10v1.StopDaemonRequest],
) (*connect.Response[f10v1.StopDaemonResponse], error) {
	d.sup.Stop(req.Msg.GetDrain(), req.Msg.GetUpgrade())

	return connect.NewResponse(&f10v1.StopDaemonResponse{}), nil
}

func (d *daemonService) WatchProject(
	_ context.Context,
	req *connect.Request[f10v1.WatchProjectRequest],
) (*connect.Response[f10v1.WatchProjectResponse], error) {
	m := req.Msg

	log, err := d.sup.WatchProject(m.GetRoot(), m.GetDir(), LocalEnv(req, m.GetEnv()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&f10v1.WatchProjectResponse{Log: log}), nil
}
