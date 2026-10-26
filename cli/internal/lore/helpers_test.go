package lore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRepo runs git in a temp repository with a fixed identity and dates,
// so commit shas, and the golden output naming them, are stable.
type gitRepo struct {
	t   *testing.T
	dir string
}

const commitDate = "2026-10-20T08:00:00Z"

func newRepo(t *testing.T) gitRepo {
	t.Helper()

	r := gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "dev@example.com")
	r.git("config", "user.name", "Dev")

	return r
}

func (r gitRepo) git(args ...string) string {
	r.t.Helper()

	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+commitDate, "GIT_COMMITTER_DATE="+commitDate,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")

	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// commit writes the files and commits them with msg, returning the sha.
func (r gitRepo) commit(msg string, files map[string]string) string {
	r.t.Helper()

	for name, content := range files {
		path := filepath.Join(r.dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}

	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)

	return r.git("rev-parse", "HEAD")
}

// counter is a deterministic entropy source: each read differs.
type counter byte

func (c *counter) Read(p []byte) (int, error) {
	for i := range p {
		*c++
		p[i] = byte(*c)
	}

	return len(p), nil
}

func testStore(t *testing.T, root string, git Git) *Store {
	t.Helper()

	return &Store{Root: root, Git: git, Entropy: new(counter)}
}

func at(s string) time.Time {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}

	return tm
}

// add writes e or fails the test.
func (s *Store) mustAdd(t *testing.T, e Entry) string {
	t.Helper()

	id, err := s.Add(t.Context(), e)
	if err != nil {
		t.Fatalf("add %s: %v", e.Kind, err)
	}

	return id
}

func intent(change, when string) Entry {
	return Entry{Kind: KindIntent, Change: change, Human: "dev@example.com", At: at(when), Body: "Why we do " + change}
}

func finding(change, when, severity, status string, anchors ...Anchor) Entry {
	e := Entry{
		Kind: KindFinding, Change: change, Human: "rev@example.com", At: at(when),
		Severity: severity, Status: status, Anchors: anchors, Body: severity + " " + status,
	}
	if status != "open" {
		e.Resolution = "abcdef1"
	}

	return e
}

func verdictEntry(change, when, outcome string) Entry {
	return Entry{
		Kind: KindVerdict, Change: change, Human: "dev@example.com", At: at(when),
		Outcome: outcome, Gate: "judge", Commit: "abcdef1", Body: "judged " + outcome,
	}
}

func lesson(change, when string, anchors ...Anchor) Entry {
	return Entry{
		Kind:    KindLesson,
		Change:  change,
		Human:   "dev@example.com",
		At:      at(when),
		Anchors: anchors,
		Body:    "learned",
	}
}

func anchorAt(path string, line, end int, commit, symbol string) Anchor {
	return Anchor{Path: path, Line: line, EndLine: end, Commit: commit, Symbol: symbol}
}
