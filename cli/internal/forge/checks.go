package forge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// HeadChecks is the CI state of a request's head commit, as the checks that
// count for a merge read it. It belongs to SHA alone, so nothing keeps a copy.
type HeadChecks struct {
	State    string  `json:"state"` // Green, Red, Pending or "" with no checks
	SHA      string  `json:"sha"`
	Required bool    `json:"required"` // the counted set is the target branch's required checks
	Failed   []Check `json:"failed,omitempty"`
	Running  []Check `json:"running,omitempty"`
}

// A Check is one check run, status or pipeline job, with its log url.
type Check struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Names is the checks' names, comma-joined.
func Names(checks []Check) string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}

	return strings.Join(names, ", ")
}

// HeadChecksQuery reads every check context on a request's head commit,
// each with whether the request's target branch requires it, a page of 100
// at a time; prev is the last two commits' rollups, to tell a head whose
// checks have not registered yet from a repository with none.
const HeadChecksQuery = "query($owner: String!, $name: String!, $number: Int!, $endCursor: String) " +
	"{ repository(owner: $owner, name: $name) { pullRequest(number: $number) { " +
	"prev: commits(last: 2) { nodes { commit { statusCheckRollup { state } } } } " +
	"head: commits(last: 1) { nodes { commit { oid statusCheckRollup { " +
	"contexts(first: 100, after: $endCursor) { pageInfo { hasNextPage endCursor } nodes { __typename " +
	"... on CheckRun { name status conclusion detailsUrl isRequired(pullRequestNumber: $number) } " +
	"... on StatusContext { context state targetUrl isRequired(pullRequestNumber: $number) } " +
	"} } } } } } } } }"

