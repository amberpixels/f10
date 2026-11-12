package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/target"
)

// Create links the body's After: line to the base task before the driver
// sees it, and still passes the base's number as --after.
func TestTaskCreateLinksTheAfterLine(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, `case "$1" in
read) echo "# FT-1041: Base [task]" ;;
url) echo https://tracker.example/1041 ;;
create) echo 1043 ;;
esac
`)

	out, _, err := runAppStdin(t, strings.NewReader("# Add the index\n\nbody\n\nAfter: FT-1041\n"), "-C",
		repo, "task", "create").streams()
	if err != nil || out != "FT-1043\n" {
		t.Fatalf("create = %q, %v", out, err)
	}

	storage := filepath.Join(repo, ".f10")
	if got, want := readFile(t, filepath.Join(storage, "stdin")),
		"# Add the index\n\nbody\n\nAfter: [FT-1041: Base \\[task\\]](https://tracker.example/1041)"; strings.TrimSpace(got) != want {
		t.Errorf("driver stdin = %q, want %q", got, want)
	}

	if got := readFile(t, filepath.Join(storage, "args")); got != "create --after 1041\n" {
		t.Errorf("driver args = %q", got)
	}

	// the new task answers from the ledger without the tracker
	k := ledger.LookupRefs(t.Context(), target.ProjectOf(t.Context(), repo), []string{"FT-1043", "FT-1041"})
	if k["FT-1043"].Title != "Add the index" || k["FT-1043"].URL != "https://tracker.example/1041" ||
		k["FT-1041"].URL != "https://tracker.example/1041" {
		t.Errorf("ledger refs = %+v", k)
	}
}

// The command prints a line per argument, in order: a known task linked, an
// unknown one bare with a note and exit 0, a bundle as typed.
func TestTaskRefCommand(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, `case "$1 $2" in
"read 109") echo "# FT-109: Base" ;;
"url 109") echo https://tracker.example/109 ;;
*) echo "no task $2" >&2; exit 1 ;;
esac
`)

	out, warn, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "ref", "--md", "FT-7", "109",
		"FT-1+FT-2").streams()
	if err != nil {
		t.Fatalf("ref: %v", err)
	}

	if want := "FT-7\n[FT-109: Base](https://tracker.example/109)\nFT-1+FT-2\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}

	if !strings.Contains(warn, "FT-7") || strings.Contains(warn, "FT-109") {
		t.Errorf("stderr = %q, want a note for FT-7 alone", warn)
	}

	out, _, err = runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "ref", "FT-109").streams()
	if err != nil || out != "FT-109 (Base)\n" {
		t.Errorf("plain ref = %q, %v", out, err)
	}
}

// `task report` prints the facts block: one task's rows, else a row per
// task in order, a task the tracker does not know bare, and never exit 3.
func TestTaskReportCommand(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, `case "$1 $2" in
"read 109") echo "# FT-109: Base" ;;
"url 109") echo https://tracker.example/109 ;;
"read 110") echo "# FT-110: Next" ;;
"url 110") echo https://tracker.example/110 ;;
*) echo "no task $2" >&2; exit 1 ;;
esac
`)

	out, _, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "report", "FT-109").streams()
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if want := "task:    FT-109\ntitle:   Base\nurl:   ↗ https://tracker.example/109\n"; out != want {
		t.Errorf("one task = %q, want %q", out, want)
	}

	out, _, err = runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "report", "FT-109", "110", "FT-7").streams()
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if want := "FT-109:  ↗ https://tracker.example/109\nFT-110:  ↗ https://tracker.example/110\nFT-7\n"; out != want {
		t.Errorf("several tasks = %q, want %q", out, want)
	}
}

// --children hangs the parent's open subtasks under it on tree lines, in
// After: order, each row with its title and url.
func TestTaskReportChildren(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := createRepo(t, `case "$1 $2" in
"read 71") echo "# FT-71: Parent" ;;
"url 71") echo https://tracker.example/71 ;;
"children 71") echo '[
  {"id":"72","title":"one","url":"https://tracker.example/72","state":"open"},
  {"id":"73","title":"two","url":"https://tracker.example/73","state":"open","after":"72"},
  {"id":"75","title":"done","url":"https://tracker.example/75","state":"closed"},
  {"id":"74","title":"three","url":"https://tracker.example/74","state":"open"}]' ;;
*) echo "no task $2" >&2; exit 1 ;;
esac
`)

	out, _, err := runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "report", "--children", "FT-71").streams()
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	want := "FT-71        Parent  ↗ https://tracker.example/71\n" +
		"├─ FT-72     one     ↗ https://tracker.example/72\n" +
		"│  └─ FT-73  two     ↗ https://tracker.example/73\n" +
		"└─ FT-74     three   ↗ https://tracker.example/74\n"
	if out != want {
		t.Errorf("children =\n%s\nwant\n%s", out, want)
	}
}
