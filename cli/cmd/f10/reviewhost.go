package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// The host's side of `f10 review`: find the reviews a declared reviewer left
// on a branch's pull or merge request, in whichever of the three places the
// project said they arrive, tell whether each is done and whether it was
// handled, and leave the handled marker. Both CLIs are asked for JSON and
// read into one shape per object, as host.go and merge.go do, so the review
// noun decides on facts and never on a CLI's spelling of them.

// A remoteReview is one review as it sits on the host, normalized across
// the three arrival kinds and both CLIs. The exported fields are the --json
// output; the rest is what ack needs to find its way back.
type remoteReview struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	SHA       string    `json:"sha,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Done      bool      `json:"done"`
	Handled   bool      `json:"handled"`

	// stored is the text as the host keeps it on the object ack edits. Body
	// differs for an inline review, which folds its comments in for the
	// reader; writing that back would copy every comment into the summary.
	stored string

	threads   []string // inline review: the threads a `thread resolved` ack resolves
	commentID string   // inline review: the comment a reaction or marker lands on
	pending   bool     // inline review: not yet submitted
	concluded bool     // check run: finished
}

// A reviewPull is the request the reviews sit on, with the commits a
// comment's sha is derived from (an issue comment stores none).
type reviewPull struct {
	Number  string
	HeadSHA string
	URL     string
	Commits []pullCommit
}

type pullCommit struct {
	SHA string
	At  time.Time
}

// shaBefore is the last commit made before t: the head a reviewer saw when
// it posted a comment at t. The head sha when no commit precedes it.
func (p reviewPull) shaBefore(t time.Time) string {
	sha := p.HeadSHA

	for _, c := range p.Commits {
		if !c.At.After(t) {
			sha = c.SHA
		}
	}

	return sha
}

// errNoReview is pick finding nothing to return: no review by the reviewer,
// or none left unhandled. wait keeps polling on it and nothing else.
var errNoReview = errors.New("no review")

// reviewer binds a host to the four declared facts, the request and the
// lazily fetched identities ack and the handled check need.
type reviewer struct {
	h     host
	facts facts.Review
	pull  reviewPull

	viewer string // the login gh or glab is signed in as
	owner  string // gh GraphQL needs owner and name spelled out
	repo   string
}

// newReviewer reads the request number names, or the current branch's.
func newReviewer(ctx context.Context, h host, f facts.Review, number string) (*reviewer, error) {
	rv := &reviewer{h: h, facts: f}

	if err := rv.loadPull(ctx, number); err != nil {
		return nil, err
	}

	return rv, nil
}

func (rv *reviewer) loadPull(ctx context.Context, number string) error {
	if rv.h.glab() {
		return rv.loadMergeRequest(ctx, number)
	}

	var p struct {
		Number     int    `json:"number"`
		URL        string `json:"url"`
		HeadRefOid string `json:"headRefOid"`
		Commits    []struct {
			OID           string    `json:"oid"`
			CommittedDate time.Time `json:"committedDate"`
		} `json:"commits"`
	}

	if err := rv.h.decode(ctx, &p, rv.h.prArgs(number, "--json", "number,url,headRefOid,commits")...); err != nil {
		return err
	}

	rv.pull = reviewPull{Number: strconv.Itoa(p.Number), HeadSHA: p.HeadRefOid, URL: p.URL}
	for _, c := range p.Commits {
		rv.pull.Commits = append(rv.pull.Commits, pullCommit{SHA: c.OID, At: c.CommittedDate})
	}

	sortCommits(rv.pull.Commits)

	return nil
}

func (rv *reviewer) loadMergeRequest(ctx context.Context, number string) error {
	var mr struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
		SHA    string `json:"sha"`
	}

	if err := rv.h.decode(ctx, &mr, rv.h.prArgs(number, "-F", "json")...); err != nil {
		return err
	}

	rv.pull = reviewPull{Number: strconv.Itoa(mr.IID), HeadSHA: mr.SHA, URL: mr.WebURL}

	type mrCommit struct {
		ID            string    `json:"id"`
		CommittedDate time.Time `json:"committed_date"`
	}

	commits, err := decodePages[mrCommit](ctx, rv.h, rv.mrPath("commits?per_page=100"))
	if err != nil {
		return err
	}

	for _, c := range commits {
		rv.pull.Commits = append(rv.pull.Commits, pullCommit{SHA: c.ID, At: c.CommittedDate})
	}

	sortCommits(rv.pull.Commits)

	return nil
}

func sortCommits(commits []pullCommit) {
	sort.Slice(commits, func(i, j int) bool { return commits[i].At.Before(commits[j].At) })
}

// issuePath and mrPath spell the REST paths each CLI expands: gh fills
// {owner}/{repo}, glab fills :fullpath.
func (rv *reviewer) issuePath(tail string) string {
	return "repos/{owner}/{repo}/" + tail
}

func (rv *reviewer) mrPath(tail string) string {
	return "projects/:fullpath/merge_requests/" + rv.pull.Number + "/" + tail
}

// sameLogin matches a declared identity against a login the host returned,
// case-insensitive. A bare `claude` also matches `claude[bot]`, but a
// declared `claude[bot]` never matches a bare `claude`: only an app's login
// can carry the suffix, so dropping it on the host's side would let a user
// who registered the bare name post as the declared bot.
func sameLogin(declared, login string) bool {
	d, l := strings.ToLower(declared), strings.ToLower(login)
	if d == "" {
		return false
	}

	return d == l || d+"[bot]" == l
}

// isReviewer is login matching the declared reviewer. GitLab names a bot
// by its username alone, with no suffix anyone could forge or omit, so a
// declared `[bot]` is dropped there; on GitHub sameLogin's rule stands.
func (rv *reviewer) isReviewer(login string) bool {
	declared := rv.facts.Reviewer
	if rv.h.glab() {
		declared = strings.TrimSuffix(declared, "[bot]")
	}

	return sameLogin(declared, login)
}

// candidates lists every review the reviewer left, newest update first,
// done and handled already judged.
func (rv *reviewer) candidates(ctx context.Context) ([]remoteReview, error) {
	var (
		found []remoteReview
		err   error
	)

	switch rv.facts.Arrives {
	case facts.ArrivesComment:
		found, err = rv.comments(ctx)
	case facts.ArrivesInline:
		found, err = rv.inlineReviews(ctx)
	case facts.ArrivesCheck:
		found, err = rv.checkRuns(ctx)
	default:
		return nil, fmt.Errorf("the Review section declares no known arrival kind: %q", rv.facts.Arrives)
	}

	if err != nil {
		return nil, err
	}

	for i := range found {
		found[i].Done = rv.isDone(found[i])

		if found[i].Handled, err = rv.isHandled(ctx, found[i]); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(found, func(i, j int) bool { return found[i].UpdatedAt.After(found[j].UpdatedAt) })

	return found, nil
}

// pick is the newest review the reviewer left that nobody handled yet.
func (rv *reviewer) pick(ctx context.Context) (remoteReview, error) {
	found, err := rv.candidates(ctx)
	if err != nil {
		return remoteReview{}, err
	}

	if len(found) == 0 {
		return remoteReview{}, fmt.Errorf("%w from %s on %s%s",
			errNoReview, rv.facts.Reviewer, rv.pull.URL, rv.manualHint())
	}

	if r, ok := firstUnhandled(found); ok {
		return r, nil
	}

	return remoteReview{}, fmt.Errorf("%w left unhandled from %s: the latest (%s) is handled%s",
		errNoReview, rv.facts.Reviewer, found[0].URL, rv.manualHint())
}

// ackTarget is the review ack marks: the one pick returns, or with every
// review handled the newest, which ack confirms rather than marks twice.
func (rv *reviewer) ackTarget(ctx context.Context) (remoteReview, error) {
	found, err := rv.candidates(ctx)
	if err != nil {
		return remoteReview{}, err
	}

	if len(found) == 0 {
		return remoteReview{}, fmt.Errorf("nothing to acknowledge: %w from %s on %s",
			errNoReview, rv.facts.Reviewer, rv.pull.URL)
	}

	if r, ok := firstUnhandled(found); ok {
		return r, nil
	}

	return found[0], nil
}

// firstUnhandled is the newest of found that nobody handled. pick returns
// it and ack marks it, so the two verbs always name the same review.
func firstUnhandled(found []remoteReview) (remoteReview, bool) {
	for _, r := range found {
		if !r.Handled {
			return r, true
		}
	}

	return remoteReview{}, false
}

// manualHint names the boundary f10 stops at: the trigger is a human's.
func (rv *reviewer) manualHint() string {
	if !strings.Contains(rv.facts.Trigger, facts.TriggerManual) {
		return ""
	}

	how := rv.facts.TriggerHow
	if how == "" {
		how = "see project.md"
	}

	if strings.Contains(rv.facts.Trigger, facts.TriggerAutomatic) {
		return "; a re-review is manual (" + how + ") and f10 never performs it"
	}

	return "; the trigger is manual (" + how + ") and f10 never performs it"
}

// isDone applies the declared done signal to a review.
func (rv *reviewer) isDone(r remoteReview) bool {
	switch rv.facts.Done {
	case facts.DoneMarker:
		return rv.facts.DoneText != "" && strings.Contains(r.Body, rv.facts.DoneText)
	case facts.DoneSubmitted:
		return !r.pending
	case facts.DoneConcluded:
		return r.concluded
	default: // posted: it exists
		return r.Kind != facts.ArrivesCheck || r.concluded
	}
}

var checkboxRE = regexp.MustCompile(`(?m)^\s*[-*]\s+\[([ xX])\]`)

// isHandled applies the declared handled marker. A reaction counts only
// when it is newer than the review's last edit: a sticky comment keeps its
// id across re-reviews, so round one's reaction must not handle round two.
func (rv *reviewer) isHandled(ctx context.Context, r remoteReview) (bool, error) {
	switch rv.facts.Handled {
	case facts.HandledReaction:
		return rv.hasFreshReaction(ctx, r)
	case facts.HandledMarker:
		return rv.facts.HandledText != "" && strings.Contains(r.stored, rv.facts.HandledText), nil
	case facts.HandledCheckbox:
		m := checkboxRE.FindStringSubmatch(r.stored)

		return len(m) > 1 && m[1] != " ", nil
	case facts.HandledThread:
		return rv.threadsResolved(ctx, r)
	default:
		return false, nil
	}
}

func (rv *reviewer) hasFreshReaction(ctx context.Context, r remoteReview) (bool, error) {
	if r.Kind == facts.ArrivesCheck {
		return false, nil
	}

	viewer, err := rv.viewerLogin(ctx)
	if err != nil {
		return false, err
	}

	path, ok := rv.reactionPath(r)
	if !ok {
		return false, nil
	}

	// gh calls them reactions with a content, glab award emoji with a name
	var reactions []struct {
		Content   string    `json:"content"`
		Name      string    `json:"name"`
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login    string `json:"login"`
			Username string `json:"username"`
		} `json:"user"`
	}

	if err := rv.h.decode(ctx, &reactions, "api", path); err != nil {
		return false, err
	}

	for _, re := range reactions {
		if cmp.Or(re.Content, re.Name) == rv.facts.HandledText &&
			sameLogin(viewer, cmp.Or(re.User.Login, re.User.Username)) &&
			!re.CreatedAt.Before(r.UpdatedAt) {
			return true, nil
		}
	}

	return false, nil
}

// reactionPath is where a review's reactions live, per kind and CLI.
func (rv *reviewer) reactionPath(r remoteReview) (string, bool) {
	id := r.ID
	if r.Kind == facts.ArrivesInline {
		if r.commentID == "" {
			return "", false
		}

		id = r.commentID
	}

	if rv.h.glab() {
		return rv.mrPath("notes/" + id + "/award_emoji?per_page=100"), true
	}

	if r.Kind == facts.ArrivesInline {
		return rv.issuePath("pulls/comments/" + id + "/reactions?per_page=100"), true
	}

	return rv.issuePath("issues/comments/" + id + "/reactions?per_page=100"), true
}

// viewerLogin is who the CLI acts as: the only login whose reaction counts.
func (rv *reviewer) viewerLogin(ctx context.Context) (string, error) {
	if rv.viewer != "" {
		return rv.viewer, nil
	}

	var user struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	}

	if err := rv.h.decode(ctx, &user, "api", "user"); err != nil {
		return "", err
	}

	rv.viewer = cmp.Or(user.Login, user.Username)
	if rv.viewer == "" {
		return "", fmt.Errorf("%s returned no login for the signed-in user", rv.h.name)
	}

	return rv.viewer, nil
}

// ack leaves the declared handled marker on r. Already handled is confirmed
// and nothing is posted twice.
func (rv *reviewer) ack(ctx context.Context, r remoteReview) (string, error) {
	if r.Handled {
		return "already handled", nil
	}

	if r.Kind == facts.ArrivesCheck {
		return "a check run carries no handled marker: a new push starts a new run", nil
	}

	switch rv.facts.Handled {
	case facts.HandledReaction:
		return rv.react(ctx, r)
	case facts.HandledMarker:
		return rv.patchBody(ctx, r, strings.TrimRight(r.stored, "\n")+"\n\n"+rv.facts.HandledText, "marker appended")
	case facts.HandledCheckbox:
		// the first box only: isHandled reads the first, and the rest may be
		// the reviewer's own list, which ticking would read as all fixed
		m := checkboxRE.FindStringSubmatchIndex(r.stored)
		if m == nil {
			return "", errors.New("the review carries no checkbox to tick")
		}

		body := r.stored[:m[2]] + "x" + r.stored[m[3]:]

		return rv.patchBody(ctx, r, body, "checkbox ticked")
	case facts.HandledThread:
		return rv.resolveThreads(ctx, r)
	default:
		return "", fmt.Errorf("the Review section declares no known handled marker: %q", rv.facts.Handled)
	}
}

func (rv *reviewer) react(ctx context.Context, r remoteReview) (string, error) {
	path, ok := rv.reactionPath(r)
	if !ok {
		return "", errors.New("the review has no comment to react to")
	}

	path, _, _ = strings.Cut(path, "?")

	field := "content=" + rv.facts.HandledText
	if rv.h.glab() {
		field = "name=" + rv.facts.HandledText
	}

	if _, err := rv.h.run(ctx, "api", "-X", "POST", path, "-f", field); err != nil {
		return "", err
	}

	return "reaction " + rv.facts.HandledText, nil
}

// patchBody rewrites the review's text with the marker in it. An issue
// comment and a glab note are edited in place; a gh inline review is edited
// through the pull's reviews endpoint.
func (rv *reviewer) patchBody(ctx context.Context, r remoteReview, body, what string) (string, error) {
	var args []string

	switch {
	case rv.h.glab():
		id := cmp.Or(r.commentID, r.ID)
		args = []string{"api", "-X", "PUT", rv.mrPath("notes/" + id), "-f", "body=" + body}
	case r.Kind == facts.ArrivesInline:
		args = []string{
			"api",
			"-X",
			"PUT",
			rv.issuePath("pulls/" + rv.pull.Number + "/reviews/" + r.ID),
			"-f",
			"body=" + body,
		}
	default:
		args = []string{"api", "-X", "PATCH", rv.issuePath("issues/comments/" + r.ID), "-f", "body=" + body}
	}

	if _, err := rv.h.run(ctx, args...); err != nil {
		return "", err
	}

	return what, nil
}

func (rv *reviewer) resolveThreads(ctx context.Context, r remoteReview) (string, error) {
	if r.Kind != facts.ArrivesInline {
		return "", errors.New(
			"only an inline review has threads to resolve; declare reaction, marker or checkbox for a comment",
		)
	}

	if len(r.threads) == 0 {
		return "", errors.New("the review opened no threads to resolve")
	}

	for _, id := range r.threads {
		var err error

		if rv.h.glab() {
			_, err = rv.h.run(ctx, "api", "-X", "PUT", rv.mrPath("discussions/"+id+"?resolved=true"))
		} else {
			_, err = rv.h.run(
				ctx,
				"api",
				"graphql",
				"-f",
				"query="+resolveThreadMutation,
				"-F",
				"id="+id,
			)
		}

		if err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%d thread(s) resolved", len(r.threads)), nil
}

// comments lists the request's issue-level comments by the reviewer.
func (rv *reviewer) comments(ctx context.Context) ([]remoteReview, error) {
	type note struct {
		ID        int64     `json:"id"`
		Body      string    `json:"body"`
		HTMLURL   string    `json:"html_url"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		System    bool      `json:"system"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		Author struct {
			Username string `json:"username"`
		} `json:"author"`
	}

	path := rv.issuePath("issues/" + rv.pull.Number + "/comments?per_page=100")
	if rv.h.glab() {
		path = rv.mrPath("notes?per_page=100")
	}

	// every page: the oldest come first, so a review past the first page is
	// the newest one
	notes, err := decodePages[note](ctx, rv.h, path)
	if err != nil {
		return nil, err
	}

	var found []remoteReview

	for _, n := range notes {
		login := cmp.Or(n.User.Login, n.Author.Username)
		if n.System || !rv.isReviewer(login) {
			continue
		}

		id := strconv.FormatInt(n.ID, 10)

		found = append(found, remoteReview{
			ID:        id,
			Kind:      facts.ArrivesComment,
			URL:       cmp.Or(n.HTMLURL, rv.pull.URL+"#note_"+id),
			Author:    login,
			Body:      n.Body,
			stored:    n.Body,
			SHA:       rv.pull.shaBefore(n.CreatedAt),
			CreatedAt: n.CreatedAt,
			UpdatedAt: cmp.Or(n.UpdatedAt, n.CreatedAt),
		})
	}

	return found, nil
}

// inlineReviews lists the reviewer's submitted reviews with their inline
// comments folded into the body as `path:line` blocks.
func (rv *reviewer) inlineReviews(ctx context.Context) ([]remoteReview, error) {
	if rv.h.glab() {
		return rv.discussions(ctx)
	}

	type pullReview struct {
		ID          int64     `json:"id"`
		Body        string    `json:"body"`
		State       string    `json:"state"`
		CommitID    string    `json:"commit_id"`
		HTMLURL     string    `json:"html_url"`
		SubmittedAt time.Time `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}

	reviews, err := decodePages[pullReview](ctx, rv.h, rv.issuePath("pulls/"+rv.pull.Number+"/reviews?per_page=100"))
	if err != nil {
		return nil, err
	}

	var found []remoteReview

	for _, r := range reviews {
		if !rv.isReviewer(r.User.Login) {
			continue
		}

		review := remoteReview{
			ID:        strconv.FormatInt(r.ID, 10),
			Kind:      facts.ArrivesInline,
			URL:       r.HTMLURL,
			Author:    r.User.Login,
			Body:      r.Body,
			stored:    r.Body,
			SHA:       r.CommitID,
			CreatedAt: r.SubmittedAt,
			UpdatedAt: r.SubmittedAt,
			pending:   strings.EqualFold(r.State, "PENDING"),
		}

		if err := rv.foldReviewComments(ctx, &review); err != nil {
			return nil, err
		}

		found = append(found, review)
	}

	return found, nil
}

