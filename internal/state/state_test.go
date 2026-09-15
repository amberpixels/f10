package state

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, session, body string) string {
	t.Helper()

	path := Path(dir, session)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stamp := strconv.FormatInt(now.Unix(), 10)

	cases := []struct {
		name string
		body string
		want Run
	}{
		{
			name: "full file",
			body: "v 1\ntask GH-26\nurl https://x/26\nroot /r\ncapture prior\nplan done\nship blocked\nleaf judge\nfinal pr\nnote stop: why\nnext do this\nupdated " + stamp + "\n",
			want: Run{
				Task: "GH-26", URL: "https://x/26", Root: "/r",
				Capture: "prior", Plan: "done", Ship: "blocked",
				Leaf: "judge", Final: "pr", Note: "stop: why", Next: "do this",
				Updated: time.Unix(now.Unix(), 0),
			},
		},
		{
			name: "old file without the new keys",
			body: "v 1\ntask GH-1\nurl \ncapture done\nplan running\nship pending\nleaf \nfinal \nupdated " + stamp + "\n",
			want: Run{
				Task:    "GH-1",
				Capture: "done",
				Plan:    "running",
				Ship:    "pending",
				Updated: time.Unix(now.Unix(), 0),
			},
		},
		{
			name: "garbage timestamp reads as stale, statuses default to pending",
			body: "v 1\ntask GH-2\nupdated soon\nunknown key ignored\n",
			want: Run{Task: "GH-2", Capture: "pending", Plan: "pending", Ship: "pending"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, dir, "s-"+strings.ReplaceAll(tc.name, " ", "_"), tc.body)

			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			tc.want.Session = strings.TrimSuffix(filepath.Base(path), fileExt)
			if *got != tc.want {
				t.Errorf("Load mismatch\n got %+v\nwant %+v", *got, tc.want)
			}
		})
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.state"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestPathSanitizes(t *testing.T) {
	got := filepath.Base(Path("/d", "a b/c:d.e-f"))
	if got != "a_b_c_d.e-f.state" {
		t.Errorf("Path = %q", got)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(-d).Unix(), 10) }

	write(t, dir, "old", "task OLD\nupdated "+at(2*time.Hour)+"\n")
	write(t, dir, "new", "task NEW\nupdated "+at(time.Minute)+"\n")
	write(t, dir, "stale", "task STALE\nupdated "+at(48*time.Hour)+"\n")
	write(t, dir, "torn", "task TORN\nupdated nope\n")

	if err := os.WriteFile(filepath.Join(dir, "hooks.log"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	runs, err := List(dir, now, DefaultTTL)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var got []string
	for _, r := range runs {
		got = append(got, r.Task)
	}

	if strings.Join(got, ",") != "NEW,OLD" {
		t.Errorf("List order = %v, want NEW,OLD", got)
	}

	if runs, err := List(filepath.Join(dir, "absent"), now, DefaultTTL); err != nil || runs != nil {
		t.Errorf("missing dir: runs=%v err=%v", runs, err)
	}
}

func TestPhaseText(t *testing.T) {
	cases := []struct {
		status, leaf, want string
	}{
		{"running", "implement", "running: implement"},
		{"blocked", "judge", "blocked at judge"},
		{"failed", "", "failed"},
		{"done", "pr", "done"},
		{"pending", "", "pending"},
	}

	for _, tc := range cases {
		r := &Run{Ship: tc.status, Leaf: tc.leaf}
		if got := r.PhaseText("ship"); got != tc.want {
			t.Errorf("PhaseText(%s,%s) = %q, want %q", tc.status, tc.leaf, got, tc.want)
		}
	}

	// the leaf is ship's: a running capture beside a recorded leaf stays bare
	r := &Run{Capture: "running", Ship: "pending", Leaf: "implement"}
	if got := r.PhaseText("capture"); got != "running" {
		t.Errorf("PhaseText(capture) = %q, want running", got)
	}
}

// The script's show is the oracle for the words: every label it prints
// must come back with the same value from this package, so a field added
// on one side fails here until the other side carries it too.
func TestShowParity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}

	script := filepath.Join(filepath.Dir(file), "..", "..", "bin", "f10-state.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("f10-state.sh not found: %v", err)
	}

	dir := t.TempDir()
	env := append(os.Environ(), dirEnv+"="+dir, sessionEnv+"=parity", "NO_COLOR=1")

	run := func(args ...string) string {
		t.Helper()

		cmd := exec.Command(script, args...)
		cmd.Env = env

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}

		return string(out)
	}

	// the sequence a ship run stopped by a judge verdict makes
	run("task", "GH-26", "https://github.com/amberpixels/f10/issues/26")
	run("final", "pr")
	run("set", "ship", "running", "judge")
	run("set", "ship", "blocked", "judge")
	run("note", "stop: the change patches the symptom", "move the retry into the client")

	r, err := Load(Path(dir, "parity"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ours := map[string]string{
		"task":    r.Task,
		"url":     r.URL,
		"capture": r.PhaseText("capture"),
		"plan":    r.PhaseText("plan"),
		"ship":    r.PhaseText("ship"),
		"note":    r.Note,
		"next":    r.Next,
		"final":   r.Final,
	}

	seen := 0

	for line := range strings.SplitSeq(strings.TrimSpace(run("show")), "\n") {
		label, rest, _ := strings.Cut(line, " ")
		value := strings.TrimSpace(rest)

		switch label {
		case "capture", "plan", "ship":
			// the script prefixes a glyph; the words after it are the value
			_, value, _ = strings.Cut(value, " ")
		case "updated", "root":
			continue // age is time-dependent; root is recorded only by the seed hook
		}

		want, known := ours[label]
		if !known {
			t.Errorf("script prints %q, which this package does not carry", label)

			continue
		}

		seen++

		if value != want {
			t.Errorf("%s: script %q, package %q", label, value, want)
		}
	}

	if seen != len(ours) {
		t.Errorf("script printed %d of the %d shared labels", seen, len(ours))
	}
}
