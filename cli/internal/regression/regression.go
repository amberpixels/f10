// Package regression traces a bug task's root cause to the task that shipped
// it, and classifies how that task's review or judge let the fault through.
// The agent picks the commit and the lines; everything here is a lookup, and
// a lookup that fails leaves a note instead of an error, because attribution
// never stops a run.
package regression

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// Classes, Origins and the rest of the payload's words.
const (
	Missed        = "missed"
	Skipped       = "skipped"
	Unreviewed    = "unreviewed"
	JudgedProceed = "judged-proceed"
	OutOfReach    = "out-of-reach"

	OriginTask = "task"
	PreF10     = "pre-f10"
	OutsideF10 = "outside-f10"

	Unclear = "unclear"
)

// Values each flag takes, for the command to check.
var (
	FoundBy     = []string{"bisect", "blame", "none"}
	Confidences = []string{"confirmed", "likely", Unclear}
	Repros      = []string{"obvious", "full", "skipped", "unconfirmed"}
)

// widen is how far a finding's line may sit outside a faulty range and
// still point at it: reviewers cite a function's first line or the one above.
const widen = 3

// Range is lines Start to End of Path, both included, in Base's numbering.
type Range struct {
	Path       string
	Start, End int
}

var rangeRE = regexp.MustCompile(`^(.+):(\d+)(?:-(\d+))?$`)

// ParseRange reads `path:start-end` or `path:line`.
func ParseRange(s string) (Range, error) {
	m := rangeRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Range{}, fmt.Errorf("%q: want path:start-end", s)
	}

	start, _ := strconv.Atoi(m[2])
	end := start

	if m[3] != "" {
		end, _ = strconv.Atoi(m[3])
	}

	if start < 1 || end < start {
		return Range{}, fmt.Errorf("%q: want 1 <= start <= end", s)
	}

	return Range{Path: m[1], Start: start, End: end}, nil
}

func (r Range) String() string { return fmt.Sprintf("%s:%d-%d", r.Path, r.Start, r.End) }

// Input is what the agent settled: the introducing commit, the faulty lines
// at Base, how it found them, and the repro outcome of the debug pass.
type Input struct {
	Dir         string // the repository
	StorageRoot string
	Prefix      string // the tracker's id prefix
	Base        string // the revision the lines are numbered in; HEAD when empty
	Commit      string // empty when none was found
	Lines       []Range
	FoundBy     string
	Confidence  string
	Repro       string
	Class       string // only OutOfReach, which a reading of the code decides
}

// Attribute is the regression_found payload for in.
func Attribute(ctx context.Context, in Input) ledger.RegressionFoundPayload {
	p := ledger.RegressionFoundPayload{
		Commit:     in.Commit,
		FoundBy:    in.FoundBy,
		Confidence: in.Confidence,
		Repro:      in.Repro,
	}

	for _, r := range in.Lines {
		p.Lines = append(p.Lines, r.String())
	}

	if in.Base == "" {
		in.Base = "HEAD"
	}

	if in.Commit == "" {
		p.FoundBy, p.Confidence = "none", Unclear
		p.Note = "no introducing commit found"

		return p
	}

	a := attribution{in: in, git: lore.CLIGit{Dir: in.Dir}}

	p.OriginTask, p.Origin, p.Note = a.origin(ctx)
	if p.OriginTask == "" {
		return p
	}

	c := a.classify(ctx, p.OriginTask)
	p.Class, p.Round, p.Finding = c.class, c.round, c.finding

	if c.note != "" {
		p.Note = c.note
		p.Confidence = Unclear
	}

	return p
}

// Line is what the plan's `Regression of:` header and the command print.
func Line(p ledger.RegressionFoundPayload) string {
	if p.OriginTask == "" {
		if p.Origin == "" {
			return "none (" + p.Note + ")"
		}

		return "none (" + p.Origin + ")"
	}

	return fmt.Sprintf("%s (%s, %s)", p.OriginTask, p.Class, p.Confidence)
}

