package scope_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A board group lists repositories beside paths; a repository on an
// unknown host says which prefix to write; a missing group names the
// board's own file.
func TestBoardGroupsMixRepositories(t *testing.T) {
	home := testkit.IsolateEnv(t)

	root := testkit.Canonical(t, t.TempDir())
	testkit.ScopeRepo(t, root, "acme-api")

	testkit.WriteUserFile(
		t,
		home,
		scope.BoardConfigFile,
		"---\ngroups:\n  work: ["+root+"/acme-*, github.com/acme/widgets]\n"+
			"  odd: [git.example.com/acme/widgets]\n---\n",
	)

	items, err := scope.ReadGroups(scope.BoardConfigFile, []string{"work"})
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 2 || items[0].Dir != filepath.Join(root, "acme-api") || items[1].Repo == nil ||
		items[1].Repo.ID() != "github.com/acme/widgets" || items[1].Repo.CLI != "gh" || items[1].Repo.Prefix != "WID" {
		t.Errorf("items = %+v", items)
	}

	if _, err := scope.ReadGroups(
		scope.BoardConfigFile,
		[]string{"odd"},
	); err == nil ||
		!strings.Contains(err.Error(), "glab:") {
		t.Errorf("odd: err = %v, want the prefixes named", err)
	}

	if _, err := scope.ReadGroups(
		scope.BoardConfigFile,
		[]string{"play"},
	); err == nil ||
		!strings.Contains(err.Error(), scope.BoardConfigFile) {
		t.Errorf("play: err = %v, want %s named", err, scope.BoardConfigFile)
	}

	// standup's groups live in its own file: the board's are not read for it
	if _, err := scope.ReadGroups(scope.StandupConfigFile, []string{"work"}); err == nil ||
		!strings.Contains(err.Error(), scope.StandupConfigFile) {
		t.Errorf("standup: err = %v, want %s named", err, scope.StandupConfigFile)
	}
}

// A repository entry parses as a string or a map, and the map's prefix
// wins over the derived one.
func TestBoardRepos(t *testing.T) {
	home := testkit.IsolateEnv(t)

	testkit.WriteUserFile(t, home, scope.BoardConfigFile, "---\nrepos:\n  - github.com/acme/widgets\n"+
		"  - {repo: gitlab.com/acme/sub/widgets, prefix: wid}\n  - glab:git.example.com/acme/infra\n"+
		"people:\n  - {name: ann, tracker: Ann Lee, host: annl, emails: [ann@acme.example]}\n---\n")

	cfg, _, err := scope.ReadBoardConfig()
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	for _, r := range cfg.Repos {
		ref, err := scope.ParseRepo(r.Repo, r.Prefix)
		if err != nil {
			t.Fatal(err)
		}

		got = append(got, ref.CLI+" "+ref.ID()+" "+ref.Prefix+" "+ref.Repo())
	}

	want := "gh github.com/acme/widgets WID acme/widgets|glab gitlab.com/acme/sub/widgets WID acme/sub/widgets|" +
		"glab git.example.com/acme/infra INF git.example.com/acme/infra"
	if strings.Join(got, "|") != want {
		t.Errorf("repos = %q, want %q", strings.Join(got, "|"), want)
	}

	if len(cfg.People) != 1 || cfg.People[0].Host != "annl" || len(cfg.People[0].Emails) != 1 {
		t.Errorf("people = %+v", cfg.People)
	}

	testkit.WriteUserFile(t, home, scope.BoardConfigFile, "---\npeople:\n  - {host: annl}\n---\n")

	if _, _, err := scope.ReadBoardConfig(); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("err = %v, want the nameless person refused", err)
	}
}
