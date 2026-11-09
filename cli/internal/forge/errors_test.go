package forge

import (
	"context"
	"errors"
	"testing"
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

		_, err := Host{Name: c.cli}.Run(context.Background(), "pr", "view", "9")

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
