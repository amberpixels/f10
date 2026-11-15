package api

import (
	"context"
	"io"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/taskwrite"
)

type taskService struct{ sup Supervisor }

func (s *taskService) CreateTask(
	ctx context.Context,
	req *connect.Request[f10v1.CreateTaskRequest],
) (*connect.Response[f10v1.CreateTaskResponse], error) {
	m := req.Msg

	if strings.TrimSpace(m.GetTitle()) == "" {
		return nil, invalid("a task needs a title")
	}

	t, err := projectTarget(ctx, s.sup, m.GetProject(), "task")
	if err != nil {
		return nil, err
	}

	p := hostops.Props{Status: m.GetStatus(), Priority: m.GetPriority(), Labels: m.GetLabels()}
	for k, v := range m.GetProperties() {
		p.Set = append(p.Set, k+"="+v)
	}

	doc := "# " + strings.TrimSpace(m.GetTitle()) + "\n\n" + m.GetBody()

	r, url, err := taskwrite.Create(ctx, io.Discard, t, taskwrite.New{Doc: doc, Props: p, Parent: m.GetParent()})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.CreateTaskResponse{Task: &f10v1.TaskRef{
		Id:    r.ID,
		Title: taskwrite.DocTitle(r.ID, doc),
		Url:   url,
	}}), nil
}

func (s *taskService) GetTask(
	ctx context.Context,
	req *connect.Request[f10v1.GetTaskRequest],
) (*connect.Response[f10v1.GetTaskResponse], error) {
	m := req.Msg

	t, err := projectTarget(ctx, s.sup, m.GetProject(), "task")
	if err != nil {
		return nil, err
	}

	r, err := t.Reference(ctx, m.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	doc, err := taskwrite.Read(ctx, t, r)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.GetTaskResponse{Task: taskOf(r, doc)}), nil
}

// taskOf is the task as read: its ref, and the meta lines task read prints
// under the title (`OPEN · created 2026-11-15 · <url>`), parsed back.
func taskOf(r ref.Ref, doc string) *f10v1.Task {
	task := &f10v1.Task{Ref: &f10v1.TaskRef{Id: r.ID, Title: taskwrite.DocTitle(r.ID, doc)}, Markdown: doc}

	for line := range strings.SplitSeq(doc, "\n") {
		parts := strings.Split(line, " · ")
		if len(parts) < 2 {
			continue
		}

		task.State = strings.TrimSpace(parts[0])

		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)

			switch {
			case strings.HasPrefix(p, "created "):
				if at, err := time.Parse(time.DateOnly, strings.TrimPrefix(p, "created ")); err == nil {
					task.Created = timestamppb.New(at)
				}
			case strings.HasPrefix(p, "http"):
				task.Ref.Url = p
			}
		}

		break
	}

	task.After = hostops.AfterIn(doc)

	return task
}
