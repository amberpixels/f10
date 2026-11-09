package forge

import (
	"errors"
	"strings"
)

// The kinds a host failure is classified into; callers test them with
// errors.Is and never read the CLI's words.
var (
	// ErrNotFound is the host saying the issue or request does not exist.
	ErrNotFound = errors.New("not found")
	// ErrUnsupported is a host without the feature asked for, such as
	// GitHub sub-issues or GitLab work items.
	ErrUnsupported = errors.New("not supported by this host")
)

// An Error is a host CLI exiting non-zero. Its text is the CLI's own stderr,
// since that names the cause better than any paraphrase.
type Error struct {
	Host   string // "gh" or "glab"
	Code   int
	Stderr string

	kind error
}

func (e *Error) Error() string { return e.Host + ": " + e.Stderr }

func (e *Error) Unwrap() error { return e.kind }

// notFound lists every phrase either CLI uses for a missing issue or request.
var notFound = []string{
	"Could not resolve to a PullRequest", // gh, a request by number
	"Could not resolve to an Issue",      // gh, an issue by number
	"no pull requests found",             // gh, a request by branch
	"no open merge request",              // glab, a request by branch
	"404 Not Found",                      // glab, by number or path
	"HTTP 404",                           // gh api
}

func classify(stderr string) error {
	for _, phrase := range notFound {
		if strings.Contains(stderr, phrase) {
			return ErrNotFound
		}
	}

	return nil
}
