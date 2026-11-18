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
	"slices"
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
	Repros      = []string{"obvious", "full", "traced", "trusted", "skipped", "unconfirmed"}
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
func Attribute(ctx context.Context, in Input) *ledger.RegressionFoundPayload {
	p := &ledger.RegressionFoundPayload{
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

	a := attribution{in: in}

	p.OriginTask, p.Origin, p.Note = a.origin(ctx)
	if p.GetOriginTask() == "" {
		return p
	}

	c := a.classify(ctx, p.GetOriginTask())
	p.Class, p.Round, p.Finding = c.class, c.round, c.finding

	p.Note = c.note
	if c.unsure {
		p.Confidence = Unclear
	}

	return p
}

// Line is what the plan's `Regression of:` header and the command print.
func Line(p *ledger.RegressionFoundPayload) string {
	if p.GetOriginTask() == "" {
		if p.GetOrigin() == "" {
			return "none (" + p.GetNote() + ")"
		}

		return "none (" + p.GetOrigin() + ")"
	}

	return fmt.Sprintf("%s (%s, %s)", p.GetOriginTask(), p.GetClass(), p.GetConfidence())
}

type attribution struct {
	in Input
}

// origin is the task the commit belongs to (CommitTask), else whether it
// predates the project's trailers or was made outside the pipeline.
func (a attribution) origin(ctx context.Context) (string, string, string) {
	id, err := CommitTask(ctx, a.in.Dir, a.in.Prefix, a.in.Commit)
	if err != nil {
		return "", "", "reading the commit: " + firstLine(err.Error())
	}

	if id != "" {
		return id, OriginTask, ""
	}

	return "", a.untracked(ctx), ""
}

// CommitTask is the task sha belongs to: its Task: trailer, its Change:
// trailer, an unmerged branch holding it, or a subject that opens with an
// id; "" when none names one. Attribution and history share it, so they
// never disagree on which task owns a commit.
func CommitTask(ctx context.Context, dir, prefix, sha string) (string, error) {
	match := ref.Matcher(prefix)

	trailers, err := lore.CLIGit{Dir: dir}.Trailers(ctx, sha)
	if err != nil {
		return "", err
	}

	if id := TrailerTask(trailers[lore.TrailerTask], trailers[lore.TrailerChange]); id != "" {
		return id, nil
	}

	if id := branchTask(ctx, dir, sha, match); id != "" {
		return id, nil
	}

	return SubjectTask(prefix, gitx.Out(ctx, dir, "show", "-s", "--format=%s", sha, "--")), nil
}

// TrailerTask is the task a commit's trailers name: a bundle's lead from
// Task:, else the task a Change: names, its PR's .n cut off; "" for none.
// Every reader of commit trailers calls it, so none tells a different owner.
func TrailerTask(tasks, changes []string) string {
	if len(tasks) > 0 {
		return tasks[0]
	}

	if len(changes) > 0 {
		return ChangeTask(changes[0])
	}

	return ""
}

// ChangeTask is the task a Change: trailer's value names: `FT-9.2`, a
// task's second PR, is FT-9.
func ChangeTask(change string) string {
	id, _, _ := strings.Cut(strings.TrimSpace(change), ".")

	return id
}

// SubjectTask is the id a commit subject opens with, "" for none: the last
// rule CommitTask tries, for a caller that already holds the subject.
func SubjectTask(prefix, subject string) string {
	word, _, _ := strings.Cut(subject, " ")

	return ref.Matcher(prefix)(word)
}

// DefaultRef is the ref branchTask reads the default branch as.
func DefaultRef(ctx context.Context, dir string) string {
	if def := gitx.Out(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); def != "" {
		return def
	}

	return "HEAD"
}

// branchTask is the task of the closest branch holding the commit, looked
// at only while the default branch does not hold it: once merged, every
// later branch holds it too and names nothing.
func branchTask(ctx context.Context, dir, sha string, match func(string) string) string {
	if on, _ := gitx.IsAncestor(ctx, dir, sha, DefaultRef(ctx, dir)); on {
		return ""
	}

	out := gitx.Out(ctx, dir, "for-each-ref", "--contains", sha,
		"--format=%(refname:short)", "refs/heads", "refs/remotes")

	best, fewest := "", -1

	for name := range strings.FieldsSeq(out) {
		id := match(name)
		if id == "" {
			continue
		}

		n, err := strconv.Atoi(gitx.Out(ctx, dir, "rev-list", "--count", sha+".."+name))
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

// classified is a class with its finding and a note; unsure drops the
// row's confidence, since the class was read without the evidence it needs.
type classified struct {
	class, round, finding, note string
	unsure                      bool
}

// classify reads the origin task's rounds and verdicts against the lines.
// Each round is matched in its own numbering, because the file has usually
// moved between the review and the bug.
func (a attribution) classify(ctx context.Context, task string) classified {
	if a.in.Class == OutOfReach {
		return classified{class: OutOfReach}
	}

	series := a.rounds(task)
	blocks, note := a.faulty(ctx)
	unsure := note != ""

	held, unread, total := false, 0, 0

	for _, sr := range series {
		// ids restart at 1.1 in each PR's series, so a verdict is read in its own
		last := lastVerdicts(sr.files)

		for _, r := range sr.files {
			total++

			sha, rest, _ := strings.Cut(r.Reviewed, " ")
			dirty := strings.TrimSpace(rest) == "dirty"

			// a dirty round numbered the working tree that became the next commit
			at := sha
			if dirty {
				at = a.committedAfter(ctx, sha)
			}

			found, reachable, ambiguous := a.find(ctx, at, r.PR, blocks)
			if c, ok := skippedIn(r, a.rangesIn(found), last); ok {
				c.note, c.unsure = note, unsure
				if sr.archived != "" {
					c.note = joinNotes(note, "the finding is in the archived series "+sr.archived)
				}

				return c
			}

			switch {
			case dirty || !reachable || len(blocks) == 0 || ambiguous:
				unread++
			case len(found) == len(blocks):
				held = true
			}
		}
	}

	switch {
	case total == 0 && a.judgedProceed(task):
		return classified{class: JudgedProceed, note: note, unsure: unsure}
	case total == 0:
		return classified{class: Unreviewed, note: note, unsure: unsure}
	case held:
		return classified{class: Missed, note: note, unsure: unsure}
	case unread == total:
		return classified{class: Missed, unsure: true, note: joinNotes(note,
			"no round's reviewed lines could be read without doubt; classified from the findings alone")}
	}

	return classified{class: Unreviewed, note: note, unsure: unsure}
}

// reviewSeries is one PR's rounds, oldest first; archived names its
// archive directory, empty for the live one.
type reviewSeries struct {
	archived string
	files    []*findings.File
}

// rounds is every series of the task, live and archived, of the files that
// parse.
func (a attribution) rounds(task string) []reviewSeries {
	live := storage.Root(a.in.StorageRoot).Reviews(task)
	dirs := []string{live}

	// finish moves a PR's rounds to archive/<task>.<N>/
	archived, _ := filepath.Glob(filepath.Join(storage.ArchiveOf(filepath.Dir(live)), task+".*"))
	dirs = append(dirs, archived...)

	var out []reviewSeries

	for _, dir := range dirs {
		sr := reviewSeries{}
		if dir != live {
			sr.archived = filepath.Base(dir)
		}

		for _, path := range findings.Series(dir) {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			if f, err := findings.Parse(string(raw)); err == nil {
				sr.files = append(sr.files, f)
			}
		}

		if len(sr.files) > 0 {
			out = append(out, sr)
		}
	}

	return out
}

// committedAfter is the commit after sha on the way to the introducing
// commit: what a review of sha's dirty tree became.
func (a attribution) committedAfter(ctx context.Context, sha string) string {
	out, _, _ := gitx.Read(ctx, a.in.Dir, "rev-list", "--ancestry-path", "--reverse", sha+".."+a.in.Commit)
	if next, _, _ := strings.Cut(out, "\n"); next != "" {
		return next
	}

	return a.in.Commit
}

// judgedProceed reports a proceed from a judge that read the code: a plan
// or task judge covered no lines.
func (a attribution) judgedProceed(task string) bool {
	for _, path := range verdict.Series(storage.Root(a.in.StorageRoot).Verdicts(task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		v, err := verdict.Parse(string(raw))
		if err != nil || v.Verdict != "proceed" {
			continue
		}

		if kind, _, _ := strings.Cut(v.Subject, " "); kind != "plan" && kind != "task" && kind != "idea" {
			return true
		}
	}

	return false
}

// lastVerdicts is each finding's verdict under the latest block naming it.
func lastVerdicts(rounds []*findings.File) map[string]string {
	last := map[string]string{}

	for _, r := range rounds {
		for _, fd := range r.Findings {
			if fd.Verdict != "" {
				last[fd.ID] = fd.Verdict
			}
		}
	}

	return last
}

// skippedIn is a finding of r pointing at a faulty range whose last verdict
// is skip.
func skippedIn(r *findings.File, ranges []Range, last map[string]string) (classified, bool) {
	for _, fd := range r.Findings {
		if fd.Prior || !points(fd, ranges) || !strings.HasPrefix(last[fd.ID], "skip") {
			continue
		}

		rnd, _, _ := strings.Cut(fd.ID, ".")

		return classified{class: Skipped, round: rnd, finding: fd.ID}, true
	}

	return classified{}, false
}

func points(fd findings.Finding, ranges []Range) bool {
	path, line, ok := fd.Location()
	if !ok {
		return false
	}

	for _, r := range ranges {
		if path == r.Path && line >= r.Start-widen && line <= r.End+widen {
			return true
		}
	}

	return false
}

// rangesIn is each faulty range as the round numbered it where its block
// was found there, else as the base numbers it.
func (a attribution) rangesIn(found map[int]Range) []Range {
	out := make([]Range, len(a.in.Lines))
	for i, r := range a.in.Lines {
		if at, ok := found[i]; ok {
			r = at
		}

		out[i] = r
	}

	return out
}

// block is one faulty range's lines as the base has them, by index into
// Input.Lines.
type block struct {
	index    int
	path     string
	lines    []string
	repeated bool // the base has these lines elsewhere too, so content cannot place them
}

// faulty reads each range at the base; a range that cannot be read is left
// out and named in the note, since its class would otherwise be a guess.
func (a attribution) faulty(ctx context.Context) ([]block, string) {
	var (
		out    []block
		missed []string
	)

	for i, r := range a.in.Lines {
		lines, err := a.fileAt(ctx, a.in.Base, r.Path)
		if err != nil || r.End > len(lines) {
			missed = append(missed, r.String())

			continue
		}

		b := block{index: i, path: r.Path, lines: lines[r.Start-1 : r.End]}
		b.repeated = len(locate(lines, b.lines)) > 1
		out = append(out, b)
	}

	if len(missed) == 0 {
		return out, ""
	}

	return out, "cannot read " + strings.Join(missed, ", ") + " at " + a.in.Base
}

// find locates each block as a run of whole lines in the file at sha, by its
// range there; reachable is false when the clone lacks sha even after
// fetching its PR's head. A block that occurs more than once there is placed
// at the occurrence nearest its base position, and ambiguous is true, as it
// is for a block the base repeats, since the copy the review saw may not be
// the faulty one.
func (a attribution) find(ctx context.Context, sha string, pr int, blocks []block) (map[int]Range, bool, bool) {
	if sha == "" {
		return nil, false, false
	}

	if !a.present(ctx, sha) {
		if pr > 0 {
			_ = gitx.FetchRef(ctx, a.in.Dir, "pull/"+strconv.Itoa(pr)+"/head")
		}

		if !a.present(ctx, sha) {
			return nil, false, false
		}
	}

	found, ambiguous := map[int]Range{}, false

	for _, b := range blocks {
		lines, err := a.fileAt(ctx, sha, b.path)
		if err != nil {
			continue
		}

		starts := locate(lines, b.lines)
		if len(starts) == 0 {
			continue
		}

		if len(starts) > 1 || b.repeated {
			ambiguous = true
		}

		at := nearest(starts, a.in.Lines[b.index].Start)
		found[b.index] = Range{Path: b.path, Start: at, End: at + len(b.lines) - 1}
	}

	return found, true, ambiguous
}

// fileAt is path at rev, trailing blanks trimmed; a path rev lacks is an
// error, never an empty file.
func (a attribution) fileAt(ctx context.Context, rev, path string) ([]string, error) {
	out, ok, err := gitx.Read(ctx, a.in.Dir, "show", rev+":"+path)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, fmt.Errorf("%s has no %s", rev, path)
	}

	return trimmed(strings.Split(out, "\n")), nil
}

// locate is every 1-based line where want starts as whole lines of text.
func locate(text, want []string) []int {
	if len(want) == 0 {
		return nil
	}

	var out []int

	for i := 0; i+len(want) <= len(text); i++ {
		if slices.Equal(text[i:i+len(want)], want) {
			out = append(out, i+1)
		}
	}

	return out
}

// nearest is the start closest to line.
func nearest(starts []int, line int) int {
	best := starts[0]
	for _, s := range starts[1:] {
		if abs(s-line) < abs(best-line) {
			best = s
		}
	}

	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}

func joinNotes(notes ...string) string {
	var out []string

	for _, n := range notes {
		if n != "" {
			out = append(out, n)
		}
	}

	return strings.Join(out, "; ")
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
