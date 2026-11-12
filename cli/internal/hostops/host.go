package hostops

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// A Host is the forge binding with the nouns only this command acts on; the
// reads standup and the board share live on forge.Host itself.
type Host struct {
	forge.Host

	Titles tasktitle.Template // tracker.title; zero leaves titles as the host holds them
	Prefix string             // the task id prefix titles are rendered and stripped with
}

func NewHost(name, dir string) Host { return Host{Name: name, Dir: dir} }

// Relink puts child under parent, or with parent "" takes it out from under
// the parent it has.
func (h Host) Relink(ctx context.Context, parent, child string) error {
	if parent != "" {
		return h.LinkParent(ctx, parent, child)
	}

	return h.Unlink(ctx, child)
}

func (h Host) In(dir string) Host {
	h.Host = h.At(dir)

	return h
}

func (h Host) IssueDoc(
	ctx context.Context,
	id, number string,
	staleAfter facts.StaleAfter,
) (string, forge.Issue, error) {
	iss, err := h.IssueWithComments(ctx, number)
	if err != nil {
		return "", forge.Issue{}, err
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", id, h.Titles.Strip(iss.Title, id, number))

	// labels sit up top: fetch reads a task's kind from them, and its age
	// from the created date when stale_after is declared
	var created string
	if at := iss.CreatedAt; !at.IsZero() {
		created = "created " + at.UTC().Format(time.DateOnly)

		// the comparison is made here so fetch reads it, never works it out
		if StaleAt(at, staleAfter, time.Now()) {
			created += " (stale: older than " + staleAfter.String() + ")"
		}
	}

	meta := slices.DeleteFunc(
		[]string{iss.State, strings.Join(iss.Labels, ", "), created, iss.URL},
		func(s string) bool { return s == "" },
	)
	if len(meta) > 0 {
		fmt.Fprintf(&b, "%s\n\n", strings.Join(meta, " · "))
	}

	b.WriteString(strings.TrimSpace(iss.Body))
	b.WriteString("\n")

	for _, c := range iss.Comments {
		fmt.Fprintf(&b, "\n---\n\n**%s**\n\n%s\n", cmp.Or(c.Author, "comment"), strings.TrimSpace(c.Body))
	}

	return b.String(), iss, nil
}

// IssueCreated is the issue's created date, zero when the host gives none.
func (h Host) IssueCreated(ctx context.Context, number string) (time.Time, error) {
	iss, err := h.Issue(ctx, number)

	return iss.CreatedAt, err
}

func (h Host) IssueURL(ctx context.Context, number string) (string, error) {
	iss, err := h.Issue(ctx, number)
	if err != nil {
		return "", err
	}

	if url := iss.URL; url != "" {
		return url, nil
	}

	return "", fmt.Errorf("%s returned no url for issue %s", h.Name, number)
}

// IssueTitle is the one field the default branch name carries.
func (h Host) IssueTitle(ctx context.Context, number string) (string, error) {
	iss, err := h.Issue(ctx, number)
	if err != nil {
		return "", err
	}

	if iss.Title == "" {
		return "", fmt.Errorf("%s returned no title for issue %s", h.Name, number)
	}

	return h.title(iss.Title, number), nil
}

func (h Host) ListIssues(
	ctx context.Context, query, prefix string, priorities map[string]string, limit int,
) ([]Row, error) {
	found, err := h.Issues(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(found))
	for _, iss := range found {
		id := iss.Number
		if prefix != "" {
			id = prefix + "-" + id
		}

		r := Row{
			ID:       id,
			Title:    h.Titles.Strip(iss.Title, id, iss.Number),
			Status:   iss.State,
			URL:      iss.URL,
			Priority: priorityOf(iss.Labels, priorities),
			After:    AfterIn(iss.Body),
			Created:  iss.CreatedAt,
		}

		if len(iss.Assignees) > 0 {
			r.Assignee = iss.Assignees[0]
		}

		rows = append(rows, r)
	}

	return rows, nil
}

// MergeMethod is the declared method, else the one the repo allows; a
// GitHub repo allowing several gets squash with a note, since a
// history-rewriting default is not silent.
func (h Host) MergeMethod(ctx context.Context, declared string) (string, string, error) {
	if declared != "" {
		return h.MethodFor(declared), "", nil
	}

	allowed, err := h.MergeMethods(ctx)
	if err != nil {
		return "", "", err
	}

	if len(allowed) == 1 {
		return allowed[0], "", nil
	}

	return "--squash", "merged with --squash: project.md declares no merge method and the repo allows several; " +
		"declare `merge method: squash` (or merge, rebase) under Hosting & PR to settle it", nil
}

// PickupOf reads the request through f10 pr, which reads one in any state on
// either host, so the prompt is the same text on GitHub and GitLab.
func (h Host) PickupOf(number string, p forge.PR) *Pickup {
	pick := &Pickup{
		Number: number,
		URL:    p.URL,
		Branch: p.Head,
		View:   "f10 pr view --comments " + number,
		Diff:   "f10 pr diff " + number,
	}

	switch p.Status {
	case forge.Merged:
		pick.SHA, pick.State = p.Landed, landedState(p)
	case forge.Closed:
		pick.SHA, pick.State = p.HeadSHA, landedState(p)
		pick.HeadRef = h.HeadRef(number)
	}

	return pick
}

// PullRows is the first page of open requests, newest first.
func (h Host) PullRows(ctx context.Context) ([]Row, error) {
	found, err := h.OpenPRs(ctx, 30)
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(found))
	for _, p := range found {
		rows = append(rows, Row{ID: h.Sigil() + strconv.Itoa(p.Number), Title: p.Title, Status: p.Status, URL: p.URL})
	}

	return rows, nil
}

