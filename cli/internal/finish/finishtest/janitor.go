package finishtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// JanitorFixture is what every janitor test starts from: origin's HEAD is main, the
// main checkout is on main and nothing else holds a branch, and no branch
// records a dependent. Each test scripts its branches and requests.
type JanitorFixture struct {
	F  *testkit.Fakes
	In finish.JanitorInput
}

func NewJanitorFixture(t *testing.T) *JanitorFixture {
	t.Helper()

	main := t.TempDir()
	fx := &JanitorFixture{
		F:  testkit.NewFakes(t),
		In: finish.JanitorInput{Main: main, Prefix: "GH", Storage: storage.InRepo(main)},
	}

	h := hostops.NewHost("gh", main)
	fx.In.Host = &h

	f := fx.F
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.OriginURL, "git@x:a/b.git")
	f.Script(testkit.RemoteURL, "git@x:a/b.git")
	f.Script(testkit.Worktrees, testkit.Porcelain())

	f.Script(testkit.GhLogin, `{"login":"me"}`)

	return fx
}

// Branches scripts a local branch at sha ("" for none) and origin's copy at
// originSHA ("" for none); call it once per test with every branch.
func (fx *JanitorFixture) Branches(t *testing.T, bs ...[3]string) {
	t.Helper()

	var local, remote []string

	for _, b := range bs {
		if b[1] != "" {
			local = append(local, b[0])
			fx.F.Script("git rev-parse -q --verify refs/heads/"+b[0], b[1])
			fx.F.Script("git branch -D "+b[0], "")
		}

		if b[2] != "" {
			remote = append(remote, b[2]+"\trefs/heads/"+b[0])
			fx.F.Script(testkit.GhStackOf(b[0]), "[]")
			fx.F.Script("gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/"+b[0], "")
			fx.F.Script("git update-ref -d refs/remotes/origin/"+b[0], "")
		}

		fx.F.Script(testkit.GhStackOf(b[0]), "[]")
	}

	fx.F.Script(testkit.AllHeads, testkit.Heads(append(local, "main")...))
	fx.F.Script(testkit.LsHeads, strings.Join(remote, "\n"))
}

func (fx *JanitorFixture) Request(branch, state, sha, author string) {
	fx.F.Script(testkit.GhHeadOf(branch), `[{"number":9,"state":"`+state+`","url":"https://gh/pr/9","headRefName":"`+
		branch+`","headRefOid":"`+sha+`","author":{"login":"`+author+`"}}]`)
}

func (fx *JanitorFixture) Plan(t *testing.T) string {
	t.Helper()

	p := filepath.Join(fx.In.Storage.Plans(), "GH-1.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func (fx *JanitorFixture) Run(t *testing.T) string {
	t.Helper()

	lines, err := finish.Janitor(t.Context(), fx.In)
	if err != nil {
		t.Fatal(err)
	}

	return strings.Join(lines, "\n")
}
