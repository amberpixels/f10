package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// The board checked on recorded runs: each fixture under testdata/board is
// every git and host call the collectors make with its answer, plus this
// machine's state, beside the Board JSON out. A call the fixture does not
// record fails it, so a feed reading per request fails. `{root}` stands for
// the main checkout. F10_UPDATE=1 rewrites the boards; read the diff first.
//
// Record a new one's calls from a public amberpixels checkout, never a
// private one, then fill in the local state by hand:
//
//	F10_BOARD_RECORD='<checkout>|<name>|<id prefix>' go test ./cmd/f10 -run TestRecordBoardFixture
type boardFixture struct {
	Now    time.Time              `json:"now"`
	Since  time.Time              `json:"since"`            // the state view's done window
	Feed   bool                   `json:"feed,omitempty"`   // the feed view instead of the state
	Window string                 `json:"window,omitempty"` // the feed's window argument
	Prefix string                 `json:"prefix"`
	Host   string                 `json:"host"`
	Remote bool                   `json:"remote,omitempty"`
	Repo   string                 `json:"repo,omitempty"` // a repository with no checkout: owner/repo, read remote
	Calls  map[string]fixtureCall `json:"calls"`
	Tasks  []lookaround.Task      `json:"tasks,omitempty"` // a driver's list; absent, the host's issues answer from calls
	Runs   map[string]string      `json:"runs,omitempty"`  // session id to state file
	Files  map[string]string      `json:"files,omitempty"` // path to content, under {root}
}

func TestBoardFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/board/*.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range paths {
		if strings.HasSuffix(path, ".board.json") {
			continue
		}

		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			fx := readBoardFixture(t, path)
			got := runBoardFixture(t, fx)

			raw, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			raw = append(raw, '\n')
			golden := strings.TrimSuffix(path, ".json") + ".board.json"

			if os.Getenv("F10_UPDATE") != "" {
				if err := os.WriteFile(golden, raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (F10_UPDATE=1 writes it)", err)
			}

			if !bytes.Equal(raw, want) {
				t.Errorf("board differs from %s:\n%s", golden, raw)
			}
		})
	}
}

func readBoardFixture(t *testing.T, path string) boardFixture {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var fx boardFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}

	return fx
}

func runBoardFixture(t *testing.T, fx boardFixture) lookaround.Board {
	t.Helper()

	root := filepath.Join(t.TempDir(), "widgets")
	sub := func(s string) string { return strings.ReplaceAll(s, "{root}", root) }

	for rel, content := range fx.Files {
		p := sub(rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(sub(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for sid, content := range fx.Runs {
		content = strings.ReplaceAll(sub(content), "{now}", strconv.FormatInt(fx.Now.Unix(), 10))
		if err := os.WriteFile(filepath.Join(stateDir, sid+".state"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	f := newFakes(t)
	f.absent = append(
		f.absent,
		"git log --no-show-signature --format=%H %ae",
		"git config --get-regexp",
		"git symbolic-ref",
		"git for-each-ref",
		"git worktree list",
	)

	for key, res := range fx.Calls {
		res.Stdout = sub(res.Stdout)
		f.answers[sub(key)] = []shell.Result{shell.Result(res)}
	}

	h := newHost(fx.Host, root)
	if fx.Repo != "" {
		h, fx.Remote = host{Name: fx.Host, Repo: fx.Repo}, true
	}

	in := lookaround.Inputs{
		Project: lookaround.Project{ID: "github.com/acme/widgets", Name: "widgets", Host: fx.Host, Remote: fx.Remote},
		Prefix:  fx.Prefix,
		Since:   fx.Since,
		Host:    &h.Host,
		Remote:  fx.Remote,
		Now:     func() time.Time { return fx.Now },
	}

	if !fx.Remote {
		in.Project.Checkout = root
		in.Local = &lookaround.LocalInputs{
			Main: root, StateDir: stateDir, TTL: 365 * 24 * time.Hour,
			LedgerPath: filepath.Join(t.TempDir(), "none.db"), ProjectID: in.Project.ID,
		}
	}

	in.Tasks = func(ctx context.Context) ([]lookaround.Task, error) {
		if fx.Tasks != nil {
			return fx.Tasks, nil
		}

		rows, err := h.listIssues(ctx, "", fx.Prefix, nil, boardPage)
		if err != nil {
			return nil, err
		}

		return boardTasks(rows, fx.Prefix), nil
	}

	var board lookaround.Board

	if fx.Feed {
		span, err := resolveBoardSpan(fx.Window, false, fx.Now, func() (standup.Workdays, error) {
			return standup.DefaultWorkdays(), nil
		})
		if err != nil {
			t.Fatal(err)
		}

		in.Since, in.Feed = span.from(), true
		facts := lookaround.Collect(t.Context(), in)

		w := lookaround.ChooseWindow(span.today, span.lastWorkday, facts)
		if span.named != nil {
			w = *span.named
		}

		board = lookaround.DeriveFeed(facts, fx.Now, w)
	} else {
		board = lookaround.Derive(lookaround.Collect(t.Context(), in), fx.Now, fx.Since)
	}

	// the temp root is the machine's, not the fixture's
	board.Project.Checkout = sub2(board.Project.Checkout, root)
	for i := range board.Rows {
		r := &board.Rows[i]
		r.Where.Worktree = sub2(r.Where.Worktree, root)

		// the agent's name hashes the worktree path, so it is named over
		// the fixture's spelling of the path
		if r.Who.Agent != "" {
			r.Who.Agent = lookaround.AgentFor(r.Key, r.Where.Branch, r.Where.Worktree)
		}
	}

	for _, call := range f.calls {
		for _, verb := range []string{" edit ", " create ", " comment ", " -X ", " merge ", " close "} {
			if strings.Contains(call, verb) {
				t.Errorf("the board wrote to the host: %s", call)
			}
		}
	}

	return board
}

func sub2(s, root string) string { return strings.ReplaceAll(s, root, "{root}") }

func TestRecordBoardFixture(t *testing.T) {
	spec := os.Getenv("F10_BOARD_RECORD")
	if spec == "" {
		t.Skip("F10_BOARD_RECORD is not set")
	}

	parts := strings.Split(spec, "|")
	if len(parts) != 3 {
		t.Fatalf("F10_BOARD_RECORD = %q, want checkout|name|prefix", spec)
	}

	root, err := filepath.Abs(parts[0])
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	fx := boardFixture{
		Now:    now,
		Since:  now.Add(-boardStateWindow),
		Prefix: parts[2],
		Host:   "gh",
		Calls:  map[string]fixtureCall{},
	}

	sub := func(s string) string { return strings.ReplaceAll(s, root, "{root}") }

	prevExec, prevCapture := gitx.Exec, shell.Capture
	t.Cleanup(func() { gitx.Exec, shell.Capture = prevExec, prevCapture })

	gitx.Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
		out, err := prevExec(ctx, dir, args...)

		res := fixtureCall{Stdout: sub(out)}
		if err != nil {
			res = fixtureCall{Code: gitExitCode(err), Stderr: sub(err.Error())}
		}

		fx.Calls[sub("git "+strings.Join(args, " "))] = res

		return out, err
	}

	shell.Capture = func(ctx context.Context, dir, name string, args ...string) (shell.Result, error) {
		res, err := prevCapture(ctx, dir, name, args...)
		fx.Calls[sub(filepath.Base(name)+" "+strings.Join(args, " "))] = fixtureCall{
			Stdout: sub(res.Stdout), Stderr: sub(res.Stderr), Code: res.Code,
		}

		return res, err
	}

	h := forge.Host{Name: "gh", Dir: root}
	in := lookaround.Inputs{
		Project: lookaround.Project{ID: "recorded", Name: filepath.Base(root), Host: "gh", Remote: true},
		Prefix:  fx.Prefix, Since: fx.Since, Host: &h, Remote: true,
		Tasks: func(ctx context.Context) ([]lookaround.Task, error) {
			rows, err := newHost("gh", root).listIssues(ctx, "", fx.Prefix, nil, boardPage)
			if err != nil {
				return nil, err
			}

			return boardTasks(rows, fx.Prefix), nil
		},
	}

	lookaround.Collect(t.Context(), in)

	data, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join("testdata", "board", parts[1]+".json"),
		append(data, '\n'),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

// On a repository nothing ever traced, every in-progress row is guessed.
func TestBoardUntracedIsGuessed(t *testing.T) {
	got := runBoardFixture(t, readBoardFixture(t, "testdata/board/untraced.json"))

	for _, r := range got.Rows {
		if r.Stage != lookaround.StageNext && r.Stage != lookaround.StageDone && r.StageFrom != lookaround.FromGuessed {
			t.Errorf("%s: %s from %s, want guessed", r.Key, r.Stage, r.StageFrom)
		}
	}
}

// With traces on, the checkout and a viewer without it agree on every
// stage where local adds nothing.
func TestBoardTracedAgreesWithRemote(t *testing.T) {
	local := runBoardFixture(t, readBoardFixture(t, "testdata/board/traced.json"))
	remote := runBoardFixture(t, readBoardFixture(t, "testdata/board/traced-remote.json"))

	stages := map[string]string{}
	for _, r := range remote.Rows {
		stages[r.Key] = r.Stage
	}

	for _, r := range local.Rows {
		if want, ok := stages[r.Key]; !ok || want != r.Stage {
			t.Errorf("%s: %s here, %s remote", r.Key, r.Stage, want)
		}
	}
}
