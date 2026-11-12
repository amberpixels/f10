package stack_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestBundleAfter(t *testing.T) {
	bodies := map[string]string{}

	trimmed := stack.Tracker{
		Resolve: func(_ context.Context, token string) (ref.Ref, error) {
			return ref.Ref{ID: strings.ToUpper(token)}, nil
		},
		Info: func(_ context.Context, r ref.Ref) (tasksource.TaskInfo, error) {
			body, ok := bodies[r.ID]
			if !ok {
				return tasksource.TaskInfo{}, errors.New("no such task")
			}

			return tasksource.TaskInfo{Body: body}, nil
		},
	}

	bodies["GH-2"] = "After: GH-7"

	line, err := stack.BundleAfter(t.Context(), trimmed, testkit.Bundle12, "After: GH-2")
	if err != nil || line != "GH-7" {
		t.Errorf("bundleAfter = %q, %v; want the rider's outside base GH-7", line, err)
	}

	bodies["GH-2"] = "After: GH-8"

	if _, err := stack.BundleAfter(t.Context(), trimmed, testkit.Bundle12, "After: GH-7"); err == nil ||
		!strings.Contains(err.Error(), "two bases") {
		t.Errorf("err = %v, want two bases refused", err)
	}

	if line, err := stack.BundleAfter(t.Context(), trimmed, testkit.Gh1, "After: GH-7"); err != nil || line != "GH-7" {
		t.Errorf("a single task's line = %q, %v; want its own body's", line, err)
	}
}