func (rv *reviewer) foldReviewComments(ctx context.Context, review *remoteReview) error {
	type reviewComment struct {
		ID        int64     `json:"id"`
		Path      string    `json:"path"`
		Line      int       `json:"line"`
		Body      string    `json:"body"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	comments, err := decodePages[reviewComment](
		ctx,
		rv.h,
		rv.issuePath("pulls/"+rv.pull.Number+"/reviews/"+review.ID+"/comments?per_page=100"),
	)
	if err != nil {
		return err
	}

	var b strings.Builder

	b.WriteString(review.Body)

	for _, c := range comments {
		if review.commentID == "" {
			review.commentID = strconv.FormatInt(c.ID, 10)
		}

		if c.UpdatedAt.After(review.UpdatedAt) {
			review.UpdatedAt = c.UpdatedAt
		}

		fmt.Fprintf(&b, "\n\n### %s:%d\n\n%s", c.Path, c.Line, strings.TrimSpace(c.Body))
	}

	review.Body = strings.TrimSpace(b.String())

	if len(comments) > 0 {
		review.threads = rv.threadsOf(ctx, review.ID)
	}

	return nil
}

// threadsOf is the node ids of the review's threads, with their resolved
// state read later by threadsResolved. GraphQL is the only place GitHub
// exposes a thread; failure here leaves the list empty and the thread ack
// says so, since a lookup problem must not fail a pick.
func (rv *reviewer) threadsOf(ctx context.Context, reviewID string) []string {
	threads, err := rv.reviewThreads(ctx)
	if err != nil {
		return nil
	}

	var ids []string

	for _, t := range threads {
		if t.review == reviewID {
			ids = append(ids, t.id)
		}
	}

	return ids
}

// The two GraphQL documents: GitHub exposes review threads nowhere else.
const (
	reviewThreadsQuery = "query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) " +
		"{ pullRequest(number: $number) { reviewThreads(first: 100) { nodes { id isResolved " +
		"comments(first: 1) { nodes { pullRequestReview { databaseId } } } } } } } }"

	resolveThreadMutation = "mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { isResolved } } }"
)

type reviewThread struct {
	id       string
	review   string
	resolved bool
}

func (rv *reviewer) reviewThreads(ctx context.Context) ([]reviewThread, error) {
	if err := rv.loadRepo(ctx); err != nil {
		return nil, err
	}

	var payload struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									PullRequestReview struct {
										DatabaseID int64 `json:"databaseId"`
									} `json:"pullRequestReview"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}

	err := rv.h.decode(ctx, &payload, "api", "graphql", "-f", "query="+reviewThreadsQuery,
		"-F", "owner="+rv.owner, "-F", "name="+rv.repo, "-F", "number="+rv.pull.Number)
	if err != nil {
		return nil, err
	}

	var threads []reviewThread

	for _, n := range payload.Data.Repository.PullRequest.ReviewThreads.Nodes {
		t := reviewThread{id: n.ID, resolved: n.IsResolved}
		if len(n.Comments.Nodes) > 0 {
			t.review = strconv.FormatInt(n.Comments.Nodes[0].PullRequestReview.DatabaseID, 10)
		}

		threads = append(threads, t)
	}

	return threads, nil
}