func (h Host) PRURL(ctx context.Context, number string) (string, error) {
	p, err := h.PR(ctx, number)
	if err != nil {
		return "", err
	}

	if p.URL == "" {
		return "", fmt.Errorf("%s returned no url for %s%s", h.Name, h.Sigil(), number)
	}

	return p.URL, nil
}

// OpenPull opens the request and reads the url back from the host, since neither CLI prints it in
// a form worth parsing.
func (h Host) OpenPull(ctx context.Context, n forge.NewPull) (string, error) {
	if err := h.CreatePull(ctx, n); err != nil {
		return "", err
	}

	p, err := h.PR(ctx, n.Head)
	if err != nil {
		return "", fmt.Errorf("opened, but reading it back failed: %w", err)
	}

	return p.URL, nil
}

// Update: an issue has two states and no custom properties, so `--status`
// takes open or closed and `--set` is refused; the labels of every other
// priority level come off, so the issue carries one; an assignee replaces
// the issue's, as glab's flag does; a title renamed by hand gets its prefix
// back.
func (h Host) Update(ctx context.Context, number string, p Props, priorities map[string]string) error {
	if len(p.Set) > 0 {
		return fmt.Errorf("%s issues have no custom properties for --set %s", h.Name, strings.Join(p.Set, ", "))
	}

	state := ""

	switch strings.ToLower(p.Status) {
	case "":
	case "open", "opened", "reopen", "reopened":
		state = "reopen"
	case "closed", "close", "done":
		state = "close"
	default:
		return fmt.Errorf("status %q: %s issues are open or closed", p.Status, h.Name)
	}

	iss, err := h.Issue(ctx, number)
	if err != nil {
		return err
	}

	title := h.retitled(iss.Title, number)

	add, remove := slices.Clone(p.Labels), []string(nil)

	if p.Priority != "" {
		label := priorityLabel(p.Priority, priorities)
		if label == "" {
			return fmt.Errorf("priority %s: tracker.priority in project.md maps no label to it", p.Priority)
		}

		add = append(add, label)

		for _, name := range iss.Labels {
			if level, ok := priorities[strings.ToLower(name)]; ok && level != p.Priority {
				remove = append(remove, name)
			}
		}
	}

	edit := forge.IssueEdit{
		Title: title, AddLabels: add, RemoveLabels: remove, Assignee: p.Assignee, Current: iss.Assignees,
	}
	if err := h.EditIssue(ctx, number, edit); err != nil {
		return err
	}

	if state != "" {
		if err := h.SetIssueOpen(ctx, number, state == "reopen"); err != nil {
			return err
		}
	}

	return nil
}

func (h Host) TitleChanges(ctx context.Context, number string) ([]titleChange, error) {
	var found []forge.Issue

	if number != "" {
		iss, err := h.Issue(ctx, number)
		if err != nil {
			return nil, err
		}

		found = []forge.Issue{iss}
	} else {
		all, err := h.AllIssues(ctx)
		if err != nil {
			return nil, err
		}

		found = all
	}

	var changes []titleChange

	for _, iss := range found {
		n := iss.Number
		if to := h.retitled(iss.Title, n); to != "" {
			changes = append(changes, titleChange{ID: h.taskID(n), Number: n, From: iss.Title, To: to})
		}
	}

	return changes, nil
}

func (h Host) SetTitle(ctx context.Context, number, title string) error {
	return h.EditIssue(ctx, number, forge.IssueEdit{Title: title})
}

func (h Host) taskID(number string) string {
	if h.Prefix == "" {
		return number
	}

	return h.Prefix + "-" + number
}

func (h Host) title(raw, number string) string {
	return h.Titles.Strip(raw, h.taskID(number), number)
}

func (h Host) retitled(raw, number string) string {
	id := h.taskID(number)
	if raw == "" || h.Titles.Applied(raw, id, number) {
		return ""
	}

	return h.Titles.Render(id, number, h.Titles.Strip(raw, id, number))
}

// StaleAt is task read's stale mark and task stale's age check in one place,
// so the two never disagree; an undeclared or bad stale_after is never stale.
func StaleAt(created time.Time, staleAfter facts.StaleAfter, now time.Time) bool {
	return staleAfter.On() && !created.IsZero() && now.Sub(created) > staleAfter.Duration
}

// IssuePage is the first page `task open --print` and `task search` show.
const IssuePage = 30

// priorityOf is the most urgent level the issue's labels map to, or "" when
// none does. Labels compare case-insensitively, as the hosts treat them.
func priorityOf(names []string, priorities map[string]string) string {
	best, level := len(trackerdriver.Priorities), ""

	for _, name := range names {
		if rank, ok := trackerdriver.PriorityRank(priorities[strings.ToLower(name)]); ok && rank < best {
			best, level = rank, trackerdriver.Priorities[rank]
		}
	}

	return level
}

// OptionalHostOf is the host, or nil for a checkout with no host CLI: a
// reader that works without one skips it rather than failing.
func OptionalHostOf(t *target.Target) *Host {
	h, err := HostOf(t)
	if err != nil {
		return nil
	}

	return &h
}

// HostOf is the CLI the probe settled on; a wrong guess opens the wrong site,
// so "unknown" refuses rather than picking.
func HostOf(t *target.Target) (Host, error) {
	switch t.Probes.CLI.Value {
	case "gh", "glab":
		h := NewHost(t.Probes.CLI.Value, t.Dir)
		if t.Effective != nil {
			h.Titles, h.Prefix = t.Effective.TitleTemplate, t.Effective.IDPrefix
		}

		return h, nil
	default:
		return Host{}, errors.New("no host CLI for this checkout: " + t.Probes.CLI.Evidence)
	}
}
