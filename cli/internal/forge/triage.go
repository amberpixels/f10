package forge

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// actionsRun is the Actions run id in a check run's details url
// (.../actions/runs/<id>/job/<job>), or "" for a check from another app.
func actionsRun(u string) string {
	_, rest, ok := strings.Cut(u, "/actions/runs/")
	if !ok {
		return ""
	}

	id, _, _ := strings.Cut(rest, "/")
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return ""
	}

	return id
}

// OpenChecks reads the checks on branch's open request, with its url; a
// branch with none open gives "" and no checks.
func (h Host) OpenChecks(ctx context.Context, branch string) (HeadChecks, string, error) {
	pulls, err := h.PullsByHead(ctx, branch)
	if err != nil {
		return HeadChecks{}, "", err
	}

	for _, p := range pulls {
		if p.Status == Open {
			hc, err := h.HeadChecks(ctx, p)

			return hc, p.URL, err
		}
	}

	return HeadChecks{}, "", nil
}

// jobID is the job id at the end of a check's log url: GitHub's
// .../job/<id>, GitLab's .../-/jobs/<id>; "" for none.
func jobID(u string) string {
	for _, marker := range []string{"/job/", "/-/jobs/"} {
		if _, rest, ok := strings.Cut(u, marker); ok {
			id, _, _ := strings.Cut(rest, "/")
			id, _, _ = strings.Cut(id, "?")

			if _, err := strconv.ParseInt(id, 10, 64); err == nil {
				return id
			}
		}
	}

	return ""
}

// FailedLog is the log of a failed check's job: on GitHub its failed
// steps (`gh run view --log-failed`), on GitLab the job's trace.
func (h Host) FailedLog(ctx context.Context, c Check) (string, error) {
	job := jobID(c.URL)

	switch {
	case h.glab() && job != "":
		return h.run(ctx, "ci", "trace", job)
	case !h.glab() && job != "":
		return h.run(ctx, "run", "view", "--job", job, "--log-failed")
	case !h.glab() && c.Run != "":
		return h.run(ctx, "run", "view", c.Run, "--log-failed")
	}

	return "", fmt.Errorf("%s names no job whose log %s can read: open %s", c.Name, h.Name, c.URL)
}

// DefaultJobQuery reads one check's runs on the default branch's last
// five commits, newest first, so a tip still running falls back to its
// parent.
const DefaultJobQuery = "query($owner: String!, $name: String!, $check: String!) { repository(owner: $owner, name: $name) " +
	"{ defaultBranchRef { target { ... on Commit { history(first: 5) { nodes { checkSuites(first: 50) { nodes " +
	"{ checkRuns(first: 1, filterBy: {checkName: $check}) { nodes { status conclusion } } } } } } } } } } }"

// defaultJobDepth is how many default-branch commits or pipelines are read
// for a job's last result.
const defaultJobDepth = 5

// DefaultJob is how the job named name last ended on the default branch:
// Green, Red, or "" when none of its recent runs there completed. A
// cancelled run is skipped, since a newer push cancelling it says nothing
// of whether the job passes.
func (h Host) DefaultJob(ctx context.Context, name string) (string, error) {
	if h.glab() {
		return h.defaultPipelineJob(ctx, name)
	}

	var payload struct {
		Data struct {
			Repository struct {
				DefaultBranchRef struct {
					Target struct {
						History struct {
							Nodes []struct {
								CheckSuites struct {
									Nodes []struct {
										CheckRuns struct {
											Nodes []checkContext `json:"nodes"`
										} `json:"checkRuns"`
									} `json:"nodes"`
								} `json:"checkSuites"`
							} `json:"nodes"`
						} `json:"history"`
					} `json:"target"`
				} `json:"defaultBranchRef"`
			} `json:"repository"`
		} `json:"data"`
	}

	err := h.decode(ctx, &payload, "api", "graphql", "-f", "query="+DefaultJobQuery,
		"-F", "owner={owner}", "-F", "name={repo}", "-f", "check="+name)
	if err != nil {
		return "", err
	}

	for _, commit := range payload.Data.Repository.DefaultBranchRef.Target.History.Nodes {
		for _, suite := range commit.CheckSuites.Nodes {
			for _, run := range suite.CheckRuns.Nodes {
				if v := run.verdict(); v != Pending && !strings.EqualFold(run.Conclusion, "CANCELLED") {
					return v, nil
				}
			}
		}
	}

	return "", nil
}

type pipelineJob struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (h Host) defaultPipelineJob(ctx context.Context, name string) (string, error) {
	branch, err := h.DefaultBranchName(ctx)
	if err != nil {
		return "", err
	}

	var pipelines []struct {
		ID int64 `json:"id"`
	}

	err = h.decode(ctx, &pipelines, "api", "projects/:fullpath/pipelines?ref="+url.QueryEscape(branch)+
		"&scope=finished&per_page="+strconv.Itoa(defaultJobDepth))
	if err != nil {
		return "", err
	}

	for _, pl := range pipelines {
		var jobs []pipelineJob

		err := h.decode(
			ctx,
			&jobs,
			"api",
			"projects/:fullpath/pipelines/"+strconv.FormatInt(pl.ID, 10)+"/jobs?per_page=100",
		)
		if err != nil {
			return "", err
		}

		i := slices.IndexFunc(jobs, func(j pipelineJob) bool { return j.Name == name })
		if i < 0 || strings.EqualFold(jobs[i].Status, "canceled") {
			continue
		}

		switch s := jobState(jobs[i].Status); s {
		case Green, Red:
			return s, nil
		}
	}

	return "", nil
}

// Rerun reruns the failed jobs of each run the failed checks belong to,
// the way `gh run rerun --failed` does; a check with no run is skipped.
// It returns the runs it reran.
func (h Host) Rerun(ctx context.Context, failed []Check) ([]string, error) {
	var runs []string

	for _, c := range failed {
		if c.Run != "" && !slices.Contains(runs, c.Run) {
			runs = append(runs, c.Run)
		}
	}

	for _, run := range runs {
		args := []string{"run", "rerun", run, "--failed"}
		if h.glab() {
			args = []string{"api", "-X", "POST", "projects/:fullpath/pipelines/" + run + "/retry"}
		}

		if _, err := h.run(ctx, args...); err != nil {
			return nil, err
		}
	}

	return runs, nil
}
