package forge

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A ReviewPR is a request opened to read and answer the reviews left on it,
// with the commits a comment's sha is derived from (an issue comment stores
// none).
type ReviewPR struct {
	h Host

	Number  string
	HeadSHA string
	URL     string
	Commits []ReviewCommit // oldest first

	pipeline *headPipeline // glab only; nil until a pipeline has run

	owner, repo string // gh GraphQL needs them spelled out
	viewer      string // the login the CLI acts as, read once
}

// A ReviewCommit is one of the request's commits, by its commit date.
type ReviewCommit struct {
	SHA string
	At  time.Time
}

// headPipeline is the merge request's latest pipeline, as GitLab records it
// on the request itself: the pipelines listing has no sort order to ask for.
type headPipeline struct {
	ID  int64  `json:"id"`
	SHA string `json:"sha"`
}

// A Note is one reviewer's artifact as the host keeps it: an issue comment,
// an inline review (GitLab: the reviewer's open discussions, folded), or a
// check run (GitLab: a pipeline job).
type Note struct {
	ID        string
	URL       string
	Author    string // "" for GitLab discussions, which the caller names
	Body      string // an inline review's comments folded in
	Stored    string // the host's own text, which an edit rewrites
	SHA       string
	CreatedAt time.Time
	UpdatedAt time.Time

	Inline     bool     // an inline review or discussion, not an issue comment
	CommentID  string   // inline: the comment a reaction or marker lands on
	Threads    []string // inline: the threads it opened
	ThreadsErr error    // inline: why Threads could not be read
	Pending    bool     // inline: not yet submitted
	Concluded  bool     // check: finished; GitLab inline: every thread resolved
}

// OpenReviews reads the request number names, or the current branch's.
func (h Host) OpenReviews(ctx context.Context, number string) (*ReviewPR, error) {
	p := &ReviewPR{h: h}

	if h.glab() {
		if err := p.loadMergeRequest(ctx, number); err != nil {
			return nil, err
		}
	} else if err := p.loadPull(ctx, number); err != nil {
		return nil, err
	}

	sort.Slice(p.Commits, func(i, j int) bool { return p.Commits[i].At.Before(p.Commits[j].At) })

	return p, nil
}

// ShaBefore is the head a reviewer saw when it posted at t; the head sha
// when no commit precedes it.
func (p *ReviewPR) ShaBefore(t time.Time) string {
	sha := p.HeadSHA

	for _, c := range p.Commits {
		if !c.At.After(t) {
			sha = c.SHA
		}
	}

	return sha
}

// issuePath and mrPath spell the REST paths each CLI expands: gh fills
// {owner}/{repo}, glab fills :fullpath.
func issuePath(tail string) string { return "repos/{owner}/{repo}/" + tail }

// IsLogin matches a declared reviewer against a login. GitLab names a bot
// by username alone, with no suffix anyone could forge, so a declared
// `[bot]` is dropped there; on GitHub SameLogin's rule stands.
func (h Host) IsLogin(declared, login string) bool {
	if h.glab() {
		declared = strings.TrimSuffix(declared, "[bot]")
	}

	return SameLogin(declared, login)
}

// Comments is every issue comment, or merge request note, by an author
// want accepts. Every page is read: the oldest come first, so a review past
// the first page is the newest one.
func (p *ReviewPR) Comments(ctx context.Context, want func(login string) bool) ([]Note, error) {
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

	path := issuePath("issues/" + p.Number + "/comments?per_page=100")
	if p.h.glab() {
		path = p.mrPath("notes?per_page=100")
	}

	notes, err := decodePages[note](ctx, p.h, path)
	if err != nil {
		return nil, err
	}

	var found []Note

	for _, n := range notes {
		login := cmp.Or(n.User.Login, n.Author.Username)
		if n.System || !want(login) {
			continue
		}

		id := strconv.FormatInt(n.ID, 10)

		found = append(found, Note{
			ID:        id,
			URL:       cmp.Or(n.HTMLURL, p.URL+"#note_"+id),
			Author:    login,
			Body:      n.Body,
			Stored:    n.Body,
			SHA:       p.ShaBefore(n.CreatedAt),
			CreatedAt: n.CreatedAt,
			UpdatedAt: cmp.Or(n.UpdatedAt, n.CreatedAt),
		})
	}

	return found, nil
}

