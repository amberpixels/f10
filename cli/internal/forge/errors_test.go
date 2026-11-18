package forge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// Every phrase either CLI uses for a missing item reads as ErrNotFound, and
// the error text stays the CLI's own words behind its name.
func TestRunClassifiesNotFound(t *testing.T) {
	for _, c := range []struct {
		cli, stderr string
		missing     bool
	}{
		{"gh", "GraphQL: Could not resolve to a PullRequest with the number of 85. (repository.pullRequest)", true},
		{"gh", "GraphQL: Could not resolve to an Issue with the number of 7. (repository.issue)", true},
		{"gh", "no pull requests found for branch \"x\"", true},
		{"gh", "gh: Not Found (HTTP 404)", true},
		{"glab", "no open merge request available for \"x\"", true},
		{"glab", "GET https://gitlab.com/api/v4/projects/1/merge_requests/9: 404 Not Found", true},
		{"gh", "To get started with GitHub CLI, please run:  gh auth login", false},
		{"glab", "connection refused", false},
	} {
		script(t, map[string]string{c.cli + " pr view 9": "FAIL " + c.stderr})

		_, err := Host{Name: c.cli}.run(context.Background(), "pr", "view", "9")

		if got := errors.Is(err, ErrNotFound); got != c.missing {
			t.Errorf("%s %q: ErrNotFound = %v, want %v", c.cli, c.stderr, got, c.missing)
		}

		if want := c.cli + ": " + c.stderr; err == nil || err.Error() != want {
			t.Errorf("error text = %v, want %q", err, want)
		}

		var he *Error
		if !errors.As(err, &he) || he.Code != 1 {
			t.Errorf("%v is not a *Error with the exit code", err)
		}
	}
}

// A 5xx is retried once where the host opts in, and never otherwise; a 404
// is never retried.
func TestRunRetriesTransientOnce(t *testing.T) {
	seam.Swap(t, &RetryPause, 0)

	for _, c := range []struct {
		name    string
		retry   bool
		answers []string
		calls   int
		ok      bool
	}{
		{"504 then success", true, []string{"FAIL gh: HTTP 504", "[]"}, 2, true},
		{"two 504s", true, []string{"FAIL gh: HTTP 504", "FAIL gh: HTTP 504"}, 2, false},
		{"404", true, []string{"FAIL gh: Not Found (HTTP 404)"}, 1, false},
		{"opted out", false, []string{"FAIL gh: HTTP 502", "[]"}, 1, false},
		{"glab 503", true, []string{"FAIL GET https://x: 503 Service Unavailable", "[]"}, 2, true},
	} {
		calls := 0

		seam.Swap(t, &shell.Capture, func(_ context.Context, _, _ string, _ ...string) (shell.Result, error) {
			out := c.answers[min(calls, len(c.answers)-1)]
			calls++

			if rest, failed := strings.CutPrefix(out, "FAIL "); failed {
				return shell.Result{Code: 1, Stderr: rest}, nil
			}

			return shell.Result{Stdout: out}, nil
		})

		_, err := Host{Name: "gh", RetryTransient: c.retry}.run(t.Context(), "api", "x")

		if calls != c.calls || (err == nil) != c.ok {
			t.Errorf("%s: %d calls, err %v; want %d calls, ok %v", c.name, calls, err, c.calls, c.ok)
		}

		if c.name == "two 504s" && !errors.Is(err, ErrTransient) {
			t.Errorf("%s: %v is not ErrTransient", c.name, err)
		}
	}
}
