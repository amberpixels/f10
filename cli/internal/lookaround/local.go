package lookaround

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// LocalInputs is where this machine keeps what the local collector reads.
type LocalInputs struct {
	Main       string // the main checkout
	StateDir   string // run state files
	TTL        time.Duration
	SharedRoot string           // the storage root every checkout shares; "" reads each checkout's .f10
	LedgerPath string           // "" reads no ledger
	ProjectID  string           // the ledger's project id
	Agents     agentctl.Backend // the agent backend, when reachable: read its workspaces; nil reads none
}

// collectLocal reads the worktrees and branches, the live runs in them,
// every plan, findings and verdict file under the roots, what the ledger
// closed, and Herdr's workspaces. Git failing fails the source; a file or
// the ledger that cannot be read is a note. prefix spells a branch's task
// from its name where nothing links one.
func collectLocal(ctx context.Context, in LocalInputs, prefix string, now time.Time) (*Local, []string, error) {
	l := &Local{
		Branches:   map[string]Branch{},
		Plans:      map[string]time.Time{},
		Archived:   map[string]bool{},
		Findings:   map[string]Findings{},
		Verdicts:   map[string]string{},
		Finished:   map[string]bool{},
		Workspaces: map[string]Workspace{},
	}

	var notes []string

	wts, err := gitx.Worktrees(ctx, in.Main)
	if err != nil {
		return nil, nil, err
	}

	def := gitx.Out(ctx, in.Main, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")

	// one log per worktree gives the count of commits past the base and
	// who wrote them, for the same call rev-list --count would cost: a
	// line per commit, the sha first so a commit with no author email
	// still counts once the output is trimmed, signatures off so
	// log.showSignature adds none
	for _, wt := range wts {
		w := Worktree{Path: wt.Path, Branch: wt.Branch}
		if wt.Branch != "" && def != "" && wt.Branch != strings.TrimPrefix(def, "origin/") {
			log, err := gitx.Exec(ctx, in.Main, "log", "--no-show-signature", "--format=%H %ae", def+".."+wt.Branch)
			if err == nil && log != "" {
				for line := range strings.SplitSeq(log, "\n") {
					w.Ahead++

					fields := strings.Fields(line)
					if len(fields) < 2 {
						continue
					}

					if email := fields[1]; !slices.ContainsFunc(w.Emails, func(e string) bool {
						return strings.EqualFold(e, email)
					}) {
						w.Emails = append(w.Emails, email)
					}
				}
			}
		}

		l.Worktrees = append(l.Worktrees, w)
	}

	branches, err := gitx.Refs(ctx, in.Main, "refs/heads/")
	if err != nil {
		return nil, nil, err
	}

	for _, name := range branches {
		l.Branches[name] = Branch{}
	}

	pattern := `^branch\..*\.(` + strings.Join(
		[]string{ref.TaskKey, ref.RidersKey, ref.AfterKey, ref.ModelKey, ref.FinishKey},
		"|",
	) + `)$`

	for _, e := range gitx.ConfigEntries(ctx, in.Main, pattern) {
		rest := strings.TrimPrefix(e.Key, "branch.")

		for _, key := range []string{ref.TaskKey, ref.RidersKey, ref.AfterKey, ref.ModelKey, ref.FinishKey} {
			name, ok := strings.CutSuffix(rest, "."+key)
			if !ok {
				continue
			}

			b := l.Branches[name]

			switch key {
			case ref.TaskKey:
				b.Task = e.Value
			case ref.RidersKey:
				b.Riders = strings.Fields(e.Value)
			case ref.AfterKey:
				b.After = e.Value
			case ref.ModelKey:
				b.Model = e.Value
			case ref.FinishKey:
				if d, ok := ref.ParseDriveFinish(e.Value); ok {
					b.Finish = &d
				}
			}

			l.Branches[name] = b
		}
	}

	dropDeadFinish(l.Branches, in.SharedRoot, prefix)

	runs, err := state.List(in.StateDir, now, in.TTL)
	if err != nil {
		notes = append(notes, "runs not read: "+err.Error())
	}

	for _, r := range runs {
		for _, wt := range wts {
			if r.Root != "" && canon(r.Root) == canon(wt.Path) {
				l.Runs = append(l.Runs, *r)

				break
			}
		}
	}

	for _, root := range storageRoots(in.SharedRoot, wts) {
		notes = append(notes, readRoot(root, l)...)
	}

	if in.LedgerPath != "" {
		notes = append(notes, readLedger(ctx, in.LedgerPath, in.ProjectID, l)...)
	}

	if in.Agents != nil {
		list, err := in.Agents.Workspaces(ctx)
		if err != nil {
			notes = append(notes, "workspaces not read: "+err.Error())
		}

		// keyed by the worktree's own spelling, since that is what the
		// rows carry; Herdr may report the path through a symlink
		for _, ws := range list {
			for _, wt := range l.Worktrees {
				if ws.Path != "" && canon(ws.Path) == canon(wt.Path) {
					l.Workspaces[wt.Path] = Workspace{ID: ws.ID, Label: ws.Label, AgentStatus: string(ws.Status)}
				}
			}
		}
	}

	return l, notes, nil
}

// readRoot reads one storage root's plans, archive, findings and
// verdicts into l.
func readRoot(root string, l *Local) []string {
	var notes []string

	for _, e := range dirEntries(filepath.Join(root, "plans")) {
		if id, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() {
			if info, err := e.Info(); err == nil {
				if at, seen := l.Plans[id]; !seen || info.ModTime().After(at) {
					l.Plans[id] = info.ModTime()
				}
			}
		}
	}

	for _, e := range dirEntries(filepath.Join(root, "plans", "archive")) {
		name := strings.TrimSuffix(e.Name(), ".md")
		if id, _, ok := strings.Cut(name, "."); ok {
			name = id
		}

		l.Archived[name] = true
	}

	for _, e := range dirEntries(filepath.Join(root, "reviews")) {
		if !e.IsDir() {
			continue
		}

		path, n := latest(filepath.Join(root, "reviews", e.Name()))
		if path == "" {
			continue
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			notes = append(notes, err.Error())

			continue
		}

		f, err := findings.Parse(string(raw))
		if err != nil {
			notes = append(notes, path+": "+err.Error())

			continue
		}

		fd := Findings{Round: n}
		if r, err := strconv.Atoi(f.Round); err == nil {
			fd.Round = r
		}

		// a prior block takes a verdict only in the three statuses that
		// reopen it; a settled one carries none by design
		for _, fi := range f.Findings {
			if fi.Prior && !findings.Reopens(fi.Status) {
				continue
			}

			if v := strings.ToLower(fi.Verdict); v == "" || strings.HasPrefix(v, "ask") {
				fd.Open++
			}
		}

		if info, err := os.Stat(path); err == nil {
			fd.At = info.ModTime()
		}

		if have, ok := l.Findings[e.Name()]; !ok || fd.Round >= have.Round {
			l.Findings[e.Name()] = fd
		}
	}

	for _, e := range dirEntries(filepath.Join(root, "verdicts")) {
		if !e.IsDir() {
			continue
		}

		path, _ := latest(filepath.Join(root, "verdicts", e.Name()))
		if path == "" {
			continue
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			notes = append(notes, err.Error())

			continue
		}

		v, err := verdict.Parse(string(raw))
		if err != nil {
			notes = append(notes, path+": "+err.Error())

			continue
		}

		if !v.Idea() {
			l.Verdicts[e.Name()] = v.Verdict
		}
	}

	return notes
}

// readLedger marks the tasks the ledger holds task_finished for.
func readLedger(ctx context.Context, path, project string, l *Local) []string {
	_ = ledger.SyncAt(ctx, path) // a failed replay still reads what the database holds

	db, err := ledger.OpenRead(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return []string{"ledger not read: " + err.Error()}
	}

	defer db.Close()

	events, err := db.List(ctx, ledger.Filter{Project: project, Kind: ledger.TaskFinished})
	if err != nil {
		return []string{"ledger not read: " + err.Error()}
	}

	for _, e := range events {
		if e.Task != "" {
			l.Finished[e.Task] = true
		}
	}

	return nil
}

// latest is the highest-numbered <n>.md in dir, and its n.
func latest(dir string) (string, int) {
	best, n := "", -1

	for _, e := range dirEntries(dir) {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok {
			continue
		}

		if i, err := strconv.Atoi(name); err == nil && i > n {
			best, n = filepath.Join(dir, e.Name()), i
		}
	}

	return best, n
}

// dirEntries is a directory's entries, none for one that is not there.
func dirEntries(dir string) []fs.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	return entries
}

