package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/storage"
)

func write(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every kind a task keeps is listed by kind and name, never by path, and
// only a listed name is served.
func TestArtifactsListAndPick(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := storage.Root(dir)

	write(t, root.Plan("FT-9"), "# plan")
	write(t, filepath.Join(root.Spikes(), "FT-9.md"), "answer")
	write(t, filepath.Join(root.Handoffs(), "FT-9.md"), "handoff")
	write(
		t,
		filepath.Join(root.Verdicts("FT-9"), "1.md"),
		"task: FT-9\nsubject: plan FT-9\nverdict: proceed\n\n## Argument\n",
	)
	write(t, filepath.Join(root.Reviews("FT-9"), "2.md"), "findings")
	write(t, filepath.Join(root.Demo("FT-9"), "report.html"), "<img src=shots/a.png>")
	write(t, filepath.Join(root.Demo("FT-9"), "shots", "a.png"), "png")
	write(t, root.Plan("FT-10"), "another task")
	write(t, filepath.Join(dir, "secret.txt"), "outside every listing")

	files := listArtifacts(root, "FT-9")

	var got []string
	for _, f := range files {
		got = append(got, kindWord(f.GetKind())+":"+f.GetName()+":"+f.GetTitle())
	}

	want := "plan:FT-9:Plan spike:FT-9:Spike report handoff:FT-9:Handoff verdict:1:plan FT-9 " +
		"findings:2:Review round 2 demo:report.html:Demo report demo:shots/a.png:shots/a.png"
	if strings.Join(got, " ") != want {
		t.Errorf("listed\n  %s\nwant\n  %s", strings.Join(got, " "), want)
	}

	if f, err := pick(files, f10v1.ArtifactKind_ARTIFACT_KIND_DEMO, "shots/a.png"); err != nil ||
		f.GetContentType() != "image/png" {
		t.Errorf("pick a demo image = %v, %v", f.Artifact, err)
	}

	for _, name := range []string{"../secret.txt", "../../FT-10.md", "FT-10", "/etc/passwd"} {
		if _, err := pick(
			files,
			f10v1.ArtifactKind_ARTIFACT_KIND_DEMO,
			name,
		); connect.CodeOf(
			err,
		) != connect.CodeNotFound {
			t.Errorf("pick %q = %v, want NotFound", name, err)
		}
	}

	files[0].Size = MaxArtifact + 1
	if _, err := pick(
		files,
		f10v1.ArtifactKind_ARTIFACT_KIND_PLAN,
		"FT-9",
	); connect.CodeOf(
		err,
	) != connect.CodeResourceExhausted {
		t.Errorf("an oversized file = %v, want ResourceExhausted", err)
	}
}

// A task id that could leave the storage root is refused before any read.
func TestArtifactsRefuseAPathAsTask(t *testing.T) {
	t.Parallel()

	s := &artifactService{sup: &fakeSup{}}

	for _, task := range []string{"../plans", "", "a/b", ".hidden"} {
		if _, err := s.files(t.Context(), "/nowhere", task); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("task %q = %v, want InvalidArgument", task, err)
		}
	}
}