func (rv *reviewer) loadRepo(ctx context.Context) error {
	if rv.owner != "" {
		return nil
	}

	var repo struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}

	if err := rv.h.decode(ctx, &repo, "repo", "view", "--json", "owner,name"); err != nil {
		return err
	}

	rv.owner, rv.repo = repo.Owner.Login, repo.Name

	return nil
}

// threadsResolved is true when the review opened threads and every one is
// resolved. glab discussions carry the flag themselves; gh threads are
// re-read, since the ids were gathered without their state.
func (rv *reviewer) threadsResolved(ctx context.Context, r remoteReview) (bool, error) {
	if r.Kind != facts.ArrivesInline || len(r.threads) == 0 {
		return false, nil
	}

	if rv.h.glab() {
		return r.concluded, nil // discussions() folds "all resolved" into concluded
	}

	threads, err := rv.reviewThreads(ctx)
	if err != nil {
		return false, err
	}

	for _, t := range threads {
		if slices.Contains(r.threads, t.id) && !t.resolved {
			return false, nil
		}
	}

	return true, nil
}

// discussions is GitLab's inline review: the reviewer's resolvable
// discussions, folded into one review value whose threads are the
// discussion ids. GitLab has no review object grouping them, so "the latest
// review" is the set of discussions still open.
func (rv *reviewer) discussions(ctx context.Context) ([]remoteReview, error) {
	type discussion struct {
		ID    string `json:"id"`
		Notes []struct {
			ID         int64     `json:"id"`
			Body       string    `json:"body"`
			System     bool      `json:"system"`
			Resolvable bool      `json:"resolvable"`
			Resolved   bool      `json:"resolved"`
			CreatedAt  time.Time `json:"created_at"`
			UpdatedAt  time.Time `json:"updated_at"`
			Author     struct {
				Username string `json:"username"`
			} `json:"author"`
			Position *struct {
				NewPath string `json:"new_path"`
				NewLine int    `json:"new_line"`
				HeadSHA string `json:"head_sha"`
			} `json:"position"`
		} `json:"notes"`
	}

	discussions, err := decodePages[discussion](ctx, rv.h, rv.mrPath("discussions?per_page=100"))
	if err != nil {
		return nil, err
	}

	review := remoteReview{Kind: facts.ArrivesInline, URL: rv.pull.URL, Author: rv.facts.Reviewer, concluded: true}

	var b strings.Builder

	for _, d := range discussions {
		if len(d.Notes) == 0 {
			continue
		}

		first := d.Notes[0]
		if first.System || !first.Resolvable || !rv.isReviewer(first.Author.Username) {
			continue
		}

		review.threads = append(review.threads, d.ID)
		review.concluded = review.concluded && first.Resolved

		// a marker or checkbox lands on this note, so its own text is the
		// one ack edits
		if review.commentID == "" {
			review.commentID = strconv.FormatInt(first.ID, 10)
			review.ID = d.ID
			review.stored = first.Body
		}

		if first.CreatedAt.After(review.CreatedAt) {
			review.CreatedAt = first.CreatedAt
		}

		if first.UpdatedAt.After(review.UpdatedAt) {
			review.UpdatedAt = first.UpdatedAt
		}

		if first.Position != nil {
			review.SHA = cmp.Or(review.SHA, first.Position.HeadSHA)
			fmt.Fprintf(&b, "\n\n### %s:%d\n\n", first.Position.NewPath, first.Position.NewLine)
		} else {
			b.WriteString("\n\n")
		}

		b.WriteString(strings.TrimSpace(first.Body))
	}

	if len(review.threads) == 0 {
		return nil, nil
	}

	review.Body = strings.TrimSpace(b.String())
	review.SHA = cmp.Or(review.SHA, rv.pull.shaBefore(review.CreatedAt))

	return []remoteReview{review}, nil
}