// storageRoots are the .f10 dirs holding task files: the shared root, plus
// a worktree's own where it holds task files from before storage moved to
// main's; with no shared root, every checkout's.
func storageRoots(shared string, wts []gitx.Worktree) []string {
	var roots []string
	if shared != "" {
		roots = append(roots, shared)
	}

	for _, wt := range wts {
		root := filepath.Join(wt.Path, ".f10")
		if shared == "" || (canon(root) != canon(shared) && layout.HoldsTaskFiles(root)) {
			roots = append(roots, root)
		}
	}

	return roots
}

func canon(p string) string {
	if c, err := filepath.EvalSymlinks(p); err == nil {
		return c
	}

	return p
}

// collectTips is every local branch's last commit time, one git call: the
// feed of a checkout with no host to read.
func collectTips(ctx context.Context, main string) ([]Tip, error) {
	out, err := gitx.Exec(
		ctx,
		main,
		"for-each-ref",
		"--format=%(refname:short) %(committerdate:iso-strict)",
		"refs/heads/",
	)
	if err != nil {
		return nil, err
	}

	var tips []Tip

	for line := range strings.SplitSeq(out, "\n") {
		name, at, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}

		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			continue
		}

		tips = append(tips, Tip{Branch: name, At: t})
	}

	return tips, nil
}

// dropDeadFinish forgets a finish record whose task's lease nobody holds:
// the drive that wrote it was killed before it could clear it.
func dropDeadFinish(branches map[string]Branch, root, prefix string) {
	for name, b := range branches {
		if b.Finish == nil {
			continue
		}

		lead := b.Task
		if lead == "" {
			if members := ref.Members(prefix, name); members != nil {
				lead = members[0]
			} else {
				lead = ref.Match(prefix, name)
			}
		}

		if _, held := drives.Probe(root, lead); root == "" || lead == "" || !held {
			b.Finish = nil
			branches[name] = b
		}
	}
}
