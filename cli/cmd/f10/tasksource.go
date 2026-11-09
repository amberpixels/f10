package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A taskSource answers every task verb from one tracker. tasks() picks it,
// so no command decides between the driver and the host's issues.
type taskSource interface {
	// Read is the task as markdown, with the title and url the ledger keeps.
	Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error)
	Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error)
	Info(ctx context.Context, r ref.Ref) (taskInfo, error)
	URL(ctx context.Context, r ref.Ref) (string, error)
	Index(ctx context.Context) (string, error)
	List(ctx context.Context, limit int) ([]row, error)
	Search(ctx context.Context, query string) ([]row, error)
	Children(ctx context.Context, r ref.Ref) ([]rawChild, error)
	// Created is zero when the tracker keeps no date f10 can read.
	Created(ctx context.Context, r ref.Ref) (time.Time, error)
	// Title is the title alone, for a branch slug and a finished child's row.
	// A driver declines it, so only the host's issues answer.
	Title(ctx context.Context, r ref.Ref) (string, error)

	Comment(ctx context.Context, warnTo io.Writer, r ref.Ref, body string) (string, error)
	Update(ctx context.Context, warnTo io.Writer, r ref.Ref, p props) error
	// Relink's unwritten is the refusal's last clause: only the caller knows
	// whether properties were written before the link.
	Relink(ctx context.Context, warnTo io.Writer, r ref.Ref, parent, unwritten string) error
	Create(ctx context.Context, warnTo io.Writer, nt newTask) (ref.Ref, string, error)
}

// newTask is task create's input, read and resolved once whichever source
// takes it; parent and after are zero when not given.
type newTask struct {
	doc           string
	props         props
	parent, after ref.Ref
}

// tasks is the driver with the host's issues behind it, the host alone, or
// a refusal; only a driver's exit 3 reaches the second. Built per call, so
// a target whose facts change never answers from a stale choice.
func (t *target) tasks() taskSource {
	d := t.driver()

	var rest taskSource = refusedSource{t: t, d: d}

	if t.hostIssues() {
		if h, err := t.host(); err != nil {
			rest = refusedSource{t: t, d: d, noCLI: err}
		} else {
			rest = hostSource{t: t, h: h}
		}
	}

	if d == nil {
		return rest
	}

	return fallbackSource{first: driverSource{t: t, d: d}, rest: rest}
}

// refusedUpFront is the refusal a write gives before stdin is read; a
// driver may still take the verb, so only a source with no driver refuses.
func (t *target) refusedUpFront() (refusedSource, bool) {
	s, ok := t.tasks().(refusedSource)

	return s, ok
}

// fallbackSource asks the driver, and the rest only for a verb it declines.
type fallbackSource struct {
	first driverSource
	rest  taskSource
}

func declined(err error) bool { return errors.Is(err, driver.ErrUnsupported) }

func (s fallbackSource) Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error) {
	doc, k, err := s.first.Read(ctx, r)
	if declined(err) {
		return s.rest.Read(ctx, r)
	}

	return doc, k, err
}

func (s fallbackSource) Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
	k, err := s.first.Ref(ctx, r)
	if declined(err) {
		return s.rest.Ref(ctx, r)
	}

	return k, err
}

func (s fallbackSource) Info(ctx context.Context, r ref.Ref) (taskInfo, error) {
	info, err := s.first.Info(ctx, r)
	if declined(err) {
		return s.rest.Info(ctx, r)
	}

	return info, err
}

func (s fallbackSource) URL(ctx context.Context, r ref.Ref) (string, error) {
	url, err := s.first.URL(ctx, r)
	if declined(err) {
		return s.rest.URL(ctx, r)
	}

	return url, err
}

func (s fallbackSource) Index(ctx context.Context) (string, error) {
	url, err := s.first.Index(ctx)
	if declined(err) {
		return s.rest.Index(ctx)
	}

	return url, err
}

func (s fallbackSource) List(ctx context.Context, limit int) ([]row, error) {
	rows, err := s.first.List(ctx, limit)
	if declined(err) {
		return s.rest.List(ctx, limit)
	}

	return rows, err
}

