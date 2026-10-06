package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// A host is one of the two forge CLIs, gh or glab, bound to the checkout
// it answers for. Every call to either goes through here: the spelling
// each CLI wants is picked in one place, the JSON each returns is read
// into one shape per noun (issue in host.go, pull in merge.go), and a
// non-zero exit is the CLI's own stderr, verbatim, since it names the
// cause better than any paraphrase.
type host struct {
	name string // "gh" or "glab"
	dir  string // the checkout the CLI reads the remote from
}

func (h host) glab() bool { return h.name == "glab" }

// at is the same host answering from another checkout of the repo.
func (h host) at(dir string) host {
	h.dir = dir

	return h
}

// pick is this host's spelling of a command: gh's or glab's.
func (h host) pick(gh, glab []string) []string {
	if h.glab() {
		return glab
	}

	return gh
}

// run runs one host CLI command and returns its stdout.
func (h host) run(ctx context.Context, args ...string) (string, error) {
	res, err := shell.Capture(ctx, h.dir, h.name, args...)
	if err != nil {
		return "", fmt.Errorf("running %s: %w", h.name, err)
	}

	if res.Code != 0 {
		return "", fmt.Errorf("%s: %s", h.name, cmp.Or(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.Code)))
	}

	return res.Stdout, nil
}

// decode runs one host CLI command and reads its JSON stdout into v.
func (h host) decode(ctx context.Context, v any, args ...string) error {
	out, err := h.run(ctx, args...)
	if err != nil {
		return err
	}

	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("parsing %s output: %w", h.name, err)
	}

	return nil
}

// decodePages runs a `api --paginate` call and reads every page into one
// list. gh merges the pages of an array into one array; glab may print one
// document per page, so every document in the output is read in turn.
func decodePages[T any](ctx context.Context, h host, path string) ([]T, error) {
	out, err := h.run(ctx, "api", "--paginate", path)
	if err != nil {
		return nil, err
	}

	var all []T

	dec := json.NewDecoder(strings.NewReader(out))

	for {
		var page []T

		err := dec.Decode(&page)
		if errors.Is(err, io.EOF) {
			return all, nil
		}

		if err != nil {
			return nil, fmt.Errorf("parsing %s output: %w", h.name, err)
		}

		all = append(all, page...)
	}
}
