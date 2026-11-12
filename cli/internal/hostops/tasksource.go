package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A taskSource answers every task verb from one tracker. tasks() picks it,
// so no command decides between the driver and the host's issues.
type taskSource interface {
	// Read is the task as markdown, with the title and url the ledger keeps.
	Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error)
	Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error)
	Info(ctx context.Context, r ref.Ref) (TaskInfo, error)
	URL(ctx context.Context, r ref.Ref) (string, error)
	Index(ctx context.Context) (string, error)
	List(ctx context.Context, limit int) ([]Row, error)
	Search(ctx context.Context, query string) ([]Row, error)
	Children(ctx context.Context, r ref.Ref) ([]RawChild, error)
	// Created is zero when the tracker keeps no date f10 can read.
	Created(ctx context.Context, r ref.Ref) (time.Time, error)
	// Title is the title alone, for a branch slug and a finished child's row.
	// A driver declines it, so only the host's issues answer.
	Title(ctx context.Context, r ref.Ref) (string, error)

	Comment(ctx context.Context, warnTo io.Writer, r ref.Ref, body string) (string, error)
	Update(ctx context.Context, warnTo io.Writer, r ref.Ref, p Props) error
	// Relink's unwritten is the refusal's last clause: only the caller knows
	// whether properties were written before the link.
	Relink(ctx context.Context, warnTo io.Writer, r ref.Ref, parent, unwritten string) error
	Create(ctx context.Context, warnTo io.Writer, nt NewTask) (ref.Ref, string, error)
}

// NewTask is task create's input, read and resolved once whichever source
// takes it; parent and after are zero when not given.
type NewTask struct {
	Doc           string
	Props         Props
	Parent, After ref.Ref
}

// TasksFor is the driver with the host's issues behind it, the host alone, or
// a refusal; only a driver's exit 3 reaches the second. Built per call, so
// a target whose facts change never answers from a stale choice.
func TasksFor(t *target.Target) taskSource {
	d := t.Driver()

	var rest taskSource = RefusedSource{Target: t, Driver: d}

	if t.HostIssues() {
		if h, err := HostOf(t); err != nil {
			rest = RefusedSource{Target: t, Driver: d, noCLI: err}
		} else {
			rest = HostSource{t: t, h: h}
		}
	}

	if d == nil {
		return rest
	}

	return FallbackSource{first: driverSource{t: t, d: d}, Rest: rest}
}

// FallbackSource asks the driver, and the rest only for a verb it declines.
type FallbackSource struct {
	first driverSource
	Rest  taskSource
}

func (s FallbackSource) Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error) {
	doc, k, err := s.first.Read(ctx, r)
	if declined(err) {
		return s.Rest.Read(ctx, r)
	}

	return doc, k, err
}

func (s FallbackSource) Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
	k, err := s.first.Ref(ctx, r)
	if declined(err) {
		return s.Rest.Ref(ctx, r)
	}

	return k, err
}

func (s FallbackSource) Info(ctx context.Context, r ref.Ref) (TaskInfo, error) {
	info, err := s.first.Info(ctx, r)
	if declined(err) {
		return s.Rest.Info(ctx, r)
	}

	return info, err
}

func (s FallbackSource) URL(ctx context.Context, r ref.Ref) (string, error) {
	url, err := s.first.URL(ctx, r)
	if declined(err) {
		return s.Rest.URL(ctx, r)
	}

	return url, err
}

func (s FallbackSource) Index(ctx context.Context) (string, error) {
	url, err := s.first.Index(ctx)
	if declined(err) {
		return s.Rest.Index(ctx)
	}

	return url, err
}

func (s FallbackSource) List(ctx context.Context, limit int) ([]Row, error) {
	rows, err := s.first.List(ctx, limit)
	if declined(err) {
		return s.Rest.List(ctx, limit)
	}

	return rows, err
}

func (s FallbackSource) Search(ctx context.Context, query string) ([]Row, error) {
	rows, err := s.first.Search(ctx, query)
	if declined(err) {
		return s.Rest.Search(ctx, query)
	}

	return rows, err
}