func (s fallbackSource) Search(ctx context.Context, query string) ([]row, error) {
	rows, err := s.first.Search(ctx, query)
	if declined(err) {
		return s.rest.Search(ctx, query)
	}

	return rows, err
}

func (s fallbackSource) Children(ctx context.Context, r ref.Ref) ([]rawChild, error) {
	raw, err := s.first.Children(ctx, r)
	if declined(err) {
		return s.rest.Children(ctx, r)
	}

	return raw, err
}

func (s fallbackSource) Created(ctx context.Context, r ref.Ref) (time.Time, error) {
	at, err := s.first.Created(ctx, r)
	if declined(err) {
		return s.rest.Created(ctx, r)
	}

	return at, err
}

func (s fallbackSource) Title(ctx context.Context, r ref.Ref) (string, error) {
	title, err := s.first.Title(ctx, r)
	if declined(err) {
		return s.rest.Title(ctx, r)
	}

	return title, err
}

func (s fallbackSource) Comment(ctx context.Context, warnTo io.Writer, r ref.Ref, body string) (string, error) {
	url, err := s.first.Comment(ctx, warnTo, r, body)
	if declined(err) {
		return s.rest.Comment(ctx, warnTo, r, body)
	}

	return url, err
}

func (s fallbackSource) Update(ctx context.Context, warnTo io.Writer, r ref.Ref, p props) error {
	err := s.first.Update(ctx, warnTo, r, p)
	if declined(err) {
		return s.rest.Update(ctx, warnTo, r, p)
	}

	return err
}

func (s fallbackSource) Relink(ctx context.Context, warnTo io.Writer, r ref.Ref, parent, unwritten string) error {
	err := s.first.Relink(ctx, warnTo, r, parent, unwritten)
	if declined(err) {
		return s.rest.Relink(ctx, warnTo, r, parent, unwritten)
	}

	return err
}

func (s fallbackSource) Create(ctx context.Context, warnTo io.Writer, nt newTask) (ref.Ref, string, error) {
	r, url, err := s.first.Create(ctx, warnTo, nt)
	if declined(err) {
		return s.rest.Create(ctx, warnTo, nt)
	}

	return r, url, err
}

// driverSource runs the project's driver. A declined verb comes back as
// driver.ErrUnsupported, unwrapped, for fallbackSource to read.
type driverSource struct {
	t *target
	d *driver.Driver
}

//nolint:lostfield // a lookup by id, not a conversion
func (s driverSource) Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error) {
	doc, err := s.d.Run(ctx, driver.VerbRead, r.Number)
	if err != nil {
		return "", ledger.TaskRef{}, err
	}

	doc = oneIDHeading(doc, r, s.t.eff.TitleTemplate)

	return doc, ledger.TaskRef{ID: r.ID, Title: s.t.driverTitle(r, doc)}, nil
}

//nolint:lostfield // a lookup by id, not a conversion
func (s driverSource) Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
	doc, err := s.d.Run(ctx, driver.VerbRead, r.Number)
	if err != nil {
		return ledger.TaskRef{}, err
	}

	k := ledger.TaskRef{ID: r.ID, Title: s.t.driverTitle(r, doc)}
	if url, err := s.d.Run(ctx, driver.VerbURL, r.Number); err == nil {
		k.URL = strings.TrimSpace(url)
	}

	if k.Title == "" {
		return k, errors.New("the driver's read has no `# <title>` line")
	}

	return k, nil
}

func (s driverSource) Info(ctx context.Context, r ref.Ref) (taskInfo, error) {
	body, err := s.d.Run(ctx, driver.VerbRead, r.Number)

	return taskInfo{body: body}, err
}

func (s driverSource) URL(ctx context.Context, r ref.Ref) (string, error) {
	url, err := s.d.Run(ctx, driver.VerbURL, r.Number)

	return strings.TrimSpace(url), err
}

func (s driverSource) Index(ctx context.Context) (string, error) {
	url, err := s.d.Run(ctx, driver.VerbIndex)

	return strings.TrimSpace(url), err
}

// List ignores limit: the contract's list is the driver's first page.
func (s driverSource) List(ctx context.Context, _ int) ([]row, error) {
	return s.rows(ctx, "list", driver.VerbList)
}

func (s driverSource) Search(ctx context.Context, query string) ([]row, error) {
	return s.rows(ctx, "search", driver.VerbSearch, query)
}

