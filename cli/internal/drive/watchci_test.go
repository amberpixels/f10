package drive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// ciWatch is a pass whose host reads red on head h3ad, counting its reads,
// and whose agent takes prompts into sent.
func ciWatch(t *testing.T, on bool) (*WatchInput, *collected, *[]string, *int) {
	t.Helper()

	f := testkit.NewFakes(t)
	f.Absent = append(f.Absent, "git rev-parse -q --verify MERGE_HEAD", "git status --porcelain",
		"git rev-parse --git-path")

	var (
		out   collected
		sent  []string
		reads int
	)

	in := &WatchInput{
		Mode: "report",
		Root: t.TempDir(),
		Now:  func() time.Time { return watchNow },
		Out:  &out,
		CI:   on,
		Checks: func(context.Context, forge.PR) (forge.HeadChecks, error) {
			reads++

			return forge.HeadChecks{State: forge.Red, SHA: "h3ad", Failed: []forge.Check{
				{Name: "test", Cancelled: true}, {Name: "lint", URL: "https://ci/lint"},
			}}, nil
		},
		Prompt: func(_ context.Context, _ WatchedTask, text string) error {
			sent = append(sent, text)

			return nil
		},
	}

	return in, &out, &sent, &reads
}

func redTask() WatchedTask {
	return WatchedTask{
		ID: "FT-1", Branch: "FT-1", Path: "/code/repo.FT-1", Ws: "ws:FT-1", Listed: true, Agent: agentctl.StatusIdle,
		Pr: &forge.PR{Number: 9, URL: "https://gh/pr/9", Status: forge.Open, HeadSHA: "h3ad"},
	}
}

// Off, a red head is told once, naming the first real failure, and
// nothing is sent; a green head is read once while it stays where it is.
func TestWatchReportsARedHeadOnce(t *testing.T) {
	in, out, sent, reads := ciWatch(t, false)
	store := map[string]watchReport{}

	for range 3 {
		in.reconcile(t.Context(), []WatchedTask{redTask()}, store)
	}

	if len(*out) != 1 || !strings.Contains((*out)[0].Text, "red at h3ad: lint https://ci/lint") ||
		(*out)[0].Next != "/f10:ci in /code/repo.FT-1" {
		t.Fatalf("told %+v, want one red report naming lint", *out)
	}

	if len(*sent) != 0 {
		t.Errorf("sent %v; want nothing", *sent)
	}

	in.Checks = func(context.Context, forge.PR) (forge.HeadChecks, error) {
		*reads++

		return forge.HeadChecks{State: forge.Green, SHA: "h3ad"}, nil
	}
	*reads = 0

	for range 3 {
		in.reconcile(t.Context(), []WatchedTask{redTask()}, store)
	}

	if *reads != 1 {
		t.Errorf("a green head read %d times, want once", *reads)
	}
}

// On, an idle agent gets /f10:ci once per head; a handed-back task, one a
// drive holds and a working agent get nothing.
func TestWatchSendsARedHeadUnderWatchCI(t *testing.T) {
	in, out, sent, _ := ciWatch(t, true)
	store := map[string]watchReport{}

	for range 2 {
		in.reconcile(t.Context(), []WatchedTask{redTask()}, store)
	}

	if len(*sent) != 1 || !strings.HasPrefix((*sent)[0], "/f10:ci FT-1 --driven && sent by the watcher") {
		t.Fatalf("sent %q, want one driven /f10:ci", *sent)
	}

	if len(*out) != 1 || !strings.Contains((*out)[0].Text, "sent /f10:ci") {
		t.Errorf("told %+v, want the send told once", *out)
	}

	for name, task := range map[string]func(*WatchedTask){
		"handed back": func(w *WatchedTask) { w.Off = true },
		"driven":      func(w *WatchedTask) { w.Owner = "drive 3" },
		"working":     func(w *WatchedTask) { w.Agent = agentctl.StatusWorking },
	} {
		in, _, sent, _ := ciWatch(t, true)
		w := redTask()
		task(&w)
		in.reconcile(t.Context(), []WatchedTask{w}, map[string]watchReport{})

		if len(*sent) != 0 {
			t.Errorf("%s: sent %q, want nothing", name, *sent)
		}
	}
}