func (s FallbackSource) Children(ctx context.Context, r ref.Ref) ([]RawChild, error) {
	raw, err := s.first.Children(ctx, r)
	if declined(err) {
		return s.Rest.Children(ctx, r)
	}

	return raw, err
}

func (s FallbackSource) Created(ctx context.Context, r ref.Ref) (time.Time, error) {
	at, err := s.first.Created(ctx, r)
	if declined(err) {
		return s.Rest.Created(ctx, r)
	}

	return at, err
}

func (s FallbackSource) Title(ctx context.Context, r ref.Ref) (string, error) {
	title, err := s.first.Title(ctx, r)
	if declined(err) {
		return s.Rest.Title(ctx, r)
	}

	return title, err
}

func (s FallbackSource) Comment(ctx context.Context, warnTo io.Writer, r ref.Ref, body string) (string, error) {
	url, err := s.first.Comment(ctx, warnTo, r, body)
	if declined(err) {
		return s.Rest.Comment(ctx, warnTo, r, body)
	}

	return url, err
}

func (s FallbackSource) Update(ctx context.Context, warnTo io.Writer, r ref.Ref, p Props) error {
	err := s.first.Update(ctx, warnTo, r, p)
	if declined(err) {
		return s.Rest.Update(ctx, warnTo, r, p)
	}

	return err
}

func (s FallbackSource) Relink(ctx context.Context, warnTo io.Writer, r ref.Ref, parent, unwritten string) error {
	err := s.first.Relink(ctx, warnTo, r, parent, unwritten)
	if declined(err) {
		return s.Rest.Relink(ctx, warnTo, r, parent, unwritten)
	}

	return err
}

func (s FallbackSource) Create(ctx context.Context, warnTo io.Writer, nt NewTask) (ref.Ref, string, error) {
	r, url, err := s.first.Create(ctx, warnTo, nt)
	if declined(err) {
		return s.Rest.Create(ctx, warnTo, nt)
	}

	return r, url, err
}

func declined(err error) bool { return errors.Is(err, trackerdriver.ErrUnsupported) }

// driverSource runs the project's driver. A declined verb comes back as
// driver.ErrUnsupported, unwrapped, for FallbackSource to read.
type driverSource struct {
	t *target.Target
	d *trackerdriver.Driver
}

//nolint:lostfield // a lookup by id, not a conversion
func (s driverSource) Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error) {
	doc, err := s.d.Run(ctx, trackerdriver.VerbRead, r.Number)
	if err != nil {
		return "", ledger.TaskRef{}, err
	}

	doc = OneIDHeading(doc, r, s.t.Effective.TitleTemplate)

	return doc, ledger.TaskRef{ID: r.ID, Title: driverTitleFor(s.t, r, doc)}, nil
}

//nolint:lostfield // a lookup by id, not a conversion
func (s driverSource) Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
	doc, err := s.d.Run(ctx, trackerdriver.VerbRead, r.Number)
	if err != nil {
		return ledger.TaskRef{}, err
	}

	k := ledger.TaskRef{ID: r.ID, Title: driverTitleFor(s.t, r, doc)}
	if url, err := s.d.Run(ctx, trackerdriver.VerbURL, r.Number); err == nil {
		k.URL = strings.TrimSpace(url)
	}

	if k.Title == "" {
		return k, errors.New("the driver's read has no `# <title>` line")
	}

	return k, nil
}

func (s driverSource) Info(ctx context.Context, r ref.Ref) (TaskInfo, error) {
	body, err := s.d.Run(ctx, trackerdriver.VerbRead, r.Number)

	return TaskInfo{Body: body}, err
}

func (s driverSource) URL(ctx context.Context, r ref.Ref) (string, error) {
	url, err := s.d.Run(ctx, trackerdriver.VerbURL, r.Number)

	return strings.TrimSpace(url), err
}

func (s driverSource) Index(ctx context.Context) (string, error) {
	url, err := s.d.Run(ctx, trackerdriver.VerbIndex)

	return strings.TrimSpace(url), err
}

// List ignores limit: the contract's list is the driver's first page.
func (s driverSource) List(ctx context.Context, _ int) ([]Row, error) {
	return s.rows(ctx, "list", trackerdriver.VerbList)
}

