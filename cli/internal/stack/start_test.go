package stack_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

func TestBranchName(t *testing.T) {
	cases := []struct {
		name     string
		driver   bool
		answer   shell.Result
		title    func(context.Context) (string, error)
		suffix   string
		want     string
		wantNote string
	}{
		{name: "no driver slugs the title", title: testkit.Titled("Add dark mode", nil), want: "GH-1/add-dark-mode"},
		{
			name:   "driver names the branch",
			driver: true,
			answer: shell.Result{Stdout: "GH-1/short-slug\n"},
			title:  testkit.Titled("unused", nil),
			want:   "GH-1/short-slug",
		},
		{
			name:   "driver declining slugs the title",
			driver: true,
			answer: shell.Result{Code: 3},
			title:  testkit.Titled("Add dark mode", nil),
			want:   "GH-1/add-dark-mode",
		},
		{
			name:   "suffix follows the driver's name",
			driver: true,
			answer: shell.Result{Stdout: "GH-1/slug"},
			suffix: "v2",
			want:   "GH-1/slug-v2",
		},
		{
			name:   "suffix follows the slug",
			title:  testkit.Titled("Add dark mode", nil),
			suffix: "v2",
			want:   "GH-1/add-dark-mode-v2",
		},
		{
			name:     "no title is the bare id, with the reason",
			title:    testkit.Titled("", errors.New("gh: offline")),
			want:     "GH-1",
			wantNote: "no title for GH-1 (gh: offline), so the branch is the bare id",
		},
		{
			name:     "a title with no slug in it is the bare id",
			title:    testkit.Titled("Исправить", nil),
			want:     "GH-1",
			wantNote: "the title of GH-1 yields no slug, so the branch is the bare id",
		},
		{
			name:     "no title source is the bare id",
			want:     "GH-1",
			wantNote: "no title source for GH-1, so the branch is the bare id",
		},
		{
			name:     "suffix follows the bare id",
			suffix:   "attempt2",
			want:     "GH-1-attempt2",
			wantNote: "no title source for GH-1, so the branch is the bare id",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := testkit.NewFakes(t)

			var d *trackerdriver.Driver
			if c.driver {
				d = testkit.FakeDriver(t)
				f.Fail("driver branch 1", c.answer)
			}

			got, note, err := stack.BranchName(t.Context(), d, testkit.Gh1, c.suffix, c.title)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("branchName = %q, want %q", got, c.want)
			}

			if note != c.wantNote {
				t.Errorf("note = %q, want %q", note, c.wantNote)
			}
		})
	}
}

func TestBranchNameDriverFailure(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Fail("driver branch 1", shell.Result{Code: 1, Stderr: "tracker down"})

	_, _, err := stack.BranchName(t.Context(), testkit.FakeDriver(t), testkit.Gh1, "", testkit.Titled("unused", nil))
	if err == nil || !strings.Contains(err.Error(), "tracker down") {
		t.Errorf("err = %v, want the driver's stderr", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ title, want string }{
		{title: "Add dark mode", want: "add-dark-mode"},
		{title: "  f10 start: --after <task-id> records a dependency!  ", want: "f10-start-after-task-id-records-a"},
		{title: "Fix (again) the CSV/XLSX export", want: "fix-again-the-csv-xlsx-export"},
		{
			title: "Default task ids from the project name, default branches as <ID>/<slug>",
			want:  "default-task-ids-from-the-project-name",
		},
		{title: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{title: "exactly-forty-characters-long-slug-here!", want: "exactly-forty-characters-long-slug-here"},
		{title: "Поддержка кириллицы", want: ""},
		{title: "---", want: ""},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			if got := stack.Slugify(c.title); got != c.want {
				t.Errorf("slugify(%q) = %q, want %q", c.title, got, c.want)
			}

			if len(c.want) > stack.SlugMax {
				t.Errorf("case expects %d characters, over the %d cut", len(c.want), stack.SlugMax)
			}
		})
	}
}

