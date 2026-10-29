// Package forge binds a forge's CLI, gh or glab. Every call goes through
// Host, so each CLI's spelling is picked in one place and its JSON read into
// one shape per noun; a non-zero exit is the CLI's own stderr verbatim,
// since it names the cause better than any paraphrase.
package forge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// A Host is one of the two forge CLIs, bound to the checkout it answers
// for, or to a repository named outright where no checkout exists.
type Host struct {
	Name string // "gh" or "glab"
	Dir  string // the checkout the CLI reads the remote from
	// Repo names the repository when no checkout can: `owner/repo`, or
	// `host/owner/repo` off the CLI's default host; set, every command carries it.
	Repo string
}

// GLab reports whether the host is GitLab's CLI.
func (h Host) GLab() bool { return h.Name == "glab" }

// At is the same host answering from another checkout of the repo.
func (h Host) At(dir string) Host {
	h.Dir = dir

	return h
}

// Pick is this host's spelling of a command: gh's or glab's.
func (h Host) Pick(gh, glab []string) []string {
	if h.GLab() {
		return glab
	}

	return gh
}

// Run runs one host CLI command and returns its stdout.
func (h Host) Run(ctx context.Context, args ...string) (string, error) {
	res, err := shell.Capture(ctx, h.Dir, h.Name, h.named(args)...)
	if err != nil {
		return "", fmt.Errorf("running %s: %w", h.Name, err)
	}

	if res.Code != 0 {
		return "", fmt.Errorf("%s: %s", h.Name, cmp.Or(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.Code)))
	}

	return res.Stdout, nil
}

// Decode runs one host CLI command and reads its JSON stdout into v.
func (h Host) Decode(ctx context.Context, v any, args ...string) error {
	out, err := h.Run(ctx, args...)
	if err != nil {
		return err
	}

	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("parsing %s output: %w", h.Name, err)
	}

	return nil
}

// Login is who the CLI is signed in as: gh names it login, glab username.
func (h Host) Login(ctx context.Context) (string, error) {
	var user struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}

	if err := h.Decode(ctx, &user, "api", "user"); err != nil {
		return "", err
	}

	login := cmp.Or(user.Login, user.Username)
	if login == "" {
		return "", fmt.Errorf("%s returned no login for the signed-in user", h.Name)
	}

	return login, nil
}

// named puts Repo on args: an api call gets `{owner}`, `{repo}` and
// `:fullpath` filled here, since the CLIs fill them from a checkout; every
// other command takes -R. glab wants an off-host repository as a url.
func (h Host) named(args []string) []string {
	if h.Repo == "" || len(args) == 0 {
		return args
	}

	host, path := "", h.Repo
	if i := strings.Index(path, "/"); i > 0 && strings.Count(path, "/") > 1 && strings.Contains(path[:i], ".") {
		host, path = path[:i], path[i+1:]
	}

	if args[0] != "api" {
		repo := h.Repo
		if host != "" && h.GLab() {
			repo = "https://" + h.Repo
		}

		return append(slices.Clone(args), "-R", repo)
	}

	owner, name, _ := strings.Cut(path, "/")
	fill := strings.NewReplacer(
		"{owner}", owner, "{repo}", name,
		":fullpath", url.PathEscape(path), ":id", url.PathEscape(path),
	)

	out := slices.Clone(args)
	for i := 1; i < len(out); i++ {
		out[i] = fill.Replace(out[i])
	}

	if host != "" && !h.GLab() {
		out = append(out, "--hostname", host)
	}

	return out
}

// DecodePages runs a `api --paginate` call and reads every page into one
// list. gh merges the pages of an array into one array; glab may print one
// document per page, so every document in the output is read in turn.
func DecodePages[T any](ctx context.Context, h Host, path string) ([]T, error) {
	out, err := h.Run(ctx, "api", "--paginate", path)
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
			return nil, fmt.Errorf("parsing %s output: %w", h.Name, err)
		}

		all = append(all, page...)
	}
}

// The three states a request can be in, whichever CLI spelled them.
const (
	Open   = "open"
	Merged = "merged"
	Closed = "closed"
)

// Status normalises a request's state: gh spells them OPEN / MERGED /
// CLOSED, glab opened / merged / closed / locked.
func Status(state string) string {
	switch s := strings.ToLower(state); s {
	case "opened", "locked":
		return Open
	default:
		return s
	}
}

// SameLogin matches a declared identity against a host login, case blind. A
// bare `claude` also matches `claude[bot]`, never the reverse, or a user
// registering the bare name could post as the declared bot.
func SameLogin(declared, login string) bool {
	d, l := strings.ToLower(declared), strings.ToLower(login)
	if d == "" {
		return false
	}

	return d == l || d+"[bot]" == l
}