func (s driverSource) Search(ctx context.Context, query string) ([]Row, error) {
	return s.rows(ctx, "search", trackerdriver.VerbSearch, query)
}

func (s driverSource) Children(ctx context.Context, r ref.Ref) ([]RawChild, error) {
	out, err := s.d.Run(ctx, trackerdriver.VerbChildren, r.Number)
	if err != nil {
		return nil, err
	}

	var raw []RawChild
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing driver children rows: %w", err)
	}

	return raw, nil
}

// Created has no driver verb; the contract's read carries no date f10 parses.
func (driverSource) Created(context.Context, ref.Ref) (time.Time, error) {
	return time.Time{}, trackerdriver.ErrUnsupported
}

// Title has no driver verb: a driver names the branch itself, and a slug
// from its read would rename every branch on its tracker.
func (driverSource) Title(context.Context, ref.Ref) (string, error) {
	return "", trackerdriver.ErrUnsupported
}

func (s driverSource) Comment(ctx context.Context, warnTo io.Writer, r ref.Ref, body string) (string, error) {
	out, err := s.write(ctx, warnTo, body, trackerdriver.VerbComment, r.Number)
	if err != nil && !declined(err) {
		return "", fmt.Errorf("%w\n%s", err, MayPosted)
	}

	return strings.TrimSpace(out), err
}

func (s driverSource) Update(ctx context.Context, warnTo io.Writer, r ref.Ref, p Props) error {
	if _, err := s.write(
		ctx,
		warnTo,
		"",
		trackerdriver.VerbUpdate,
		append([]string{r.Number}, p.args()...)...); err != nil {
		return err
	}

	RetitleAfter(ctx, warnTo, s.t, r, "updated")

	return nil
}

func (s driverSource) Relink(ctx context.Context, warnTo io.Writer, r ref.Ref, parent, unwritten string) error {
	_, err := s.write(ctx, warnTo, "", trackerdriver.VerbUpdate, r.Number, "--parent", parent)
	if err != nil && !declined(err) && unwritten != UnwrittenNothing {
		return fmt.Errorf("%w\n%s", err, unwritten)
	}

	return err
}

// Create passes the parent and the body's After: base as numbers, as every
// driver verb's argument is.
func (s driverSource) Create(ctx context.Context, warnTo io.Writer, nt NewTask) (ref.Ref, string, error) {
	args := nt.Props.args()

	for _, link := range []struct {
		flag string
		r    ref.Ref
	}{{"--parent", nt.Parent}, {"--after", nt.After}} {
		if link.r.Number != "" {
			args = append(args, link.flag, link.r.Number)
		}
	}

	return driverCreate(ctx, warnTo, s.t, s.d, nt.Doc, args)
}

func (s driverSource) rows(ctx context.Context, what, verb string, args ...string) ([]Row, error) {
	out, err := s.d.Run(ctx, verb, args...)
	if err != nil {
		return nil, err
	}

	var rows []Row
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("parsing driver %s rows: %w", what, err)
	}

	return StripTitles(rows, s.t.Effective.TitleTemplate), nil
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

// HostSource is the host's issues as the tracker.
type HostSource struct {
	t *target.Target
	h Host
}

