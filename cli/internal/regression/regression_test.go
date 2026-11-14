package regression

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	os.Exit(m.Run())
}

type fixture struct {
	t       *testing.T
	dir     string
	storage string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	f := fixture{t: t, dir: t.TempDir(), storage: t.TempDir()}
	f.git("init", "-q", "-b", "main")
	f.git("config", "user.email", "dev@example.com")
	f.git("config", "user.name", "Dev")

	return f
}

func (f fixture) git(args ...string) string {
	f.t.Helper()

	cmd := exec.CommandContext(f.t.Context(), "git", args...)
	cmd.Dir = f.dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

func (f fixture) commit(msg, content string) string {
	f.t.Helper()

	if err := os.WriteFile(filepath.Join(f.dir, "calc.go"), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}

	f.git("add", "-A")
	f.git("commit", "-q", "-m", msg)

	return f.git("rev-parse", "HEAD")
}

// review writes round one of FT-414 on sha into dir; body is its findings.
func (f fixture) review(dir, sha, body string) {
	f.t.Helper()

	path := filepath.Join(f.storage, "reviews", dir, "1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}

	text := "task: FT-414\nround: 1\nreviewed: " + sha + "\nreviewer: local\nverdict: findings\n\n" +
		"## Categories\n\n- correctness and logic: 1\n\n## Findings\n\n" + body

	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f fixture) attribute(commit string) ledger.RegressionFoundPayload {
	return Attribute(f.t.Context(), Input{
		Dir: f.dir, StorageRoot: f.storage, Prefix: "FT",
		Commit: commit, Lines: []Range{{Path: "calc.go", Start: 3, End: 3}},
		FoundBy: "bisect", Confidence: "confirmed", Repro: "full",
	})
}

const (
	good = "package calc\n\nfunc Last(n int) int { return n - 1 }\n"
	bad  = "package calc\n\nfunc Last(n int) int { return n }\n"
)

func TestAttribute(t *testing.T) {
	t.Parallel()

	skipFinding := "### 1.1 Off by one in Last\nwhere: calc.go:3\ncategory: correctness and logic\n" +
		"verdict: skip\nreason: intended\n"
	otherFinding := "### 1.1 Naming\nwhere: calc.go:40\ncategory: correctness and logic\nverdict: fix\n"

	for name, tc := range map[string]struct {
		build func(f fixture) string // returns the guilty commit
		want  ledger.RegressionFoundPayload
	}{
		"missed: the round saw the line and no finding touched it": {
			build: func(f fixture) string {
				f.commit("init", good)
				sha := f.commit("change Last\n\nTask: FT-414", bad)
				f.review("FT-414", sha, otherFinding)

				return sha
			},
			want: ledger.RegressionFoundPayload{OriginTask: "FT-414", Origin: OriginTask, Class: Missed},
		},
		"skipped: a skipped finding pointed at the line": {
			build: func(f fixture) string {
				f.commit("init", good)
				sha := f.commit("change Last\n\nTask: FT-414", bad)
				f.review("FT-414", sha, skipFinding)

				return sha
			},
			want: ledger.RegressionFoundPayload{
				OriginTask: "FT-414", Origin: OriginTask, Class: Skipped, Round: "1", Finding: "1.1",
			},
		},
		"unreviewed: a commit after the round wrote the line": {
			build: func(f fixture) string {
				f.commit("init", good)
				reviewed := f.commit("touch\n\nTask: FT-414", good+"// note\n")
				f.review("FT-414", reviewed, otherFinding)

				return f.commit("resolve fix\n\nTask: FT-414", bad+"// note\n")
			},
			want: ledger.RegressionFoundPayload{OriginTask: "FT-414", Origin: OriginTask, Class: Unreviewed},
		},
		"archived rounds count too": {
			build: func(f fixture) string {
				f.commit("init", good)
				sha := f.commit("change Last\n\nTask: FT-414", bad)
				f.review(filepath.Join("archive", "FT-414.1"), sha, skipFinding)

				return sha
			},
			want: ledger.RegressionFoundPayload{
				OriginTask: "FT-414", Origin: OriginTask, Class: Skipped, Round: "1", Finding: "1.1",
			},
		},
		"a Change: trailer names the task": {
			build: func(f fixture) string {
				f.commit("init", good)

				return f.commit("change Last\n\nChange: FT-414.2", bad)
			},
			want: ledger.RegressionFoundPayload{OriginTask: "FT-414", Origin: OriginTask, Class: Unreviewed},
		},
		"a squash subject names the task": {
			build: func(f fixture) string {
				f.commit("init", good)

				return f.commit("FT-414: change Last (#9)", bad)
			},
			want: ledger.RegressionFoundPayload{OriginTask: "FT-414", Origin: OriginTask, Class: Unreviewed},
		},
		"pre-f10: older than the first Task: commit": {
			build: func(f fixture) string {
				sha := f.commit("init", bad)
				f.commit("later\n\nTask: FT-1", bad+"// x\n")

				return sha
			},
			want: ledger.RegressionFoundPayload{Origin: PreF10},
		},
		"outside-f10: no commit carries a task": {
			build: func(f fixture) string {
				return f.commit("init", bad)
			},
			want: ledger.RegressionFoundPayload{Origin: OutsideF10},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			sha := tc.build(f)
			got := f.attribute(sha)

			if got.OriginTask != tc.want.OriginTask || got.Origin != tc.want.Origin || got.Class != tc.want.Class ||
				got.Round != tc.want.Round || got.Finding != tc.want.Finding {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}

			if got.Commit != sha || got.Repro != "full" || got.Confidence != "confirmed" || got.FoundBy != "bisect" ||
				len(got.Lines) != 1 || got.Lines[0] != "calc.go:3-3" {
				t.Errorf("the agent's fields changed: %+v", got)
			}
		})
	}
}

// Only a judge that read the code covers its lines: a plan judge does not.
func TestJudgedProceedWithoutAReview(t *testing.T) {
	t.Parallel()

	for subject, want := range map[string]string{
		"pr #9":       JudgedProceed,
		"plan FT-414": Unreviewed,
	} {
		f := newFixture(t)
		f.commit("init", good)
		sha := f.commit("change Last\n\nTask: FT-414", bad)

		dir := filepath.Join(f.storage, "verdicts", "FT-414")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		verdict := "task: FT-414\nsubject: " + subject + "\nverdict: proceed\nreason: fine\nrun: ship before pr\n"
		if err := os.WriteFile(filepath.Join(dir, "1.md"), []byte(verdict), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := f.attribute(sha); got.Class != want {
			t.Errorf("%s: class %q, want %q", subject, got.Class, want)
		}
	}
}

// The file moved between the review and the bug: the skipped finding is
// matched where the round saw the line, not where the base has it.
func TestASkipIsMatchedInTheRoundsNumbering(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.commit("init", good)
	sha := f.commit("change Last\n\nTask: FT-414", bad)
	f.review("FT-414", sha, "### 1.1 Off by one\nwhere: calc.go:3\ncategory: correctness and logic\nverdict: skip\n")

	padding := strings.Repeat("// pad\n", 20)
	f.commit("later\n\nTask: FT-500", "package calc\n\n"+padding+"func Last(n int) int { return n }\n")

	got := Attribute(t.Context(), Input{
		Dir: f.dir, StorageRoot: f.storage, Prefix: "FT", Commit: sha,
		Lines: []Range{{Path: "calc.go", Start: 23, End: 23}}, FoundBy: "blame", Confidence: "likely", Repro: "full",
	})
	if got.Class != Skipped || got.Finding != "1.1" {
		t.Errorf("got %+v", got)
	}
}

// A dirty round read lines no sha holds, so it cannot say missed.
func TestADirtyRoundIsClassifiedFromTheFindings(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	parent := f.commit("init", good)
	sha := f.commit("change Last\n\nTask: FT-414", bad)
	f.review("FT-414", parent+" dirty", "")

	got := f.attribute(sha)
	if got.Class != Missed || got.Confidence != Unclear || got.Note == "" {
		t.Errorf("got %+v", got)
	}
}

// A faulty line found only inside a longer one is not held.
func TestHoldsMatchesWholeLines(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	reviewed := f.commit("init\n\nTask: FT-414", "package calc\n\nfunc Last(n int) int {\n\treturn n - 1\n}\n")
	f.review("FT-414", reviewed, "")
	sha := f.commit("fix\n\nTask: FT-414", "package calc\n\nfunc Last(n int) int {\n\treturn n\n}\n")

	got := Attribute(t.Context(), Input{
		Dir: f.dir, StorageRoot: f.storage, Prefix: "FT", Commit: sha,
		Lines: []Range{{Path: "calc.go", Start: 4, End: 4}}, FoundBy: "blame", Confidence: "likely", Repro: "full",
	})
	if got.Class != Unreviewed {
		t.Errorf("class %q, want %q", got.Class, Unreviewed)
	}
}

// A range the base cannot read leaves a note and an unclear confidence.
func TestAnUnreadableRangeIsNoted(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.commit("init", good)
	sha := f.commit("change Last\n\nTask: FT-414", bad)
	f.review("FT-414", sha, "")

	got := Attribute(t.Context(), Input{
		Dir: f.dir, StorageRoot: f.storage, Prefix: "FT", Commit: sha,
		Lines:   []Range{{Path: "cli/calc.go", Start: 3, End: 3}},
		FoundBy: "blame", Confidence: "confirmed", Repro: "full",
	})
	if got.Confidence != Unclear || !strings.Contains(got.Note, "cli/calc.go") {
		t.Errorf("got %+v", got)
	}
}

func TestOutOfReachIsTheAgentsCall(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.commit("init", good)
	sha := f.commit("change Last\n\nTask: FT-414", bad)
	f.review("FT-414", sha, "")

	got := Attribute(t.Context(), Input{
		Dir: f.dir, StorageRoot: f.storage, Prefix: "FT", Commit: sha, Class: OutOfReach,
		Lines: []Range{{Path: "calc.go", Start: 3, End: 3}}, FoundBy: "blame", Confidence: "likely", Repro: "full",
	})
	if got.Class != OutOfReach || got.OriginTask != "FT-414" {
		t.Errorf("got %+v", got)
	}
}

func TestAnUnreachableReviewedCommitFallsBackToTheFindings(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.commit("init", good)
	sha := f.commit("change Last\n\nTask: FT-414", bad)
	f.review("FT-414", "0123456789abcdef0123456789abcdef01234567", "")

	got := f.attribute(sha)
	if got.Class != Missed || got.Confidence != Unclear || got.Note == "" {
		t.Errorf("got %+v", got)
	}
}

func TestNoCommitStillRecordsTheRepro(t *testing.T) {
	t.Parallel()

	got := Attribute(t.Context(), Input{Dir: t.TempDir(), Repro: "unconfirmed", FoundBy: "blame", Confidence: "likely"})
	if got.FoundBy != "none" || got.Confidence != Unclear || got.Repro != "unconfirmed" || got.OriginTask != "" {
		t.Errorf("got %+v", got)
	}

	if line := Line(got); line != "none (no introducing commit found)" {
		t.Errorf("line %q", line)
	}
}

func TestAFindingPointsWithinThreeLines(t *testing.T) {
	t.Parallel()

	ranges := []Range{{Path: "calc.go", Start: 10, End: 12}}

	for where, want := range map[string]bool{
		"calc.go:7": true, "calc.go:6": false, "calc.go:15": true, "calc.go:16": false, "other.go:11": false,
	} {
		f := findingAt(where)
		if got := points(f, ranges); got != want {
			t.Errorf("%s: %v, want %v", where, got, want)
		}
	}
}

func TestParseRange(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]Range{
		"a/b.go:3-5": {"a/b.go", 3, 5},
		"a/b.go:7":   {"a/b.go", 7, 7},
	} {
		got, err := ParseRange(in)
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", in, got, err)
		}
	}

	for _, in := range []string{"a/b.go", "a/b.go:5-3", "a/b.go:0"} {
		if _, err := ParseRange(in); err == nil {
			t.Errorf("%s: no error", in)
		}
	}
}

func findingAt(where string) findings.Finding { return findings.Finding{ID: "1.1", Where: where} }
