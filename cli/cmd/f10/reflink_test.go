package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// a tracker that knows FT-109 alone
const refTracker = `case "$1 $2" in
"read 109") echo "# FT-109: Base" ;;
"url 109") echo https://tracker.example/109 ;;
"read "*|"url "*) echo "no task $2" >&2; exit 1 ;;
*) echo https://tracker.example/c1 ;;
esac
`

const baseLink = "[FT-109: Base](https://tracker.example/109)"

// A comment reaches the tracker with the first mention of each known id
// linked, later ones and code left bare, and stderr naming what it linked.
func TestTaskCommentLinksFirstMentions(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, refTracker)

	_, warn, err := runAppStdin(t, strings.NewReader("Built on FT-109 and FT-7; `FT-109` again, FT-109.\n"), "-C",
		repo, "task", "comment", "FT-12").streams()
	if err != nil {
		t.Fatalf("comment: %v", err)
	}

	want := "Built on " + baseLink + " and FT-7; `FT-109` again, FT-109."
	if got := readFile(t, filepath.Join(repo, ".f10", "stdin")); got != want {
		t.Errorf("driver stdin = %q, want %q", got, want)
	}

	if !strings.Contains(warn, "linked FT-109") {
		t.Errorf("stderr = %q, want the linked id named", warn)
	}
}

// Create links the body, never the title line, which renders nothing.
func TestTaskCreateLinksTheBody(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	script := strings.Replace(refTracker, "*) echo https://tracker.example/c1", `"create "*) echo 1043`, 1)
	repo := createRepo(t, script)

	if _, _, err := runAppStdin(t, strings.NewReader("# Follow FT-109 up\n\nExtends FT-109.\n"), "-C", repo,
		"task", "create").streams(); err != nil {
		t.Fatalf("create: %v", err)
	}

	want := "# Follow FT-109 up\n\nExtends " + baseLink + "."
	if got := strings.TrimSpace(readFile(t, filepath.Join(repo, ".f10", "stdin"))); got != want {
		t.Errorf("driver stdin = %q, want %q", got, want)
	}
}

// check names each bare first mention it could link and fails. link fixes
// the file in place, so check then passes. HTML goes by its extension.
func TestRefsCheckAndLink(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, refTracker)
	md := filepath.Join(repo, "report.md")
	page := filepath.Join(repo, "report.html")

	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(md, "# Report\n\nShips FT-109 and FT-7.\n")
	write(page, `<html><head><title>FT-109</title></head><body><h1>FT-109 shipped</h1></body></html>`)

	out, warn, err := runApp(t, "-C", repo, "refs", "check", md).streams()
	if err == nil || out != md+":3: FT-109 is not linked\n" || !strings.Contains(warn, "FT-7") {
		t.Fatalf("check before = %q, %q, %v; want FT-109 named, FT-7 a note, exit 1", out, warn, err)
	}

	if out, _, err = runApp(t, "-C", repo, "refs", "link", md).streams(); err != nil || out != "FT-109\n" {
		t.Fatalf("link = %q, %v", out, err)
	}

	if got := readFile(t, md); got != "# Report\n\nShips "+baseLink+" and FT-7.\n" {
		t.Errorf("linked file = %q", got)
	}

	if _, _, err = runApp(t, "-C", repo, "refs", "check", md).streams(); err != nil {
		t.Errorf("check after link: %v", err)
	}

	if _, _, err = runApp(t, "-C", repo, "refs", "link", page).streams(); err != nil {
		t.Fatalf("link html: %v", err)
	}

	if got := readFile(t, page); got != `<html><head><title>FT-109</title></head><body><h1>`+
		`<a href="https://tracker.example/109">FT-109: Base</a> shipped</h1></body></html>` {
		t.Errorf("linked page = %q", got)
	}
}