// checkRuns lists the check run (gh) or pipeline job (glab) named as the
// reviewer, on the request's head.
func (rv *reviewer) checkRuns(ctx context.Context) ([]remoteReview, error) {
	if rv.h.glab() {
		return rv.pipelineJobs(ctx)
	}

	var payload struct {
		CheckRuns []struct {
			ID          int64     `json:"id"`
			Name        string    `json:"name"`
			Status      string    `json:"status"`
			HTMLURL     string    `json:"html_url"`
			HeadSHA     string    `json:"head_sha"`
			StartedAt   time.Time `json:"started_at"`
			CompletedAt time.Time `json:"completed_at"`
			Output      struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
				Text    string `json:"text"`
			} `json:"output"`
		} `json:"check_runs"`
	}

	err := rv.h.decode(ctx, &payload, "api", rv.issuePath("commits/"+rv.pull.HeadSHA+"/check-runs?per_page=100"))
	if err != nil {
		return nil, err
	}

	var found []remoteReview

	for _, c := range payload.CheckRuns {
		if !strings.EqualFold(c.Name, rv.facts.Reviewer) {
			continue
		}

		body := strings.TrimSpace(strings.Join([]string{c.Output.Title, c.Output.Summary, c.Output.Text}, "\n\n"))

		found = append(found, remoteReview{
			ID:        strconv.FormatInt(c.ID, 10),
			Kind:      facts.ArrivesCheck,
			URL:       c.HTMLURL,
			Author:    c.Name,
			Body:      body,
			SHA:       c.HeadSHA,
			CreatedAt: c.StartedAt,
			UpdatedAt: cmp.Or(c.CompletedAt, c.StartedAt),
			concluded: c.Status == "completed",
		})
	}

	return found, nil
}