// checksPage is one page of HeadChecksQuery.
type checksPage struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				Prev struct {
					Nodes []struct {
						Commit struct {
							Rollup *struct{} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"prev"`
				Head struct {
					Nodes []struct {
						Commit struct {
							OID    string `json:"oid"`
							Rollup *struct {
								Contexts struct {
									Nodes []checkContext `json:"nodes"`
								} `json:"contexts"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"head"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// HeadChecks reads the checks on p's head. On GitHub the target branch's
// required checks count when it declares any, else all of them, since a
// branch with no protection would otherwise gate on nothing; on GitLab the
// head pipeline decides, as GitLab has no required flag. A head with no
// checks whose previous commit had some is Pending: its CI has not started.
func (h Host) HeadChecks(ctx context.Context, p PR) (HeadChecks, error) {
	if h.glab() {
		return h.pipelineChecks(ctx, p)
	}

	raw, err := h.run(ctx, "api", "graphql", "--paginate", "-f", "query="+HeadChecksQuery,
		"-F", "owner={owner}", "-F", "name={repo}", "-F", "number="+strconv.Itoa(p.Number))
	if err != nil {
		return HeadChecks{}, err
	}

	out := HeadChecks{SHA: p.HeadSHA}

	var (
		counted         []checkContext
		rolled, prevHad bool
	)

	// gh prints one document per page
	dec := json.NewDecoder(strings.NewReader(raw))

	for {
		var page checksPage
		if err := dec.Decode(&page); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return HeadChecks{}, fmt.Errorf("parsing %s output: %w", h.Name, err)
		}

		pr := page.Data.Repository.PullRequest
		if prev := pr.Prev.Nodes; len(prev) == 2 && prev[0].Commit.Rollup != nil {
			prevHad = true
		}

		if len(pr.Head.Nodes) == 0 {
			continue
		}

		head := pr.Head.Nodes[0].Commit
		out.SHA = head.OID

		if head.Rollup != nil {
			rolled = true
			counted = append(counted, head.Rollup.Contexts.Nodes...)
		}
	}

	if !rolled {
		if prevHad {
			out.State = Pending
		}

		return out, nil
	}

	for _, c := range counted {
		if c.Required {
			out.Required = true

			break
		}
	}

	for _, c := range counted {
		if out.Required && !c.Required {
			continue
		}

		out.add(c.verdict(), Check{Name: c.name(), URL: c.url()})
	}

	return out, nil
}

// checkContext is a CheckRun or a StatusContext, tagged for both.
type checkContext struct {
	Type       string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"detailsUrl"`
	Context    string `json:"context"`
	State      string `json:"state"`
	TargetURL  string `json:"targetUrl"`
	Required   bool   `json:"isRequired"`
}

func (c checkContext) name() string {
	if c.Type == "StatusContext" {
		return c.Context
	}

	return c.Name
}

func (c checkContext) url() string {
	if c.Type == "StatusContext" {
		return c.TargetURL
	}

	return c.DetailsURL
}

// verdict is Red, Pending or Green; skipped and neutral runs pass.
func (c checkContext) verdict() string {
	if c.Type == "StatusContext" {
		switch strings.ToUpper(c.State) {
		case "ERROR", "FAILURE":
			return Red
		case "SUCCESS":
			return Green
		default: // PENDING, and EXPECTED: a required status not reported yet
			return Pending
		}
	}

	if !strings.EqualFold(c.Status, "COMPLETED") {
		return Pending
	}

	switch strings.ToUpper(c.Conclusion) {
	case "FAILURE", "TIMED_OUT", "CANCELLED", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return Red
	default:
		return Green
	}
}

// add counts one check: Red wins over Pending, Pending over Green.
func (hc *HeadChecks) add(verdict string, c Check) {
	switch verdict {
	case Red:
		hc.State = Red
		hc.Failed = append(hc.Failed, c)
	case Pending:
		if hc.State != Red {
			hc.State = Pending
		}

		hc.Running = append(hc.Running, c)
	default:
		if hc.State == "" {
			hc.State = Green
		}
	}
}

// pipelineChecks reads a merge request's head pipeline and, for a failed or
// running one, the jobs that make it so; a job allowed to fail never counts.
// A skipped pipeline, or one stopped at a manual job, ran nothing to wait for.
func (h Host) pipelineChecks(ctx context.Context, p PR) (HeadChecks, error) {
	var mr struct {
		SHA          string `json:"sha"`
		HeadPipeline *struct {
			ID     int64  `json:"id"`
			SHA    string `json:"sha"`
			Status string `json:"status"`
		} `json:"head_pipeline"`
		// the request's latest pipeline, on any of its commits
		Pipeline *struct{} `json:"pipeline"`
	}

	// by branch, the selector PR reads by, falling back to the number
	if err := h.decode(ctx, &mr, "mr", "view", cmp.Or(p.Head, strconv.Itoa(p.Number)), "-F", "json"); err != nil {
		return HeadChecks{}, err
	}

	out := HeadChecks{SHA: mr.SHA}

	// an earlier commit's pipeline and none on the head: the head's has not started
	pl := mr.HeadPipeline
	switch {
	case pl == nil && mr.Pipeline != nil, pl != nil && pl.SHA != "" && mr.SHA != "" && pl.SHA != mr.SHA:
		out.State = Pending

		return out, nil
	case pl == nil:
		return out, nil
	}

	out.SHA = cmp.Or(out.SHA, pl.SHA)

	out.State = jobState(pl.Status)
	if out.State == Green || out.State == "" {
		return out, nil
	}

	type job struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		WebURL       string `json:"web_url"`
		AllowFailure bool   `json:"allow_failure"`
	}

	jobs, err := decodePages[job](
		ctx,
		h,
		"projects/:fullpath/pipelines/"+strconv.FormatInt(pl.ID, 10)+"/jobs?per_page=100",
	)
	if err != nil {
		return HeadChecks{}, err
	}

	for _, j := range jobs {
		c := Check{Name: j.Name, URL: j.WebURL}

		switch s := jobState(j.Status); {
		case s == Red && !j.AllowFailure:
			out.Failed = append(out.Failed, c)
		case s == Pending:
			out.Running = append(out.Running, c)
		}
	}

	return out, nil
}

// pipelineState reads a GitLab pipeline's status.
func pipelineState(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return Green
	case "failed", "canceled":
		return Red
	case "":
		return ""
	default:
		return Pending
	}
}

// jobState reads a job's status. Manual and skipped jobs never ran, so
// they hold nothing up.
func jobState(status string) string {
	switch strings.ToLower(status) {
	case "skipped", "manual":
		return Green
	default:
		return pipelineState(status)
	}
}