// InlineReviews is every submitted or pending review by an author want
// accepts, its line comments folded into the body. GitLab has no review
// object, so its reviewer's resolvable discussions fold into one.
func (p *ReviewPR) InlineReviews(ctx context.Context, want func(login string) bool) ([]Note, error) {
	if p.h.glab() {
		return p.discussions(ctx, want)
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

	reviews, err := decodePages[pullReview](ctx, p.h, issuePath("pulls/"+p.Number+"/reviews?per_page=100"))
	if err != nil {
		return nil, err
	}

	var found []Note

	for _, r := range reviews {
		if !want(r.User.Login) {
			continue
		}

		review := Note{
			ID:        strconv.FormatInt(r.ID, 10),
			URL:       r.HTMLURL,
			Author:    r.User.Login,
			Body:      r.Body,
			Stored:    r.Body,
			SHA:       r.CommitID,
			CreatedAt: r.SubmittedAt,
			UpdatedAt: r.SubmittedAt,
			Inline:    true,
			Pending:   strings.EqualFold(r.State, "PENDING"),
		}

		if err := p.foldReviewComments(ctx, &review); err != nil {
			return nil, err
		}

		found = append(found, review)
	}

	return found, nil
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

// ThreadsResolved reports whether every thread n opened is resolved. gh
// threads are read again, since their ids were gathered without their
// state; glab discussions carry the flag, folded into Concluded.
func (p *ReviewPR) ThreadsResolved(ctx context.Context, n Note) (bool, error) {
	if !n.Inline || len(n.Threads) == 0 {
		return false, nil
	}

	if p.h.glab() {
		return n.Concluded, nil
	}

	threads, err := p.reviewThreads(ctx)
	if err != nil {
		return false, err
	}

	for _, t := range threads {
		if slices.Contains(n.Threads, t.id) && !t.resolved {
			return false, nil
		}
	}

	return true, nil
}

// ResolveThreads resolves each of the threads, stopping at the first failure.
func (p *ReviewPR) ResolveThreads(ctx context.Context, threads []string) error {
	for _, id := range threads {
		var err error

		if p.h.glab() {
			_, err = p.h.run(ctx, "api", "-X", "PUT", p.mrPath("discussions/"+id+"?resolved=true"))
		} else {
			_, err = p.h.run(ctx, "api", "graphql", "-f", "query="+resolveThreadMutation, "-F", "id="+id)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// Checks is every check run on the head named name, or on GitLab every job
// of the head pipeline; a finished job's log is its body.
func (p *ReviewPR) Checks(ctx context.Context, name string) ([]Note, error) {
	if p.h.glab() {
		return p.pipelineJobs(ctx, name)
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

	err := p.h.decode(ctx, &payload, "api", issuePath("commits/"+p.HeadSHA+"/check-runs?per_page=100"))
	if err != nil {
		return nil, err
	}

	var found []Note

	for _, c := range payload.CheckRuns {
		if !strings.EqualFold(c.Name, name) {
			continue
		}

		body := strings.TrimSpace(strings.Join([]string{c.Output.Title, c.Output.Summary, c.Output.Text}, "\n\n"))

		found = append(found, Note{
			ID:        strconv.FormatInt(c.ID, 10),
			URL:       c.HTMLURL,
			Author:    c.Name,
			Body:      body,
			SHA:       c.HeadSHA,
			CreatedAt: c.StartedAt,
			UpdatedAt: cmp.Or(c.CompletedAt, c.StartedAt),
			Concluded: c.Status == "completed",
		})
	}

	return found, nil
}

// RequestReview asks login for a review of the request.
func (p *ReviewPR) RequestReview(ctx context.Context, login string) error {
	args := []string{
		"api", "-X", "POST", issuePath("pulls/" + p.Number + "/requested_reviewers"),
		"-f", "reviewers[]=" + login,
	}
	if p.h.glab() {
		args = []string{"mr", "update", p.Number, "--reviewer", "+" + login}
	}

	_, err := p.h.run(ctx, args...)

	return err
}

// Comment posts text on the request and returns the comment's url and the
// host's time for it, zero when the host gives none.
func (p *ReviewPR) Comment(ctx context.Context, text string) (string, time.Time, error) {
	path := issuePath("issues/" + p.Number + "/comments")
	if p.h.glab() {
		path = p.mrPath("notes")
	}

	var posted struct {
		ID        int64     `json:"id"`
		HTMLURL   string    `json:"html_url"`
		CreatedAt time.Time `json:"created_at"`
	}

	if err := p.h.decode(ctx, &posted, "api", "-X", "POST", path, "-f", "body="+text); err != nil {
		return "", time.Time{}, err
	}

	return cmp.Or(posted.HTMLURL, p.URL+"#note_"+strconv.FormatInt(posted.ID, 10)), posted.CreatedAt, nil
}

// Viewer is who the CLI acts as: the only login whose reaction counts.
func (p *ReviewPR) Viewer(ctx context.Context) (string, error) {
	if p.viewer != "" {
		return p.viewer, nil
	}

	login, err := p.h.Login(ctx)
	if err != nil {
		return "", err
	}

	p.viewer = login

	return p.viewer, nil
}

// FreshReactions is the set of reactions the viewer left on n since its
// last edit; nil for an inline review with no comment to react to.
func (p *ReviewPR) FreshReactions(ctx context.Context, n Note) (map[string]bool, error) {
	viewer, err := p.Viewer(ctx)
	if err != nil {
		return nil, err
	}

	path, ok := p.reactionPath(n)
	if !ok {
		return nil, nil
	}

	// gh calls them reactions with a content, glab award emoji with a name
	var reactions []struct {
		Content   string    `json:"content"`
		Name      string    `json:"name"`
		CreatedAt time.Time `json:"created_at"`
		User      user      `json:"user"`
	}

	if err := p.h.decode(ctx, &reactions, "api", path); err != nil {
		return nil, err
	}

	fresh := map[string]bool{}

	for _, re := range reactions {
		if SameLogin(viewer, re.User.name()) && !re.CreatedAt.Before(n.UpdatedAt) {
			fresh[cmp.Or(re.Content, re.Name)] = true
		}
	}

	return fresh, nil
}

// React leaves emoji on n.
func (p *ReviewPR) React(ctx context.Context, n Note, emoji string) error {
	path, ok := p.reactionPath(n)
	if !ok {
		return errors.New("the review has no comment to react to")
	}

	path, _, _ = strings.Cut(path, "?")

	field := "content=" + emoji
	if p.h.glab() {
		field = "name=" + emoji
	}

	_, err := p.h.run(ctx, "api", "-X", "POST", path, "-f", field)

	return err
}

// EditBody replaces the text of n, the note's own on GitLab.
func (p *ReviewPR) EditBody(ctx context.Context, n Note, body string) error {
	var args []string

	switch {
	case p.h.glab():
		args = []string{"api", "-X", "PUT", p.mrPath("notes/" + cmp.Or(n.CommentID, n.ID)), "-f", "body=" + body}
	case n.Inline:
		args = []string{
			"api", "-X", "PUT", issuePath("pulls/" + p.Number + "/reviews/" + n.ID), "-f", "body=" + body,
		}
	default:
		args = []string{"api", "-X", "PATCH", issuePath("issues/comments/" + n.ID), "-f", "body=" + body}
	}

	_, err := p.h.run(ctx, args...)

	return err
}

func (p *ReviewPR) loadPull(ctx context.Context, number string) error {
	var pr struct {
		Number     int    `json:"number"`
		URL        string `json:"url"`
		HeadRefOid string `json:"headRefOid"`
		Commits    []struct {
			OID           string    `json:"oid"`
			CommittedDate time.Time `json:"committedDate"`
		} `json:"commits"`
	}

	args := []string{"pr", "view"}
	if number != "" {
		args = append(args, number)
	}

	if err := p.h.decode(ctx, &pr, append(args, "--json", "number,url,headRefOid,commits")...); err != nil {
		return err
	}

	p.Number, p.HeadSHA, p.URL = strconv.Itoa(pr.Number), pr.HeadRefOid, pr.URL
	for _, c := range pr.Commits {
		p.Commits = append(p.Commits, ReviewCommit{SHA: c.OID, At: c.CommittedDate})
	}

	return nil
}

func (p *ReviewPR) loadMergeRequest(ctx context.Context, number string) error {
	var mr struct {
		IID          int           `json:"iid"`
		WebURL       string        `json:"web_url"`
		SHA          string        `json:"sha"`
		HeadPipeline *headPipeline `json:"head_pipeline"`
	}

	args := []string{"mr", "view"}
	if number != "" {
		args = append(args, number)
	}

	if err := p.h.decode(ctx, &mr, append(args, "-F", "json")...); err != nil {
		return err
	}

	p.Number, p.HeadSHA, p.URL, p.pipeline = strconv.Itoa(mr.IID), mr.SHA, mr.WebURL, mr.HeadPipeline

	type mrCommit struct {
		ID            string    `json:"id"`
		CommittedDate time.Time `json:"committed_date"`
	}

	commits, err := decodePages[mrCommit](ctx, p.h, p.mrPath("commits?per_page=100"))
	if err != nil {
		return err
	}

	for _, c := range commits {
		p.Commits = append(p.Commits, ReviewCommit{SHA: c.ID, At: c.CommittedDate})
	}

	return nil
}

func (p *ReviewPR) mrPath(tail string) string {
	return "projects/:fullpath/merge_requests/" + p.Number + "/" + tail
}

func (p *ReviewPR) foldReviewComments(ctx context.Context, review *Note) error {
	type reviewComment struct {
		ID        int64     `json:"id"`
		Path      string    `json:"path"`
		Line      int       `json:"line"`
		Body      string    `json:"body"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	comments, err := decodePages[reviewComment](
		ctx, p.h, issuePath("pulls/"+p.Number+"/reviews/"+review.ID+"/comments?per_page=100"),
	)
	if err != nil {
		return err
	}

	var b strings.Builder

	b.WriteString(review.Body)

	for _, c := range comments {
		if review.CommentID == "" {
			review.CommentID = strconv.FormatInt(c.ID, 10)
		}

		if c.UpdatedAt.After(review.UpdatedAt) {
			review.UpdatedAt = c.UpdatedAt
		}

		fmt.Fprintf(&b, "\n\n### %s:%d\n\n%s", c.Path, c.Line, strings.TrimSpace(c.Body))
	}

	review.Body = strings.TrimSpace(b.String())

	if len(comments) > 0 {
		// a lookup problem must not fail a pick: the error is kept for the
		// thread ack to report, the only step that needs the ids
		review.Threads, review.ThreadsErr = p.threadsOf(ctx, review.ID)
	}

	return nil
}

func (p *ReviewPR) threadsOf(ctx context.Context, reviewID string) ([]string, error) {
	threads, err := p.reviewThreads(ctx)
	if err != nil {
		return nil, err
	}

	var ids []string

	for _, t := range threads {
		if t.review == reviewID {
			ids = append(ids, t.id)
		}
	}

	return ids, nil
}

func (p *ReviewPR) reviewThreads(ctx context.Context) ([]reviewThread, error) {
	if err := p.loadRepo(ctx); err != nil {
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

	err := p.h.decode(ctx, &payload, "api", "graphql", "-f", "query="+reviewThreadsQuery,
		"-F", "owner="+p.owner, "-F", "name="+p.repo, "-F", "number="+p.Number)
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

func (p *ReviewPR) loadRepo(ctx context.Context) error {
	if p.owner != "" {
		return nil
	}

	var repo struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}

	if err := p.h.decode(ctx, &repo, "repo", "view", "--json", "owner,name"); err != nil {
		return err
	}

	p.owner, p.repo = repo.Owner.Login, repo.Name

	return nil
}

// discussions folds the reviewer's resolvable discussions into one review:
// GitLab has no review object grouping them, so "the latest review" is the
// set still open.
func (p *ReviewPR) discussions(ctx context.Context, want func(login string) bool) ([]Note, error) {
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

	discussions, err := decodePages[discussion](ctx, p.h, p.mrPath("discussions?per_page=100"))
	if err != nil {
		return nil, err
	}

	review := Note{URL: p.URL, Inline: true, Concluded: true}

	var b strings.Builder

	for _, d := range discussions {
		if len(d.Notes) == 0 {
			continue
		}

		first := d.Notes[0]
		if first.System || !first.Resolvable || !want(first.Author.Username) {
			continue
		}

		review.Threads = append(review.Threads, d.ID)
		review.Concluded = review.Concluded && first.Resolved

		// a marker or checkbox lands on this note, so its own text is the
		// one an edit rewrites
		if review.CommentID == "" {
			review.CommentID = strconv.FormatInt(first.ID, 10)
			review.ID = d.ID
			review.Stored = first.Body
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

	if len(review.Threads) == 0 {
		return nil, nil
	}

	review.Body = strings.TrimSpace(b.String())
	review.SHA = cmp.Or(review.SHA, p.ShaBefore(review.CreatedAt))

	return []Note{review}, nil
}

func (p *ReviewPR) pipelineJobs(ctx context.Context, name string) ([]Note, error) {
	pipeline := p.pipeline
	if pipeline == nil {
		return nil, nil
	}

	type job struct {
		ID         int64     `json:"id"`
		Name       string    `json:"name"`
		Status     string    `json:"status"`
		WebURL     string    `json:"web_url"`
		CreatedAt  time.Time `json:"created_at"`
		FinishedAt time.Time `json:"finished_at"`
	}

	jobs, err := decodePages[job](
		ctx, p.h, "projects/:fullpath/pipelines/"+strconv.FormatInt(pipeline.ID, 10)+"/jobs?per_page=100",
	)
	if err != nil {
		return nil, err
	}

	var found []Note

	for _, j := range jobs {
		if !strings.EqualFold(j.Name, name) {
			continue
		}

		id := strconv.FormatInt(j.ID, 10)
		concluded := slices.Contains([]string{"success", "failed", "canceled", "skipped", "manual"}, j.Status)

		n := Note{
			ID:        id,
			URL:       j.WebURL,
			Author:    j.Name,
			SHA:       pipeline.SHA,
			CreatedAt: j.CreatedAt,
			UpdatedAt: cmp.Or(j.FinishedAt, j.CreatedAt),
			Concluded: concluded,
		}

		if concluded {
			if trace, err := p.h.run(ctx, "api", "projects/:fullpath/jobs/"+id+"/trace"); err == nil {
				n.Body = strings.TrimSpace(trace)
			}
		}

		found = append(found, n)
	}

	return found, nil
}

// reactionPath is where n's reactions live; ok is false for an inline
// review with no comment to react to.
func (p *ReviewPR) reactionPath(n Note) (string, bool) {
	id := n.ID
	if n.Inline {
		if n.CommentID == "" {
			return "", false
		}

		id = n.CommentID
	}

	if p.h.glab() {
		return p.mrPath("notes/" + id + "/award_emoji?per_page=100"), true
	}

	if n.Inline {
		return issuePath("pulls/comments/" + id + "/reactions?per_page=100"), true
	}

	return issuePath("issues/comments/" + id + "/reactions?per_page=100"), true
}
