package testkit

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta/branchmetatest"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// Fakes stands in for git, wt, herdr and the driver: every call recorded,
// every answer scripted, nothing executed. A key scripted with several
// answers hands them out in order, repeating the last, so a listing changes.
type Fakes struct {
	// mu guards calls and answers: the board's collectors call concurrently
	mu      sync.Mutex
	T       *testing.T
	Calls   []string
	Answers map[string][]shell.Result
	Has     map[string]bool
	// Absent answers a call with a key under one of these prefixes, and no
	// script of its own, the way git answers a ref or config it lacks
	Absent []string
	// Defaults answers a call under one of these prefixes, and no script of
	// its own, with that stdout
	Defaults map[string]string
	// Launches are the agent starts argvLaunch answered for a test
	Launches []string
	// Meta holds the branch records; its writes join calls in order
	Meta *branchmetatest.Memory
}

func NewFakes(t *testing.T) *Fakes {
	t.Helper()

	f := &Fakes{T: t, Answers: map[string][]shell.Result{}, Has: map[string]bool{"herdr": true}}
	f.Meta = branchmetatest.Use(t)
	f.Meta.OnWrite = func(w branchmetatest.Write) {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.Calls = append(f.Calls, w.String())
	}
	// Herdr shows no workspace until a test says otherwise
	f.Script("herdr workspace list", `{"result":{"workspaces":[]}}`)
	// no local branch carries a task id past a prefix until a test says so
	f.Script(AllHeads, "")
	// no branch is linked to a task, origin has no task branch, and finish
	// measures an empty branch (so records no size), until a test says so
	// a PR's head has no checks until a test says so
	f.Defaults = map[string]string{GhChecks: NoChecks}
	f.Absent = []string{
		"git for-each-ref --format=%(refname) refs/remotes/origin/", "git diff --numstat ",
		"git rev-list --ancestry-path --merges",
		// no base tip is recorded until a test says so
		"git merge-base refs/heads/",
		// an agent's screen shows no input box until a test says so, so no
		// typed prompt is taken for unsubmitted
		"herdr agent read ",
	}

	seam.Swap(t, &launch.EnterRetry, 0)
	seam.Swap(t, &launch.EnterPoll, 0)

	// task refs need the project from git, which these fakes do not script
	seam.Swap(t, &target.ProjectOf, func(context.Context, string) string { return "" })

	seam.Swap(t, &gitx.Exec, func(_ context.Context, _ string, args ...string) (string, error) {
		res := f.Answer("git " + strings.Join(args, " "))
		if res.Code != 0 {
			return "", &gitx.ExitError{
				Args: args, Code: res.Code, Stdout: strings.TrimSpace(res.Stdout), Stderr: res.Stderr,
			}
		}

		return strings.TrimSpace(res.Stdout), nil
	})

	seam.Swap(t, &shell.Capture, func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		args = withoutTestGrants(name, args)
		if res, ok := f.argvLaunch(filepath.Base(name), args); ok {
			return res, nil
		}

		return f.Answer(filepath.Base(name) + " " + strings.Join(args, " ")), nil
	})

	seam.Swap(t, &shell.Has, func(name string) bool { return f.Has[name] })
	t.Setenv("HERDR_ENV", "1")

	return f
}

func (f *Fakes) Answer(key string) shell.Result {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Calls = append(f.Calls, key)

	queue, ok := f.Answers[key]
	if !ok {
		for _, p := range f.Absent {
			if strings.HasPrefix(key, p) {
				return shell.Result{}
			}
		}

		for p, out := range f.Defaults {
			if strings.HasPrefix(key, p) {
				return shell.Result{Stdout: out}
			}
		}

		// no rebase's state directory exists until a test says so
		if name, ok := strings.CutPrefix(key, "git rev-parse --git-path "); ok {
			return shell.Result{Stdout: "/nonexistent/" + name}
		}

		f.T.Fatalf("unscripted call: %q\nscripted:\n  %s", key, strings.Join(f.keys(), "\n  "))
	}

	if len(queue) > 1 {
		f.Answers[key] = queue[1:]
	}

	return queue[0]
}

// withoutTestGrants drops the --add-dir pairs Storage adds to an agent
// start, and the "--" they leave alone at the end.
func withoutTestGrants(name string, args []string) []string {
	if filepath.Base(name) != "herdr" || len(args) < 2 || args[0] != "agent" || args[1] != "start" {
		return args
	}

	var out []string

	for i := 0; i < len(args); i++ {
		if args[i] == "--add-dir" && i+1 < len(args) && gitx.Inside(args[i+1], Storage.Path()) {
			i++

			continue
		}

		out = append(out, args[i])
	}

	if len(out) > 0 && out[len(out)-1] == "--" {
		out = out[:len(out)-1]
	}

	return out
}

// Script sets the answers for key: one per call, the last repeating.
func (f *Fakes) Script(key string, stdouts ...string) {
	f.Answers[key] = nil
	for _, s := range stdouts {
		f.Answers[key] = append(f.Answers[key], shell.Result{Stdout: s})
	}
}

func (f *Fakes) Fail(key string, res shell.Result) { f.Answers[key] = []shell.Result{res} }

func (f *Fakes) Called(prefix string) bool {
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}

	return false
}

