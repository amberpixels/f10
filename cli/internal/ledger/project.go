package ledger

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// Place is where an event happened: the project, the checkout and its
// branch.
type Place struct {
	Project      string
	ProjectLocal bool // no remote: the main checkout's path stands in
	Checkout     string
	Branch       string
}

// Where reads the place a directory is in. Outside git, everything is empty
// but the project, which is the directory itself, flagged local.
func Where(ctx context.Context, dir string) Place {
	if dir == "" {
		return Place{}
	}

	p := Place{
		Checkout: gitx.Out(ctx, dir, "rev-parse", "--show-toplevel"),
		Branch:   gitx.CurrentBranch(ctx, dir),
	}

	remote := gitx.Out(ctx, dir, "remote", "get-url", "origin")
	if remote == "" {
		if names := strings.Fields(gitx.Out(ctx, dir, "remote")); len(names) > 0 {
			remote = gitx.Out(ctx, dir, "remote", "get-url", names[0])
		}
	}

	if p.Project = NormalizeRemote(remote); p.Project != "" {
		return p
	}

	p.ProjectLocal = true
	p.Project = dir

	if wts, err := gitx.Worktrees(ctx, dir); err == nil && len(wts) > 0 {
		p.Project = wts[0].Path
	} else if p.Checkout != "" {
		p.Project = p.Checkout
	}

	return p
}

// NormalizeRemote turns a remote url into a project id, so one repo cloned
// over ssh and over https is one project: scheme, user, port and `.git`
// dropped, scp-style `host:path` read as `host/path`, the host lowercased.
func NormalizeRemote(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}

	rest, hasScheme := cutScheme(url)

	if !hasScheme {
		// scp-style `[user@]host:path`; a path with no colon before its first
		// slash is a local remote
		colon, slash := strings.Index(rest, ":"), strings.Index(rest, "/")
		if colon < 0 || (slash >= 0 && slash < colon) {
			return filepath.Clean(rest)
		}

		rest = rest[:colon] + "/" + strings.TrimPrefix(rest[colon+1:], "/")
	}

	if strings.HasPrefix(url, "file://") {
		return filepath.Clean(rest)
	}

	host, path, _ := strings.Cut(rest, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}

	// an ssh:// port is how the host is reached, not which repo it is
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	path = strings.TrimSuffix(path, "/")

	return strings.ToLower(host) + "/" + path
}

func cutScheme(url string) (string, bool) {
	if _, rest, ok := strings.Cut(url, "://"); ok {
		return rest, true
	}

	return url, false
}