func (s driverSource) Children(ctx context.Context, r ref.Ref) ([]rawChild, error) {
	out, err := s.d.Run(ctx, driver.VerbChildren, r.Number)
	if err != nil {
		return nil, err
	}

	var raw []rawChild
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing driver children rows: %w", err)
	}

	return raw, nil
}

// Created has no driver verb; the contract's read carries no date f10 parses.
func (driverSource) Created(context.Context, ref.Ref) (time.Time, error) {
	return time.Time{}, driver.ErrUnsupported
}

// Title has no driver verb: a driver names the branch itself, and a slug
// from its read would rename every branch on its tracker.
func (driverSource) Title(context.Context, ref.Ref) (string, error) {
	return "", driver.ErrUnsupported
}

func (s driverSource) Comment(ctx context.Context, warnTo io.Writer, r ref.Ref, body string) (string, error) {
	out, err := s.write(ctx, warnTo, body, driver.VerbComment, r.Number)
	if err != nil && !declined(err) {
		return "", fmt.Errorf("%w\n%s", err, mayPosted)
	}

	return strings.TrimSpace(out), err
}

func (s driverSource) Update(ctx context.Context, warnTo io.Writer, r ref.Ref, p props) error {
	if _, err := s.write(ctx, warnTo, "", driver.VerbUpdate, append([]string{r.Number}, p.args()...)...); err != nil {
		return err
	}

	retitleAfter(ctx, warnTo, s.t, r, "updated")

	return nil
}

func (s driverSource) Relink(ctx context.Context, warnTo io.Writer, r ref.Ref, parent, unwritten string) error {
	_, err := s.write(ctx, warnTo, "", driver.VerbUpdate, r.Number, "--parent", parent)
	if err != nil && !declined(err) && unwritten != unwrittenNothing {
		return fmt.Errorf("%w\n%s", err, unwritten)
	}

	return err
}

// Create passes the parent and the body's After: base as numbers, as every
// driver verb's argument is.
func (s driverSource) Create(ctx context.Context, warnTo io.Writer, nt newTask) (ref.Ref, string, error) {
	args := nt.props.args()

	for _, link := range []struct {
		flag string
		r    ref.Ref
	}{{"--parent", nt.parent}, {"--after", nt.after}} {
		if link.r.Number != "" {
			args = append(args, link.flag, link.r.Number)
		}
	}

	return driverCreate(ctx, warnTo, s.t, s.d, nt.doc, args)
}

func (s driverSource) rows(ctx context.Context, what, verb string, args ...string) ([]row, error) {
	out, err := s.d.Run(ctx, verb, args...)
	if err != nil {
		return nil, err
	}

	var rows []row
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("parsing driver %s rows: %w", what, err)
	}

	return stripTitles(rows, s.t.eff.TitleTemplate), nil
}

// write runs a verb taking stdin and passes its warning on; a declined
// verb wrote nothing, so it has no warning to pass.
func (s driverSource) write(ctx context.Context, warnTo io.Writer, in, verb string, args ...string) (string, error) {
	out, warn, err := s.d.RunInput(ctx, in, verb, args...)
	if err == nil && warn != "" {
		fmt.Fprintln(warnTo, warn)
	}

	return out, err
}

// hostSource is the host's issues as the tracker.
type hostSource struct {
	t *target
	h host
}

//nolint:lostfield // a lookup by id, not a conversion
func (s hostSource) Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error) {
	doc, iss, err := s.h.issueDoc(ctx, r.ID, r.Number, s.t.eff.StaleAfter)
	if err != nil {
		return "", ledger.TaskRef{}, err
	}

	return doc, ledger.TaskRef{
		ID:    r.ID,
		Title: tasktitle.Clean(s.h.title(iss.Title, r.Number), r.ID),
		URL:   iss.URL,
	}, nil
}

//nolint:lostfield // a lookup by id, not a conversion
func (s hostSource) Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
	iss, err := s.h.Issue(ctx, r.Number)
	if err != nil {
		return ledger.TaskRef{}, err
	}

	if iss.Title == "" {
		return ledger.TaskRef{}, fmt.Errorf("%s returned no title for issue %s", s.h.Name, r.Number)
	}

	return ledger.TaskRef{ID: r.ID, Title: tasktitle.Clean(s.h.title(iss.Title, r.Number), r.ID), URL: iss.URL}, nil
}

