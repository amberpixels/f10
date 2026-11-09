package lookaround

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// LocalInputs is where this machine keeps what the local collector reads.
type LocalInputs struct {
	Main       string // the main checkout
	StateDir   string // run state files
	TTL        time.Duration
	SharedRoot storage.Root     // the storage root every checkout shares
	LedgerPath string           // "" reads no ledger
	ProjectID  string           // the ledger's project id
	Agents     agentctl.Backend // the agent backend, when reachable: read its workspaces; nil reads none
	// Resolve turns a task as typed (`117`, a url) into its id; nil keeps it
	Resolve func(string) string
}

// collectLocal reads this machine's facts. Git failing fails the source; a
// file or the ledger that cannot be read is a note. prefix spells a branch's
// task from its name where nothing links one.
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

	// one log per worktree counts the commits past the base and who wrote
	// them: the sha first so a commit with no author email still counts after
	// trimming, signatures off so log.showSignature adds none
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

	records, err := branchmeta.LoadAll(ctx, in.Main)
	if err != nil {
		return nil, nil, err
	}

	for _, r := range records {
		if r.Task == "" && r.Riders == nil && r.After == "" && r.Agent == "" && r.Finish == nil {
			continue
		}

		b := l.Branches[r.Branch]
		b.Task, b.Riders, b.After, b.Finish = r.Task, r.Riders, r.After, r.Finish

		if r.Agent != "" {
			p := agentctl.ParseRecorded(r.Agent)
			b.Model, b.Agent = p.Model, p.Short()
		}

		l.Branches[r.Branch] = b
	}

	dropDeadFinish(l.Branches, in.SharedRoot.Path(), prefix)

	runs, err := state.List(in.StateDir, now, in.TTL)
	if err != nil {
		notes = append(notes, "runs not read: "+err.Error())
	}

	for _, r := range runs {
		for _, wt := range wts {
			if r.Root != "" && gitx.Resolved(r.Root) == gitx.Resolved(wt.Path) {
				l.Runs = append(l.Runs, *r)

				break
			}
		}
	}

	notes = append(notes, readRoot(in.SharedRoot, l)...)

	if in.LedgerPath != "" {
		notes = append(notes, readLedger(ctx, in.LedgerPath, in.ProjectID, in.Resolve, l)...)
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
				if ws.Path != "" && gitx.Resolved(ws.Path) == gitx.Resolved(wt.Path) {
					l.Workspaces[wt.Path] = Workspace{ID: ws.ID, Label: ws.Label, AgentStatus: string(ws.Status)}
				}
			}
		}
	}

	return l, notes, nil
}

func readRoot(root storage.Root, l *Local) []string {
	var notes []string

	for _, e := range dirEntries(root.Plans()) {
		if id, ok := strings.CutSuffix(e.Name(), ".md"); ok && !e.IsDir() {
			if info, err := e.Info(); err == nil {
				if at, seen := l.Plans[id]; !seen || info.ModTime().After(at) {
					l.Plans[id] = info.ModTime()
				}
			}
		}
	}

	for _, e := range dirEntries(root.Archive()) {
		name := strings.TrimSuffix(e.Name(), ".md")
		if id, _, ok := strings.Cut(name, "."); ok {
			name = id
		}

		l.Archived[name] = true
	}

	for _, e := range dirEntries(root.Reviews("")) {
		// archive/ holds the series of finished PRs, no task's live rounds
		if !e.IsDir() || e.Name() == storage.ArchiveDir {
			continue
		}

		path := findings.Latest(root.Reviews(e.Name()))
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

		fd := Findings{Round: storage.NumberOf(path)}
		if r, err := strconv.Atoi(f.Round); err == nil {
			fd.Round = r
		}

		fd.Open = f.Tally().Open

		if info, err := os.Stat(path); err == nil {
			fd.At = info.ModTime()
		}

		if have, ok := l.Findings[e.Name()]; !ok || fd.Round >= have.Round {
			l.Findings[e.Name()] = fd
		}
	}

	for _, e := range dirEntries(root.Verdicts("")) {
		if !e.IsDir() {
			continue
		}

		path := verdict.Latest(root.Verdicts(e.Name()))
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

func readLedger(ctx context.Context, path, project string, resolve func(string) string, l *Local) []string {
	events, err := ledger.ReadEvents(ctx, path, ledger.Filter{Project: project, Kind: ledger.TaskFinished})
	if err != nil {
		return []string{"ledger not read: " + err.Error()}
	}

	finishedFrom(events, resolve, l)

	return nil
}

// finishedNow is a ledger finish still standing: a live plan means the task
// was re-planned after it, so it is pending work again.
func finishedNow(l *Local, id string) bool {
	_, live := l.Plans[id]

	return l.Finished[id] && !live
}

// finishedFrom marks each task a finish row names; resolve turns the
// argument as typed into an id, nil keeping it as typed.
func finishedFrom(events []ledger.Event, resolve func(string) string, l *Local) {
	for _, e := range events {
		id := e.FinishedTask()
		if id != "" && resolve != nil {
			id = resolve(id)
		}

		if id != "" {
			l.Finished[id] = true
		}
	}
}

func dirEntries(dir string) []fs.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	return entries
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
		if lead != "" {
			lead = ref.TypedLead(lead)
		} else {
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
