package main

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

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
)

func (rv *reviewer) loadPull(ctx context.Context, number string) error {
	if rv.h.GLab() {
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

	if err := rv.h.Decode(ctx, &p, rv.h.prArgs(number, "--json", "number,url,headRefOid,commits")...); err != nil {
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
		IID          int           `json:"iid"`
		WebURL       string        `json:"web_url"`
		SHA          string        `json:"sha"`
		HeadPipeline *headPipeline `json:"head_pipeline"`
	}

	if err := rv.h.Decode(ctx, &mr, rv.h.prArgs(number, "-F", "json")...); err != nil {
		return err
	}

	rv.pull = reviewPull{
		Number:   strconv.Itoa(mr.IID),
		HeadSHA:  mr.SHA,
		URL:      mr.WebURL,
		Pipeline: mr.HeadPipeline,
	}

	type mrCommit struct {
		ID            string    `json:"id"`
		CommittedDate time.Time `json:"committed_date"`
	}

	commits, err := forge.DecodePages[mrCommit](ctx, rv.h.Host, rv.mrPath("commits?per_page=100"))
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

// requestReview: neither host dates the request, so the local clock stands
// in; a human's review comes long after.
func (rv *reviewer) requestReview(ctx context.Context, login string) (string, string, time.Time, error) {
	at := time.Now().UTC().Truncate(time.Second)

	args := []string{
		"api", "-X", "POST", rv.issuePath("pulls/" + rv.pull.Number + "/requested_reviewers"),
		"-f", "reviewers[]=" + login,
	}
	if rv.h.GLab() {
		args = []string{"mr", "update", rv.pull.Number, "--reviewer", "+" + login}
	}

	if _, err := rv.h.Run(ctx, args...); err != nil {
		return "", "", time.Time{}, err
	}

	return "requested a review from " + login, rv.pull.URL, at, nil
}

// freshReactions is read once so the handled and settled checks share one
// call.
func (rv *reviewer) freshReactions(ctx context.Context, r remoteReview) (map[string]bool, error) {
	viewer, err := rv.viewerLogin(ctx)
	if err != nil {
		return nil, err
	}

	path, ok := rv.reactionPath(r)
	if !ok {
		return nil, nil
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

	if err := rv.h.Decode(ctx, &reactions, "api", path); err != nil {
		return nil, err
	}

	fresh := map[string]bool{}

	for _, re := range reactions {
		if forge.SameLogin(viewer, cmp.Or(re.User.Login, re.User.Username)) && !re.CreatedAt.Before(r.UpdatedAt) {
			fresh[cmp.Or(re.Content, re.Name)] = true
		}
	}

	return fresh, nil
}

func (rv *reviewer) reactionPath(r remoteReview) (string, bool) {
	id := r.ID
	if r.Kind == facts.ArrivesInline {
		if r.commentID == "" {
			return "", false
		}

		id = r.commentID
	}

	if rv.h.GLab() {
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

	login, err := rv.h.Login(ctx)
	if err != nil {
		return "", err
	}

	rv.viewer = login

	return rv.viewer, nil
}

func (rv *reviewer) react(ctx context.Context, r remoteReview, emoji string) (string, error) {
	path, ok := rv.reactionPath(r)
	if !ok {
		return "", errors.New("the review has no comment to react to")
	}

	path, _, _ = strings.Cut(path, "?")

	field := "content=" + emoji
	if rv.h.GLab() {
		field = "name=" + emoji
	}

	if _, err := rv.h.Run(ctx, "api", "-X", "POST", path, "-f", field); err != nil {
		return "", err
	}

	return "reaction " + emoji, nil
}

func (rv *reviewer) patchBody(ctx context.Context, r remoteReview, body, what string) (string, error) {
	var args []string

	switch {
	case rv.h.GLab():
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

	if _, err := rv.h.Run(ctx, args...); err != nil {
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

	if r.threadsErr != nil {
		return "", fmt.Errorf("reading the review's threads: %w", r.threadsErr)
	}

	if len(r.threads) == 0 {
		return "", errors.New("the review opened no threads to resolve")
	}

	for _, id := range r.threads {
		var err error

		if rv.h.GLab() {
			_, err = rv.h.Run(ctx, "api", "-X", "PUT", rv.mrPath("discussions/"+id+"?resolved=true"))
		} else {
			_, err = rv.h.Run(
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
	if rv.h.GLab() {
		path = rv.mrPath("notes?per_page=100")
	}

	// every page: the oldest come first, so a review past the first page is
	// the newest one
	notes, err := forge.DecodePages[note](ctx, rv.h.Host, path)
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

func (rv *reviewer) inlineReviews(ctx context.Context) ([]remoteReview, error) {
	if rv.h.GLab() {
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

	reviews, err := forge.DecodePages[pullReview](
		ctx,
		rv.h.Host,
		rv.issuePath("pulls/"+rv.pull.Number+"/reviews?per_page=100"),
	)
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

	comments, err := forge.DecodePages[reviewComment](
		ctx,
		rv.h.Host,
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
		// a lookup problem must not fail a pick: the error is kept for the
		// thread ack to report, the only step that needs the ids
		review.threads, review.threadsErr = rv.threadsOf(ctx, review.ID)
	}

	return nil
}

func (rv *reviewer) threadsOf(ctx context.Context, reviewID string) ([]string, error) {
	threads, err := rv.reviewThreads(ctx)
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

	err := rv.h.Decode(ctx, &payload, "api", "graphql", "-f", "query="+reviewThreadsQuery,
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

	if err := rv.h.Decode(ctx, &repo, "repo", "view", "--json", "owner,name"); err != nil {
		return err
	}

	rv.owner, rv.repo = repo.Owner.Login, repo.Name

	return nil
}

// threadsResolved re-reads gh threads, whose ids were gathered without their
// state; glab discussions carry the flag themselves.
func (rv *reviewer) threadsResolved(ctx context.Context, r remoteReview) (bool, error) {
	if r.Kind != facts.ArrivesInline || len(r.threads) == 0 {
		return false, nil
	}

	if rv.h.GLab() {
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

// discussions folds the reviewer's resolvable discussions into one review:
// GitLab has no review object grouping them, so "the latest review" is the
// set still open.
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

	discussions, err := forge.DecodePages[discussion](ctx, rv.h.Host, rv.mrPath("discussions?per_page=100"))
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

func (rv *reviewer) checkRuns(ctx context.Context) ([]remoteReview, error) {
	if rv.h.GLab() {
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

	err := rv.h.Decode(ctx, &payload, "api", rv.issuePath("commits/"+rv.pull.HeadSHA+"/check-runs?per_page=100"))
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
	pipeline := rv.pull.Pipeline
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

	jobs, err := forge.DecodePages[job](
		ctx,
		rv.h.Host,
		"projects/:fullpath/pipelines/"+strconv.FormatInt(pipeline.ID, 10)+"/jobs?per_page=100",
	)
	if err != nil {
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
			if trace, err := rv.h.Run(ctx, "api", "projects/:fullpath/jobs/"+id+"/trace"); err == nil {
				review.Body = strings.TrimSpace(trace)
			}
		}

		found = append(found, review)
	}

	return found, nil
}

// prArgs: GitHub has pull requests, GitLab merge requests.
func (h host) prArgs(number string, tail ...string) []string {
	args := h.Pick([]string{"pr", "view"}, []string{"mr", "view"})

	if number != "" {
		args = append(args, number)
	}

	return append(args, tail...)
}