//nolint:lostfield // a lookup by id, not a conversion
func (s HostSource) Read(ctx context.Context, r ref.Ref) (string, ledger.TaskRef, error) {
	doc, iss, err := s.h.IssueDoc(ctx, r.ID, r.Number, s.t.Effective.StaleAfter)
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
func (s HostSource) Ref(ctx context.Context, r ref.Ref) (ledger.TaskRef, error) {
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
func (s HostSource) Info(ctx context.Context, r ref.Ref) (TaskInfo, error) {
	iss, err := s.h.Item(ctx, r.Number)
	if err != nil {
		return TaskInfo{}, err
	}

	return TaskInfo{Body: iss.Body, Closed: strings.EqualFold(iss.State, "closed"), Pull: iss.Pull}, nil
}

// URL goes through the issue, never `issue view --web`, which opens a pull
// request that shares the number.
func (s HostSource) URL(ctx context.Context, r ref.Ref) (string, error) {
	return s.h.IssueURL(ctx, r.Number)
}

func (s HostSource) Index(ctx context.Context) (string, error) { return s.h.IssuesURL(ctx) }

func (s HostSource) List(ctx context.Context, limit int) ([]Row, error) {
	return s.h.ListIssues(ctx, "", s.t.Effective.IDPrefix, s.t.Effective.PriorityLabels, limit)
}

func (s HostSource) Search(ctx context.Context, query string) ([]Row, error) {
	return s.h.ListIssues(ctx, query, s.t.Effective.IDPrefix, s.t.Effective.PriorityLabels, IssuePage)
}

func (s HostSource) Children(ctx context.Context, r ref.Ref) ([]RawChild, error) {
	kids, err := s.h.Children(ctx, r.Number)
	if errors.Is(err, forge.ErrUnsupported) {
		return nil, ErrNoParentLink
	}

	if err != nil {
		return nil, err
	}

	raw := make([]RawChild, 0, len(kids))
	for _, k := range kids {
		raw = append(
			raw,
			RawChild{ID: k.Number, Title: k.Title, URL: k.URL, State: k.State, Body: k.Body, Labels: k.Labels},
		)
	}

	return raw, nil
}

func (s HostSource) Created(ctx context.Context, r ref.Ref) (time.Time, error) {
	return s.h.IssueCreated(ctx, r.Number)
}

func (s HostSource) Title(ctx context.Context, r ref.Ref) (string, error) {
	return s.h.IssueTitle(ctx, r.Number)
}

func (s HostSource) Comment(ctx context.Context, _ io.Writer, r ref.Ref, body string) (string, error) {
	url, err := s.h.CommentIssue(ctx, r.Number, body)
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, MayPosted)
	}

	return url, nil
}

func (s HostSource) Update(ctx context.Context, _ io.Writer, r ref.Ref, p Props) error {
	return s.h.Update(ctx, r.Number, p, s.t.Effective.PriorityLabels)
}

func (s HostSource) Relink(ctx context.Context, _ io.Writer, r ref.Ref, parent, unwritten string) error {
	err := s.h.Relink(ctx, parent, r.Number)
	if errors.Is(err, forge.ErrNoIssueParent) {
		return ui.Exit(ui.ExitDeclined, "%v; %s", err, unwritten)
	}

	return err
}

func (s HostSource) Create(ctx context.Context, warnTo io.Writer, nt NewTask) (ref.Ref, string, error) {
	return hostCreate(ctx, warnTo, s.t, s.h, nt)
}

// RefusedSource is a tracker f10 cannot reach: not the host's issues and no
// driver taking the verb, or the host's issues with no host CLI. Reads fail
// plainly. Writes exit 3 so capture falls back to its adapters, except that
// comment and update with no host CLI fail plainly (RefuseWrite).
type RefusedSource struct {
	Target *target.Target
	Driver *trackerdriver.Driver // the driver that declined, nil when none was found
	noCLI  error                 // set when the tracker is the host's issues and no CLI reaches it
}

func (s RefusedSource) Read(context.Context, ref.Ref) (string, ledger.TaskRef, error) {
	return "", ledger.TaskRef{}, s.Refuse(trackerdriver.VerbRead)
}

func (s RefusedSource) Ref(context.Context, ref.Ref) (ledger.TaskRef, error) {
	return ledger.TaskRef{}, s.Refuse(trackerdriver.VerbRead)
}

func (s RefusedSource) Info(context.Context, ref.Ref) (TaskInfo, error) {
	return TaskInfo{}, s.Refuse(trackerdriver.VerbRead)
}

func (s RefusedSource) URL(context.Context, ref.Ref) (string, error) {
	return "", s.Refuse(trackerdriver.VerbURL)
}

func (s RefusedSource) Index(context.Context) (string, error) {
	return "", s.Refuse(trackerdriver.VerbIndex)
}

func (s RefusedSource) List(context.Context, int) ([]Row, error) {
	return nil, s.Refuse(trackerdriver.VerbList)
}

