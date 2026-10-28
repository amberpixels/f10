package state

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
			body: "v 1\ntask GH-26\nurl https://x/26\nroot /r\ncapture prior\nplan done\nship blocked\nleaf judge\nfinal pr\npipeline implement → review (local) → judge → pr\nnote stop: why\nnext do this\nask 1. which? [a | b]\nupdated " + stamp + "\n",
			want: Run{
				Task: "GH-26", URL: "https://x/26", Root: "/r",
				Capture: "prior", Plan: "done", Ship: "blocked",
				Leaf: "judge", Final: "pr", Pipeline: []string{"implement", "review (local)", "judge", "pr"},
				Note: "stop: why", Next: "do this", Ask: "1. which? [a | b]",
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
			if !reflect.DeepEqual(*got, tc.want) {
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

	// but plan carries the judge a stale task stops at
	r = &Run{Plan: "blocked", Ship: "pending", Leaf: "judge"}
	if got := r.PhaseText("plan"); got != "blocked at judge" {
		t.Errorf("PhaseText(plan) = %q, want blocked at judge", got)
	}
}

// A declared pipeline survives a save and load, sets the final step, and a
// new run forgets it.
func TestPipelineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := Fresh("pipe")

	if err := r.SetPipeline([]string{"implement", "review (local)", "judge → pr"}); err != nil {
		t.Fatal(err)
	}

	want := []string{"implement", "review (local)", "judge", "pr"}
	if !reflect.DeepEqual(r.Pipeline, want) || r.Final != "pr" {
		t.Fatalf("pipeline %q final %q, want %q final pr", r.Pipeline, r.Final, want)
	}

	if err := r.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := Load(Path(dir, "pipe"))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got.Pipeline, want) {
		t.Errorf("loaded pipeline %q, want %q", got.Pipeline, want)
	}

	got.Seed("ship", "GH-9", "")

	if got.Pipeline != nil || got.Final != "" {
		t.Errorf("Seed kept pipeline %q final %q", got.Pipeline, got.Final)
	}

	if err := r.SetPipeline(nil); err == nil {
		t.Error("an empty pipeline was accepted")
	}
}

// Show and `f10 status` print the same facts: every label Show prints must
// come back with the same value from the Run, so a field added to one fails
// here until the other carries it too.
func TestShowParity(t *testing.T) {
	r := Fresh("parity")

	// the sequence a ship run stopped by a judge verdict makes
	for _, err := range []error{
		r.SetTask("GH-26", "https://github.com/amberpixels/f10/issues/26"),
		r.SetFinal("pr"),
		r.Set("ship", "running", "judge"),
		r.Set("ship", "blocked", "judge"),
		r.SetNote("stop: the change patches the symptom", "move the retry into the client"),
		r.SetAsk("1. keep the retry? [no | yes]\n2. where? [client | server]"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	ours := map[string]string{
		"task":    r.Task,
		"url":     r.URL,
		"capture": r.PhaseText("capture"),
		"plan":    r.PhaseText("plan"),
		"ship":    r.PhaseText("ship"),
		"note":    r.Note,
		"next":    r.Next,
		"ask":     r.Ask,
		"final":   r.Final,
	}

	if want := "1. keep the retry? [no | yes] 2. where? [client | server]"; r.Ask != want {
		t.Errorf("ask = %q, want the newline folded: %q", r.Ask, want)
	}

	if want := "blocked at judge"; ours["ship"] != want {
		t.Errorf("ship = %q, want %q", ours["ship"], want)
	}

	var out strings.Builder
	r.Show(&out, time.Now())

	seen := 0

	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		label, rest, _ := strings.Cut(line, " ")
		value := strings.TrimSpace(rest)

		switch label {
		case "capture", "plan", "ship":
			// a glyph comes first; the words after it are the value
			_, value, _ = strings.Cut(value, " ")
		case "updated", "root":
			continue
		}

		want, known := ours[label]
		if !known {
			t.Errorf("show prints %q, which the run does not carry", label)

			continue
		}

		seen++

		if value != want {
			t.Errorf("%s: show %q, run %q", label, value, want)
		}
	}

	if seen != len(ours) {
		t.Errorf("show printed %d of the %d shared labels", seen, len(ours))
	}
}

// The status line runs bin/f10-state.sh render, a bash reader of the file
// this package writes. Each status must draw its own glyph, and a stopped
// run its step, exactly as the bash writer's files drew them.
func TestRenderParity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}

	script := filepath.Join(filepath.Dir(file), "..", "..", "..", "bin", "f10-state.sh")
	dir := t.TempDir()

	render := func(r *Run) string {
		t.Helper()

		if err := r.Save(dir, time.Now()); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(script, "render", r.Session)
		cmd.Env = append(os.Environ(), dirEnv+"="+dir, "NO_COLOR=1")

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("render: %v", err)
		}

		return string(out)
	}

	for _, tc := range []struct {
		run  Run
		want string
	}{
		{Run{Capture: "pending", Plan: "pending", Ship: "pending"}, " 󱊴 ○○○"},
		{Run{Capture: "running", Plan: "pending", Ship: "pending"}, " 󱊴 ◎○○"},
		{Run{Capture: "skipped", Plan: "done", Ship: "pending"}, " 󱊴 ◌●○"},
		{Run{Capture: "prior", Plan: "prior", Ship: "running", Leaf: "implement"}, " 󱊴 ◉◉◎"},
		{Run{Capture: "prior", Plan: "done", Ship: "failed", Leaf: "pr"}, " 󱊴 ◉●✗ pr"},
		{Run{Capture: "prior", Plan: "done", Ship: "partial", Leaf: "pr"}, " 󱊴 ◉●󰅚 pr"},
		{Run{Capture: "prior", Plan: "done", Ship: "blocked", Leaf: "judge", Skill: "ship", Ask: "1. go?"}, " 󱊴 ◉●󰜺 judge"},
	} {
		r := tc.run
		r.Session, r.Root = "render", "/work/acme"

		if got := render(&r); got != tc.want {
			t.Errorf("%+v renders %q, want %q", tc.run, got, tc.want)
		}
	}
}