// argvLaunch answers an agent started with its first prompt as the start
// a test scripted without it, followed by that prompt submitted, unless the
// test scripted the launch itself: the agent takes the prompt either way.
// The real call goes to launches, so a test can still tell the two apart.
func (f *Fakes) argvLaunch(name string, args []string) (shell.Result, bool) {
	sep := slices.Index(args, "--")
	if name != "herdr" || len(args) < 3 || args[0] != "agent" || args[1] != "start" || sep < 0 || sep+1 >= len(args) {
		return shell.Result{}, false
	}

	launched := name + " " + strings.Join(args, " ")

	f.mu.Lock()
	_, scripted := f.Answers[launched]
	if !scripted {
		f.Launches = append(f.Launches, launched)
	}
	f.mu.Unlock()

	if scripted {
		return shell.Result{}, false
	}

	started := slices.Concat(args[:sep], args[sep+2:])
	if sep+2 < len(args) {
		started = slices.Concat(args[:sep+1], args[sep+2:])
	}

	res := f.Answer(name + " " + strings.Join(started, " "))
	if res.Code != 0 {
		return res, true
	}

	return f.Answer(name + " agent prompt " + args[2] + " " + args[sep+1]), true
}

func (f *Fakes) keys() []string {
	var ks []string
	for k := range f.Answers {
		ks = append(ks, k)
	}

	return ks
}

// FakeDriver is an executable at <root>/driver so driver.Find accepts it;
// what it answers comes from the fakes, never from running it.
func FakeDriver(t *testing.T) *trackerdriver.Driver {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, "driver")

	err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o700) //nolint:gosec // the fake driver must run
	if err != nil {
		t.Fatal(err)
	}

	return trackerdriver.Find(root, root)
}

const (
	RefsOrigin = "git for-each-ref --format=%(refname) refs/remotes/origin/GH-1 refs/remotes/origin/GH-1-* refs/remotes/origin/GH-1+* refs/remotes/origin/GH-1+*/*"
	RefsLocal  = "git for-each-ref --format=%(refname) refs/heads/GH-1 refs/heads/GH-1-* refs/heads/GH-1+* refs/heads/GH-1+*/*"
	RefsAfter  = "git for-each-ref --format=%(refname) refs/heads/GH-7 refs/heads/GH-7-* refs/heads/GH-7+* refs/heads/GH-7+*/*"
	RefsAfterO = "git for-each-ref --format=%(refname) refs/remotes/origin/GH-7 refs/remotes/origin/GH-7-* refs/remotes/origin/GH-7+* refs/remotes/origin/GH-7+*/*"
	AllHeads   = "git for-each-ref --format=%(refname) refs/heads/"
	ShaAfter   = "git rev-parse --verify --quiet GH-7/base"
	CfgTask    = "set branch.GH-1.f10-after GH-7"
	CfgBranch  = "set branch.GH-1.f10-after-branch GH-7/base"
	Worktrees  = "git worktree list --porcelain"
	ShaLocal   = "git rev-parse --verify --quiet refs/heads/release/1.2"
	ShaRemote  = "git rev-parse --verify --quiet refs/remotes/release/1.2"
	ShaOrigin  = "git rev-parse --verify --quiet refs/remotes/origin/release/1.2"
	HeadRef    = "git symbolic-ref --short refs/remotes/origin/HEAD"
	ShaMain    = "git rev-parse --verify --quiet refs/remotes/origin/main"
	Opened     = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":false}}`
	Reopened   = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":true}}`
	AgentFound = `{"result":{"agent":{"name":"claude-by-hand"}}}`
)

// AgentOf is the name start gives the agent of a task with no suffix,
// checked out at the sibling path these tests use.
func AgentOf(id string) string { return agentctl.AgentName(id, "", "/code/repo."+id) }

var Gh1 = ref.Ref{ID: "GH-1", Number: "1", Origin: ref.OriginExplicit}

// Missing is what rev-parse --verify --quiet answers for a ref that is not there.
var Missing = shell.Result{Code: 1}

// RefsKey is the for-each-ref call that looks for task id's branches under root.
func RefsKey(root, id string) string {
	return "git for-each-ref --format=%(refname) " + strings.Join(stack.TaskRefs(root, id), " ")
}

// Heads prefixes branch names the way for-each-ref prints them.
func Heads(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("refs/heads/" + n + "\n")
	}

	return b.String()
}

// Porcelain is a worktree listing of main plus the given branch/path pairs.
func Porcelain(pairs ...string) string {
	var b strings.Builder

	b.WriteString("worktree /code/repo\nHEAD a\nbranch refs/heads/main\n")

	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("\nworktree " + pairs[i+1] + "\nHEAD b\nbranch refs/heads/" + pairs[i] + "\n")
	}

	return b.String()
}

// Titled is a title lookup that answers with one title, or fails.
func Titled(title string, err error) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return title, err }
}

// WritePlan saves a plan file at path, making its dir.
func WritePlan(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// IndexOf loads a branch index from the records the test holds now.
func IndexOf(t *testing.T) *stack.BranchIndex {
	t.Helper()

	x, err := stack.LoadBranchIndex(t.Context(), "/code/repo")
	if err != nil {
		t.Fatal(err)
	}

	return x
}
