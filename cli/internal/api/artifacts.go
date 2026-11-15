package api

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// MaxArtifact is the largest file GetArtifact sends, since a unary message
// carries the whole file.
const MaxArtifact = 8 << 20

// artifactTask is a task id safe to join into a storage path.
var artifactTask = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type artifactService struct{ sup Supervisor }

func (s *artifactService) ListArtifacts(
	ctx context.Context,
	req *connect.Request[f10v1.ListArtifactsRequest],
) (*connect.Response[f10v1.ListArtifactsResponse], error) {
	files, err := s.files(ctx, req.Msg.GetProject(), req.Msg.GetTask())
	if err != nil {
		return nil, err
	}

	out := make([]*f10v1.Artifact, len(files))
	for i, f := range files {
		out[i] = f.Artifact
	}

	return connect.NewResponse(&f10v1.ListArtifactsResponse{Artifacts: out}), nil
}

func (s *artifactService) GetArtifact(
	ctx context.Context,
	req *connect.Request[f10v1.GetArtifactRequest],
) (*connect.Response[f10v1.GetArtifactResponse], error) {
	m := req.Msg

	files, err := s.files(ctx, m.GetProject(), m.GetTask())
	if err != nil {
		return nil, err
	}

	f, err := pick(files, m.GetKind(), m.GetName())
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(f.path)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("artifact %s: %w", m.GetName(), err))
	}

	return connect.NewResponse(&f10v1.GetArtifactResponse{Artifact: f.Artifact, Content: content}), nil
}

// pick is the listed file of kind named name: only a listed file is served,
// so no name reaches outside the listing.
func pick(files []artifactFile, kind f10v1.ArtifactKind, name string) (artifactFile, error) {
	for _, f := range files {
		if f.GetKind() != kind || f.GetName() != name {
			continue
		}

		if f.GetSize() > MaxArtifact {
			return artifactFile{}, connect.NewError(connect.CodeResourceExhausted,
				fmt.Errorf("%s is %d bytes, over the %d a call carries", name, f.GetSize(), MaxArtifact))
		}

		return f, nil
	}

	return artifactFile{}, connect.NewError(connect.CodeNotFound,
		fmt.Errorf("the task has no %s artifact named %q", kindWord(kind), name))
}

// artifactFile is an artifact with the path it is read from, which never
// leaves the daemon.
type artifactFile struct {
	*f10v1.Artifact

	path string
}

func (s *artifactService) files(ctx context.Context, project, task string) ([]artifactFile, error) {
	if !artifactTask.MatchString(task) {
		return nil, invalid(fmt.Sprintf("task %q is not a task id", task))
	}

	t, err := projectTarget(ctx, s.sup, project, "artifacts")
	if err != nil {
		return nil, err
	}

	return listArtifacts(t.Layout.StorageRoot, task), nil
}

// listArtifacts is every file root keeps for task, in a fixed order: plan,
// spike report, handoff, verdicts, findings rounds, then the demo's files.
func listArtifacts(root storage.Root, task string) []artifactFile {
	if root == "" {
		return nil
	}

	var out []artifactFile

	add := func(kind f10v1.ArtifactKind, name, title, path string) {
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			return
		}

		out = append(out, artifactFile{path: path, Artifact: &f10v1.Artifact{
			Kind:        kind,
			Name:        name,
			Title:       title,
			Size:        int32(min(fi.Size(), 1<<31-1)), //nolint:gosec // clamped
			Modified:    timestamppb.New(fi.ModTime()),
			ContentType: contentType(path),
		}})
	}

	add(f10v1.ArtifactKind_ARTIFACT_KIND_PLAN, task, "Plan", root.Plan(task))
	add(f10v1.ArtifactKind_ARTIFACT_KIND_SPIKE, task, "Spike report", filepath.Join(root.Spikes(), task+".md"))
	add(f10v1.ArtifactKind_ARTIFACT_KIND_HANDOFF, task, "Handoff", filepath.Join(root.Handoffs(), task+".md"))

	for _, p := range storage.Numbered(root.Verdicts(task)) {
		n := strings.TrimSuffix(filepath.Base(p), ".md")
		add(f10v1.ArtifactKind_ARTIFACT_KIND_VERDICT, n, cmp.Or(headerValue(p, "subject"), "Verdict "+n), p)
	}

	for _, p := range storage.Numbered(root.Reviews(task)) {
		n := strings.TrimSuffix(filepath.Base(p), ".md")
		add(f10v1.ArtifactKind_ARTIFACT_KIND_FINDINGS, n, "Review round "+n, p)
	}

	demo := root.Demo(task)

	_ = filepath.WalkDir(demo, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is left out, not an error
		}

		rel, err := filepath.Rel(demo, p)
		if err != nil {
			return nil //nolint:nilerr // outside the dir: left out
		}

		name := filepath.ToSlash(rel)
		title := name

		if name == "report.html" {
			title = "Demo report"
		}

		add(f10v1.ArtifactKind_ARTIFACT_KIND_DEMO, name, title, p)

		return nil
	})

	return out
}

// headerValue is a `key: value` line from the header a verdict file opens
// with, "" when the header has none.
func headerValue(file, key string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			return ""
		}

		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

func contentType(p string) string {
	switch ext := strings.ToLower(path.Ext(p)); ext {
	case ".md":
		return "text/markdown; charset=utf-8"
	case "":
		return "application/octet-stream"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}

		return "application/octet-stream"
	}
}

func kindWord(k f10v1.ArtifactKind) string {
	return strings.ToLower(strings.TrimPrefix(k.String(), "ARTIFACT_KIND_"))
}