func (rv *reviewer) pipelineJobs(ctx context.Context) ([]remoteReview, error) {
	var pipelines []struct {
		ID  int64  `json:"id"`
		SHA string `json:"sha"`
	}

	if err := rv.h.decode(ctx, &pipelines, "api", rv.mrPath("pipelines?per_page=1")); err != nil {
		return nil, err
	}

	if len(pipelines) == 0 {
		return nil, nil
	}

	pipeline := pipelines[0]

	var jobs []struct {
		ID         int64     `json:"id"`
		Name       string    `json:"name"`
		Status     string    `json:"status"`
		WebURL     string    `json:"web_url"`
		CreatedAt  time.Time `json:"created_at"`
		FinishedAt time.Time `json:"finished_at"`
	}

	path := "projects/:fullpath/pipelines/" + strconv.FormatInt(pipeline.ID, 10) + "/jobs?per_page=100"
	if err := rv.h.decode(ctx, &jobs, "api", path); err != nil {
		return nil, err
	}

	var found []remoteReview

	for _, j := range jobs {
		if !strings.EqualFold(j.Name, rv.facts.Reviewer) {
			continue
		}

		id := strconv.FormatInt(j.ID, 10)
		concluded := slices.Contains([]string{"success", "failed", "canceled", "skipped", "manual"}, j.Status)

		review := remoteReview{
			ID:        id,
			Kind:      facts.ArrivesCheck,
			URL:       j.WebURL,
			Author:    j.Name,
			SHA:       pipeline.SHA,
			CreatedAt: j.CreatedAt,
			UpdatedAt: cmp.Or(j.FinishedAt, j.CreatedAt),
			concluded: concluded,
		}

		if concluded {
			if trace, err := rv.h.run(ctx, "api", "projects/:fullpath/jobs/"+id+"/trace"); err == nil {
				review.Body = strings.TrimSpace(trace)
			}
		}

		found = append(found, review)
	}

	return found, nil
}