func TestNamedRef(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.ShaLocal, "aaa")

		got, sha, err := stack.NamedRef(t.Context(), "/code/repo", "release/1.2")
		if err != nil || got != "release/1.2" || sha != "aaa" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}

		if f.Called("git rev-parse --verify --quiet refs/remotes") {
			t.Error("remotes were searched although a local branch matched")
		}
	})

	t.Run("a remote typed as such is taken literally", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Fail("git rev-parse --verify --quiet refs/heads/origin/main", testkit.Missing)
		f.Script("git rev-parse --verify --quiet refs/remotes/origin/main", "bbb")

		got, sha, err := stack.NamedRef(t.Context(), "/code/repo", "origin/main")
		if err != nil || got != "origin/main" || sha != "bbb" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Fail(testkit.ShaLocal, testkit.Missing)
		f.Fail(testkit.ShaRemote, testkit.Missing)
		f.Script(testkit.ShaOrigin, "ccc")

		got, sha, err := stack.NamedRef(t.Context(), "/code/repo", "release/1.2")
		if err != nil || got != "origin/release/1.2" || sha != "ccc" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}
	})

	t.Run("nothing anywhere fails naming the branch", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Fail(testkit.ShaLocal, testkit.Missing)
		f.Fail(testkit.ShaRemote, testkit.Missing)
		f.Fail(testkit.ShaOrigin, testkit.Missing)

		_, _, err := stack.NamedRef(t.Context(), "/code/repo", "release/1.2")
		if err == nil || !strings.Contains(err.Error(), "release/1.2") {
			t.Errorf("err = %v, want the branch named", err)
		}
	})
}

func TestExistingBranch(t *testing.T) {
	cases := []struct {
		name    string
		refs    string
		heads   string // every local branch, read when refs finds none
		linked  string // the branches' f10-task links, as git config lists them
		want    string
		suffix  string
		got     string
		wantErr bool
	}{
		{name: "none", refs: "", want: "GH-1", got: ""},
		{name: "the intended name wins", refs: testkit.Heads("GH-1-attempt2", "GH-1"), want: "GH-1", got: "GH-1"},
		{
			name: "one task branch is reused whatever its shape",
			refs: testkit.Heads("GH-1/old-slug"),
			want: "GH-1/new-slug",
			got:  "GH-1/old-slug",
		},
		{
			name:   "a suffix narrows",
			refs:   testkit.Heads("GH-1", "GH-1-attempt2", "GH-1/slug-attempt2"),
			want:   "GH-1-attempt2",
			suffix: "attempt2",
			got:    "GH-1-attempt2",
		},
		{
			name:   "a suffix with no match creates",
			refs:   testkit.Heads("GH-1", "GH-1-attempt2"),
			want:   "GH-1-v3",
			suffix: "v3",
			got:    "",
		},
		{
			name:    "several without the intended name",
			refs:    testkit.Heads("GH-1/a", "GH-1/b"),
			want:    "GH-1",
			wantErr: true,
		},
		{
			name:  "the id past a prefix",
			heads: testkit.Heads("main", "feat/GH-1-workdays"),
			want:  "GH-1/workdays",
			got:   "feat/GH-1-workdays",
		},
		{
			name:  "a longer id is another task",
			heads: testkit.Heads("feat/GH-10-other", "fix/gh-12"),
			want:  "GH-1",
			got:   "",
		},
		{
			name:  "the strict shapes win over a prefixed branch",
			refs:  testkit.Heads("GH-1/slug"),
			heads: testkit.Heads("GH-1/slug", "feat/GH-1-old"),
			want:  "GH-1/other",
			got:   "GH-1/slug",
		},
		{
			name:   "a prefixed branch linked to another task",
			heads:  testkit.Heads("feat/GH-1-followup"),
			linked: "feat/GH-1-followup", // linked to GH-5
			want:   "GH-1",
			got:    "",
		},
		{
			name:   "a suffix narrows prefixed branches",
			heads:  testkit.Heads("feat/GH-1-a", "feat/GH-1-a-attempt2"),
			want:   "GH-1-attempt2",
			suffix: "attempt2",
			got:    "feat/GH-1-a-attempt2",
		},
		{
			name:    "several prefixed branches",
			heads:   testkit.Heads("feat/GH-1-a", "fix/GH-1-b"),
			want:    "GH-1",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			f.Script(testkit.RefsLocal, c.refs)
			f.Script(testkit.AllHeads, c.heads)

			if c.linked != "" {
				f.Meta.Seed(c.linked, branchmeta.Task, "GH-5")
			}

			got, err := testkit.IndexOf(t).Find(t.Context(), c.want, "GH-1", c.suffix, stack.PolicyLocalOrOrigin)
			if c.wantErr {
				if err == nil {
					t.Fatalf("find = %+v, want an error", got)
				}

				for name := range strings.FieldsSeq(strings.ReplaceAll(c.refs+c.heads, "refs/heads/", "")) {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("err = %v, want %s named", err, name)
					}
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got.Name != c.got || got.Remote {
				t.Errorf("find = %+v, want %q, local", got, c.got)
			}
		})
	}
}
