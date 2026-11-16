package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
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
	ctx, c := withCaller(ctx, req, m.GetCaller())

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "task")
	if err != nil {
		return nil, err
	}

	// nowhere to create answers exit 3 before any check, so capture falls back
	if rs, ok := taskwrite.RefusedUpFront(t); ok {
		return nil, rs.RefuseCreate()
	}

	if m.GetCheck() {
		return connect.NewResponse(&f10v1.CreateTaskResponse{}), nil
	}

	// a local f10 task create sends stdin whole, an empty one included
	whole := c != nil || m.GetMarkdown() != ""
	if !whole && strings.TrimSpace(m.GetTitle()) == "" {
		return nil, invalid("a task needs a title")
	}

	doc := "# " + strings.TrimSpace(m.GetTitle()) + "\n\n" + m.GetBody()

	// the contract puts the title on the first line, so leading blanks go
	if whole {
		doc = strings.TrimLeft(m.GetMarkdown(), " \t\r\n")
		if hostops.TaskTitle(doc) == "" {
			return nil, errors.New("task create reads the task as markdown on stdin, `# <title>` on its first line")
		}
	}

	p := hostops.Props{Status: m.GetStatus(), Priority: m.GetPriority(), Labels: m.GetLabels()}
	for _, k := range slices.Sorted(maps.Keys(m.GetProperties())) {
		p.Set = append(p.Set, k+"="+m.GetProperties()[k])
	}

	o := output(c)

	r, url, err := taskwrite.Create(ctx, &o.Stderr, t, taskwrite.New{Doc: doc, Props: p, Parent: m.GetParent()})
	if err != nil {
		return nil, o.Wrap(err)
	}

	fmt.Fprintln(&o.Stdout, r.ID)

	return connect.NewResponse(&f10v1.CreateTaskResponse{
		Task:    &f10v1.TaskRef{Id: r.ID, Title: taskwrite.DocTitle(r.ID, doc), Url: url},
		Printed: printedOf(o),
	}), nil
}

func (s *taskService) GetTask(
	ctx context.Context,
	req *connect.Request[f10v1.GetTaskRequest],
) (*connect.Response[f10v1.GetTaskResponse], error) {
	m := req.Msg
	ctx, c := withCaller(ctx, req, m.GetCaller())

	t, err := callerTarget(ctx, s.sup, m.GetProject(), c, "task")
	if err != nil {
		return nil, err
	}

	// a local f10 task read: its words, its bundles and its images
	if c != nil {
		doc, err := taskwrite.Document(ctx, t, m.GetId(), !m.GetSkipImages() && !c.GetTerminal())
		if err != nil {
			return nil, err
		}

		r, err := t.Reference(ctx, m.GetId())
		if err != nil {
			return nil, err
		}

		return connect.NewResponse(&f10v1.GetTaskResponse{Task: taskOf(r, doc), Document: doc}), nil
	}

	r, err := t.Reference(ctx, m.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	doc, err := taskwrite.Read(ctx, t, r)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.GetTaskResponse{Task: taskOf(r, doc), Document: doc}), nil
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