type attribution struct {
	in  Input
	git lore.CLIGit
}

// origin is the task the commit belongs to: its Task: trailer, its Change:
// trailer, an unmerged branch holding it, or a subject that opens with an id.
func (a attribution) origin(ctx context.Context) (string, string, string) {
	match := ref.Matcher(a.in.Prefix)

	trailers, err := a.git.Trailers(ctx, a.in.Commit)
	if err != nil {
		return "", "", "reading the commit: " + firstLine(err.Error())
	}

	if ids := trailers[lore.TrailerTask]; len(ids) > 0 {
		return ids[0], OriginTask, ""
	}

	if ids := trailers[lore.TrailerChange]; len(ids) > 0 {
		id, _, _ := strings.Cut(ids[0], ".")

		return id, OriginTask, ""
	}

	if id := a.branchTask(ctx, match); id != "" {
		return id, OriginTask, ""
	}

	subject := gitx.Out(ctx, a.in.Dir, "show", "-s", "--format=%s", a.in.Commit, "--")
	if word, _, _ := strings.Cut(subject, " "); match(word) != "" {
		return match(word), OriginTask, ""
	}

	return "", a.untracked(ctx), ""
}

// branchTask is the task of the closest branch holding the commit, looked
// at only while the default branch does not hold it: once merged, every
// later branch holds it too and names nothing.
func (a attribution) branchTask(ctx context.Context, match func(string) string) string {
	def := gitx.Out(ctx, a.in.Dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if def == "" {
		def = "HEAD"
	}

	if on, _ := gitx.IsAncestor(ctx, a.in.Dir, a.in.Commit, def); on {
		return ""
	}

	out := gitx.Out(ctx, a.in.Dir, "for-each-ref", "--contains", a.in.Commit,
		"--format=%(refname:short)", "refs/heads", "refs/remotes")

	best, fewest := "", -1

	for name := range strings.FieldsSeq(out) {
		id := match(name)
		if id == "" {
			continue
		}

		n, err := strconv.Atoi(gitx.Out(ctx, a.in.Dir, "rev-list", "--count", a.in.Commit+".."+name))
		if err == nil && (fewest < 0 || n < fewest) {
			best, fewest = id, n
		}
	}

	return best
}

// untracked tells a commit from before the project used task trailers from
// one made outside the pipeline since; a repo with none is all outside.
func (a attribution) untracked(ctx context.Context) string {
	out := gitx.Out(ctx, a.in.Dir, "log", "--reverse", "--format=%H", "-i", "--grep=^Task: ", a.in.Base, "--")

	first, _, _ := strings.Cut(out, "\n")
	if first == "" {
		return OutsideF10
	}

	if before, _ := gitx.IsAncestor(ctx, a.in.Dir, a.in.Commit, first); before && !gitx.SameCommit(a.in.Commit, first) {
		return PreF10
	}

	return OutsideF10
}

type classified struct {
	class, round, finding, note string
}

// classify reads the origin task's rounds and verdicts against the lines.
func (a attribution) classify(ctx context.Context, task string) classified {
	if a.in.Class == OutOfReach {
		return classified{class: OutOfReach}
	}

	rounds := a.rounds(task)

	if c, ok := a.skipped(rounds); ok {
		return c
	}

	if len(rounds) == 0 {
		if a.judgedProceed(task) {
			return classified{class: JudgedProceed}
		}

		return classified{class: Unreviewed}
	}

	faulty := a.faultyText(ctx)
	unreachable := 0

	for _, r := range rounds {
		sha, _, _ := strings.Cut(r.Reviewed, " ")

		held, ok := a.holds(ctx, sha, r.PR, faulty)
		if !ok {
			unreachable++

			continue
		}

		if held {
			return classified{class: Missed}
		}
	}

	if unreachable == len(rounds) {
		return classified{class: Missed, note: "the reviewed commit is unreachable; classified from the findings alone"}
	}

	return classified{class: Unreviewed}
}

// rounds is every findings file of the task, live and archived, that parses.
func (a attribution) rounds(task string) []*findings.File {
	live := storage.Root(a.in.StorageRoot).Reviews(task)
	dirs := []string{live}

	// finish moves a PR's rounds to archive/<task>.<N>/
	archived, _ := filepath.Glob(filepath.Join(storage.ArchiveOf(filepath.Dir(live)), task+".*"))
	dirs = append(dirs, archived...)

	var out []*findings.File

	for _, dir := range dirs {
		for _, path := range findings.Series(dir) {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			if f, err := findings.Parse(string(raw)); err == nil {
				out = append(out, f)
			}
		}
	}

	return out
}

// judgedProceed reports a judge verdict of proceed on the task.
func (a attribution) judgedProceed(task string) bool {
	for _, path := range verdict.Series(storage.Root(a.in.StorageRoot).Verdicts(task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		if v, err := verdict.Parse(string(raw)); err == nil && v.Verdict == "proceed" {
			return true
		}
	}

	return false
}

// skipped is a finding pointing at a faulty range whose last verdict is skip.
func (a attribution) skipped(rounds []*findings.File) (classified, bool) {
	last := map[string]string{}

	var pointing []findings.Finding

	for _, r := range rounds {
		for _, fd := range r.Findings {
			if fd.Verdict != "" {
				last[fd.ID] = fd.Verdict
			}

			if !fd.Prior && a.points(fd) {
				pointing = append(pointing, fd)
			}
		}
	}

	for _, fd := range pointing {
		if strings.HasPrefix(last[fd.ID], "skip") {
			rnd, _, _ := strings.Cut(fd.ID, ".")

			return classified{class: Skipped, round: rnd, finding: fd.ID}, true
		}
	}

	return classified{}, false
}

func (a attribution) points(fd findings.Finding) bool {
	path, line, ok := fd.Location()
	if !ok {
		return false
	}

	for _, r := range a.in.Lines {
		if path == r.Path && line >= r.Start-widen && line <= r.End+widen {
			return true
		}
	}

	return false
}

// faultyText is each range's lines as Base has them.
func (a attribution) faultyText(ctx context.Context) map[string][]string {
	out := map[string][]string{}

	for _, r := range a.in.Lines {
		lines := strings.Split(gitx.Out(ctx, a.in.Dir, "show", a.in.Base+":"+r.Path), "\n")
		if r.End > len(lines) {
			continue
		}

		out[r.Path] = append(out[r.Path], strings.Join(trimmed(lines[r.Start-1:r.End]), "\n"))
	}

	return out
}

// holds reports whether the file at sha carries every faulty block; ok is
// false when sha is not in the clone even after fetching its PR's head.
func (a attribution) holds(ctx context.Context, sha string, pr int, faulty map[string][]string) (bool, bool) {
	if sha == "" {
		return false, false
	}

	if !a.present(ctx, sha) {
		if pr > 0 {
			_ = gitx.FetchRef(ctx, a.in.Dir, "pull/"+strconv.Itoa(pr)+"/head")
		}

		if !a.present(ctx, sha) {
			return false, false
		}
	}

	if len(faulty) == 0 {
		return false, true
	}

	for path, blocks := range faulty {
		text := strings.Join(trimmed(strings.Split(gitx.Out(ctx, a.in.Dir, "show", sha+":"+path), "\n")), "\n")

		for _, b := range blocks {
			if !strings.Contains(text, b) {
				return false, true
			}
		}
	}

	return true, true
}

func (a attribution) present(ctx context.Context, sha string) bool {
	got, err := gitx.Ref(ctx, a.in.Dir, sha+"^{commit}")

	return err == nil && got != ""
}

// trimmed drops trailing blanks, which an editor or a formatter moves
// without changing the code.
func trimmed(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(l, " \t\r")
	}

	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}