// Info reads the item whatever it is, since start refuses a PR by its own
// words rather than by the issue read's.
func (s hostSource) Info(ctx context.Context, r ref.Ref) (taskInfo, error) {
	iss, err := s.h.Item(ctx, r.Number)
	if err != nil {
		return taskInfo{}, err
	}

	return taskInfo{body: iss.Body, closed: strings.EqualFold(iss.State, "closed"), pull: iss.Pull}, nil
}

// URL goes through the issue, never `issue view --web`, which opens a pull
// request that shares the number.
func (s hostSource) URL(ctx context.Context, r ref.Ref) (string, error) {
	return s.h.issueURL(ctx, r.Number)
}

func (s hostSource) Index(ctx context.Context) (string, error) { return s.h.IssuesURL(ctx) }

func (s hostSource) List(ctx context.Context, limit int) ([]row, error) {
	return s.h.listIssues(ctx, "", s.t.eff.IDPrefix, s.t.eff.PriorityLabels, limit)
}

func (s hostSource) Search(ctx context.Context, query string) ([]row, error) {
	return s.h.listIssues(ctx, query, s.t.eff.IDPrefix, s.t.eff.PriorityLabels, issuePage)
}

func (s hostSource) Children(ctx context.Context, r ref.Ref) ([]rawChild, error) {
	kids, err := s.h.Children(ctx, r.Number)
	if errors.Is(err, forge.ErrUnsupported) {
		return nil, errNoParentLink
	}

	if err != nil {
		return nil, err
	}

	raw := make([]rawChild, 0, len(kids))
	for _, k := range kids {
		raw = append(
			raw,
			rawChild{ID: k.Number, Title: k.Title, URL: k.URL, State: k.State, Body: k.Body, Labels: k.Labels},
		)
	}

	return raw, nil
}

func (s hostSource) Created(ctx context.Context, r ref.Ref) (time.Time, error) {
	return s.h.issueCreated(ctx, r.Number)
}

func (s hostSource) Title(ctx context.Context, r ref.Ref) (string, error) {
	return s.h.issueTitle(ctx, r.Number)
}

func (s hostSource) Comment(ctx context.Context, _ io.Writer, r ref.Ref, body string) (string, error) {
	url, err := s.h.CommentIssue(ctx, r.Number, body)
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, mayPosted)
	}

	return url, nil
}

func (s hostSource) Update(ctx context.Context, _ io.Writer, r ref.Ref, p props) error {
	return s.h.update(ctx, r.Number, p, s.t.eff.PriorityLabels)
}

func (s hostSource) Relink(ctx context.Context, _ io.Writer, r ref.Ref, parent, unwritten string) error {
	err := s.h.relink(ctx, parent, r.Number)
	if errors.Is(err, forge.ErrNoIssueParent) {
		return ui.Exit(ui.ExitDeclined, "%v; %s", err, unwritten)
	}

	return err
}

func (s hostSource) Create(ctx context.Context, warnTo io.Writer, nt newTask) (ref.Ref, string, error) {
	return hostCreate(ctx, warnTo, s.t, s.h, nt)
}

// refusedSource is a tracker f10 cannot reach: not the host's issues and no
// driver taking the verb, or the host's issues with no host CLI. Reads fail
// plainly. Writes exit 3 so capture falls back to its adapters, except that
// comment and update with no host CLI fail plainly (refuseWrite).
type refusedSource struct {
	t     *target
	d     *driver.Driver // the driver that declined, nil when none was found
	noCLI error          // set when the tracker is the host's issues and no CLI reaches it
}

// verbNeeds names what needs each verb, for the refusal when a driver
// declines it.
var verbNeeds = map[string]string{
	driver.VerbRead:   "`f10 task read`, `f10 start` and `f10 drive` need",
	driver.VerbBranch: "`f10 start` and `f10 drive` need for a branch name past the bare id",
	driver.VerbURL:    "`f10 task open <id>` needs",
	driver.VerbIndex:  "`f10 task open` with no id needs",
	driver.VerbList:   "`f10 task open --print` and the board's next rows need",
	driver.VerbSearch: "`f10 task search` needs",
}