func TestSetRejectsTypos(t *testing.T) {
	r := Fresh("s")

	for _, err := range []error{
		r.Set("shp", "running", ""),
		r.Set("ship", "runing", ""),
		r.SetTask("", ""),
		r.SetNote("", "x"),
		r.SetAsk(""),
		r.SetFinal(""),
	} {
		if _, ok := errors.AsType[*UsageError](err); !ok {
			t.Errorf("got %v, want a UsageError", err)
		}
	}
}

func TestSaveFlattensAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	r := Fresh("s")
	r.Root, r.Skill, r.Note = "/work/acme", "ship", "two\nlines"

	if err := r.Save(dir, time.Unix(1_800_000_000, 0)); err != nil {
		t.Fatal(err)
	}

	back, err := Load(Path(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}

	if back.Note != "two lines" || back.Skill != "ship" || back.Updated.Unix() != 1_800_000_000 {
		t.Errorf("read back %+v", back)
	}

	if matches, _ := filepath.Glob(filepath.Join(dir, "*.state.*")); len(matches) != 0 {
		t.Errorf("temp files left: %v", matches)
	}
}

// f10-inject.sh tells the model to run f10-load-conventions.sh, gated by a per-session
// marker. This pins the gate: a session's first skill is told, a later prompt
// is not, --help and the step-less skills never are, and a compaction tells
// again only where the session had them.
func TestInject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}

	bin := filepath.Join(filepath.Dir(file), "..", "..", "..", "bin")
	script := filepath.Join(bin, "f10-inject.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("f10-inject.sh not found: %v", err)
	}

	dir := t.TempDir()
	env := append(os.Environ(), dirEnv+"="+dir)

	inject := func(payload string) string {
		t.Helper()

		cmd := exec.Command(script)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(payload)

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("inject: %v\n%s", err, out)
		}

		if len(out) == 0 {
			return ""
		}

		var verdict struct {
			Out struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out, &verdict); err != nil {
			t.Fatalf("inject printed no JSON verdict: %v\n%s", err, out)
		}

		if verdict.Out.Event == "" || verdict.Out.Context == "" {
			t.Fatalf("inject: incomplete verdict\n%s", out)
		}

		return verdict.Out.Context
	}

	typed := func(sid, prompt, name, args string) string {
		return `{"session_id":"` + sid + `","prompt_id":"` + prompt + `","hook_event_name":"UserPromptExpansion","command_name":"` + name + `","command_args":"` + args + `"}`
	}
	called := func(sid, prompt, skill, args string) string {
		return `{"session_id":"` + sid + `","prompt_id":"` + prompt + `","hook_event_name":"PreToolUse","tool_name":"Skill","tool_input":{"skill":"` + skill + `","args":"` + args + `"}}`
	}
	started := func(sid, source string) string {
		return `{"session_id":"` + sid + `","hook_event_name":"SessionStart","source":"` + source + `"}`
	}

	// the first skill of a session is told to run f10-load-conventions.sh
	if got := inject(typed("s1", "p1", "f10:plan", "GH-1")); !strings.Contains(got, "f10-load-conventions.sh") {
		t.Errorf("first skill: got %q, want a line naming f10-load-conventions.sh", got)
	}

	// a later prompt in the same session, typed or model-invoked: the session holds them
	if got := inject(typed("s1", "p2", "f10:ship", "GH-1")); got != "" {
		t.Error("second prompt: injected again")
	}

	if got := inject(called("s1", "p3", "f10:ship", "GH-1 --driven")); got != "" {
		t.Error("second prompt via Skill: injected again")
	}

	// compaction dropped the text: back it comes, in this session only
	if got := inject(started("s1", "compact")); got == "" {
		t.Error("compact: not re-injected")
	}

	if got := inject(started("s1", "startup")); got != "" {
		t.Error("startup: injected")
	}

	if got := inject(started("s9", "compact")); got != "" {
		t.Error("compact in a session that never ran f10: injected")
	}

	// nothing to read the conventions: help, and the skills that only run the binary
	for name, payload := range map[string]string{
		"--help":         typed("s2", "p1", "f10:plan", "--help"),
		"status":         typed("s2", "p1", "f10:status", ""),
		"start":          called("s2", "p1", "f10:start", "GH-1"),
		"not a skill":    called("s2", "p1", "frontend", ""),
		"not Skill tool": `{"session_id":"s2","prompt_id":"p1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`,
	} {
		if got := inject(payload); got != "" {
			t.Errorf("%s: injected", name)
		}
	}

	// the model-invoked path injects for a session's first skill too
	if got := inject(called("s3", "p1", "plan", "GH-1")); got == "" {
		t.Error("first skill via Skill: not injected")
	}

	// a new run (seed) and a dropped badge (clear) are not a new context
	r := Fresh("s3")
	r.Seed("plan", "GH-2", dir)

	if err := r.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	Clear(dir, "s3")

	if got := inject(typed("s3", "p2", "f10:plan", "GH-2")); got != "" {
		t.Error("after seed and clear: injected again")
	}

	if _, err := os.Stat(filepath.Join(dir, "s3.conventions")); err != nil {
		t.Errorf("marker gone after seed and clear: %v", err)
	}
}

// A run seeded from a full task id names its task before fetch reports it,
// so the board attaches the run to the task's row from the first moment.
func TestSeedRecordsTheTask(t *testing.T) {
	for _, tc := range []struct {
		skill, args, want string
	}{
		{"plan", "FT-195", "FT-195"},
		{"ship", "ft-7 extra steering", "FT-7"},
		{"ship", "local: FT-3", "FT-3"},
		{"plan", "FT-1+FT-2", "FT-1"},
		{"plan", "FT-4 && /f10:ship FT-4 --driven", "FT-4"},
		{"ship", "plans/FT-5.md", "FT-5"},
		{"plan", "195", ""},
		{"plan", "https://github.com/acme/widgets/issues/9", ""},
		{"ship", "add a retry to the client", ""},
		{"capture", "FT-6 needs a retry", ""},
	} {
		t.Run(tc.skill+" "+tc.args, func(t *testing.T) {
			r := Fresh("s")
			r.Seed(tc.skill, tc.args, "")

			if r.Task != tc.want {
				t.Errorf("Task = %q, want %q", r.Task, tc.want)
			}
		})
	}
}