func (s RefusedSource) Search(context.Context, string) ([]Row, error) {
	return nil, s.Refuse(trackerdriver.VerbSearch)
}

// Children: a driver declining it on a tracker that is not the host's
// issues leaves no parent link to read, which status, drive and finish expect.
func (s RefusedSource) Children(context.Context, ref.Ref) ([]RawChild, error) {
	if s.Driver != nil && s.noCLI == nil {
		return nil, ErrNoParentLink
	}

	return nil, s.Refuse(trackerdriver.VerbChildren)
}

// Created is undated for a tracker that is not the host's issues; the
// host's issues with no CLI fail, since that tracker does carry a date.
func (s RefusedSource) Created(context.Context, ref.Ref) (time.Time, error) {
	return time.Time{}, s.noCLI
}

func (s RefusedSource) Title(context.Context, ref.Ref) (string, error) {
	return "", s.Refuse(trackerdriver.VerbBranch)
}

func (s RefusedSource) Comment(context.Context, io.Writer, ref.Ref, string) (string, error) {
	return "", s.RefuseWrite(trackerdriver.VerbComment, UnwrittenNothing)
}

func (s RefusedSource) Update(context.Context, io.Writer, ref.Ref, Props) error {
	return s.RefuseWrite(trackerdriver.VerbUpdate, UnwrittenNothing)
}

func (s RefusedSource) Relink(_ context.Context, _ io.Writer, _ ref.Ref, _, unwritten string) error {
	return s.RefuseWrite(trackerdriver.VerbUpdate+" --parent", unwritten)
}

func (s RefusedSource) Create(context.Context, io.Writer, NewTask) (ref.Ref, string, error) {
	return ref.Ref{}, "", s.RefuseCreate()
}

func (s RefusedSource) Refuse(verb string) error {
	if s.noCLI != nil {
		return s.noCLI
	}

	return errors.New(s.why(verb) + "; see docs/driver-contract.md")
}

// RefuseWrite keeps a missing host CLI a plain error: the checkout is
// misconfigured, not a tracker capture should route around.
func (s RefusedSource) RefuseWrite(verb, unwritten string) error {
	if s.noCLI != nil {
		return fmt.Errorf("%w; %s", s.noCLI, unwritten)
	}

	return ui.Exit(ui.ExitDeclined, "%s; %s", s.why(verb), unwritten)
}

// RefuseCreate is exit 3 in every case, so capture creates the task through
// its own adapter.
func (s RefusedSource) RefuseCreate() error {
	return ui.Exit(ui.ExitDeclined, "%s; create it through the project's Create adapter (project.md, Tracker)",
		s.why(trackerdriver.VerbCreate))
}

// why is the refusal's one sentence for verb.
func (s RefusedSource) why(verb string) string {
	if s.noCLI != nil {
		return s.noCLI.Error()
	}

	head := "project.md declares a " + s.Target.Effective.TrackerKind.String() + " tracker, which has no host-issues fallback, and "
	if s.Driver == nil {
		return head + "no driver was found (looked for " + strings.Join(s.Target.DriverPaths(), ", ") + ")"
	}

	need := ""
	if n, ok := VerbNeeds[verb]; ok {
		need = ", which " + n
	}

	return head + "the driver at " + s.Driver.Path + " declines `" + verb + "`" + need
}

// VerbNeeds names what needs each verb, for the refusal when a driver
// declines it.
var VerbNeeds = map[string]string{
	trackerdriver.VerbRead:   "`f10 task read`, `f10 start` and `f10 drive` need",
	trackerdriver.VerbBranch: "`f10 start` and `f10 drive` need for a branch name past the bare id",
	trackerdriver.VerbURL:    "`f10 task open <id>` needs",
	trackerdriver.VerbIndex:  "`f10 task open` with no id needs",
	trackerdriver.VerbList:   "`f10 task open --print` and the board's next rows need",
	trackerdriver.VerbSearch: "`f10 task search` needs",
}

// UnwrittenNothing is a write's refusal when no write came before it.
const UnwrittenNothing = "nothing was written"