func (s refusedSource) Read(context.Context, ref.Ref) (string, ledger.TaskRef, error) {
	return "", ledger.TaskRef{}, s.refuse(driver.VerbRead)
}

func (s refusedSource) Ref(context.Context, ref.Ref) (ledger.TaskRef, error) {
	return ledger.TaskRef{}, s.refuse(driver.VerbRead)
}

func (s refusedSource) Info(context.Context, ref.Ref) (taskInfo, error) {
	return taskInfo{}, s.refuse(driver.VerbRead)
}

func (s refusedSource) URL(context.Context, ref.Ref) (string, error) {
	return "", s.refuse(driver.VerbURL)
}

func (s refusedSource) Index(context.Context) (string, error) {
	return "", s.refuse(driver.VerbIndex)
}

func (s refusedSource) List(context.Context, int) ([]row, error) {
	return nil, s.refuse(driver.VerbList)
}

func (s refusedSource) Search(context.Context, string) ([]row, error) {
	return nil, s.refuse(driver.VerbSearch)
}

// Children: a driver declining it on a tracker that is not the host's
// issues leaves no parent link to read, which status, drive and finish expect.
func (s refusedSource) Children(context.Context, ref.Ref) ([]rawChild, error) {
	if s.d != nil && s.noCLI == nil {
		return nil, errNoParentLink
	}

	return nil, s.refuse(driver.VerbChildren)
}

// Created is undated for a tracker that is not the host's issues; the
// host's issues with no CLI fail, since that tracker does carry a date.
func (s refusedSource) Created(context.Context, ref.Ref) (time.Time, error) {
	return time.Time{}, s.noCLI
}

func (s refusedSource) Title(context.Context, ref.Ref) (string, error) {
	return "", s.refuse(driver.VerbBranch)
}

func (s refusedSource) Comment(context.Context, io.Writer, ref.Ref, string) (string, error) {
	return "", s.refuseWrite(driver.VerbComment, unwrittenNothing)
}

func (s refusedSource) Update(context.Context, io.Writer, ref.Ref, props) error {
	return s.refuseWrite(driver.VerbUpdate, unwrittenNothing)
}

func (s refusedSource) Relink(_ context.Context, _ io.Writer, _ ref.Ref, _, unwritten string) error {
	return s.refuseWrite(driver.VerbUpdate+" --parent", unwritten)
}

func (s refusedSource) Create(context.Context, io.Writer, newTask) (ref.Ref, string, error) {
	return ref.Ref{}, "", s.refuseCreate()
}

// why is the refusal's one sentence for verb.
func (s refusedSource) why(verb string) string {
	if s.noCLI != nil {
		return s.noCLI.Error()
	}

	head := "project.md declares a " + s.t.eff.TrackerKind.String() + " tracker, which has no host-issues fallback, and "
	if s.d == nil {
		return head + "no driver was found (looked for " + strings.Join(s.t.driverPaths(), ", ") + ")"
	}

	need := ""
	if n, ok := verbNeeds[verb]; ok {
		need = ", which " + n
	}

	return head + "the driver at " + s.d.Path + " declines `" + verb + "`" + need
}

func (s refusedSource) refuse(verb string) error {
	if s.noCLI != nil {
		return s.noCLI
	}

	return errors.New(s.why(verb) + "; see docs/driver-contract.md")
}

// unwrittenNothing is a write's refusal when no write came before it.
const unwrittenNothing = "nothing was written"

// refuseWrite keeps a missing host CLI a plain error: the checkout is
// misconfigured, not a tracker capture should route around.
func (s refusedSource) refuseWrite(verb, unwritten string) error {
	if s.noCLI != nil {
		return fmt.Errorf("%w; %s", s.noCLI, unwritten)
	}

	return ui.Exit(ui.ExitDeclined, "%s; %s", s.why(verb), unwritten)
}

// refuseCreate is exit 3 in every case, so capture creates the task through
// its own adapter.
func (s refusedSource) refuseCreate() error {
	return ui.Exit(ui.ExitDeclined, "%s; create it through the project's Create adapter (project.md, Tracker)",
		s.why(driver.VerbCreate))
}
