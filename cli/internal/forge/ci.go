package forge

import (
	"context"
	"time"
)

// CommitCI is how the checks on a commit ended, nil when it has none. On
// GitHub that is its check runs and its commit statuses; on GitLab its
// last pipeline.
func (h Host) CommitCI(ctx context.Context, sha string) (*CI, error) {
	if h.GLab() {
		var commit struct {
			LastPipeline *struct {
				Status    string    `json:"status"`
				UpdatedAt time.Time `json:"updated_at"`
			} `json:"last_pipeline"`
		}

		if err := h.Decode(ctx, &commit, "api", "projects/:fullpath/repository/commits/"+sha); err != nil {
			return nil, err
		}

		if p := commit.LastPipeline; p != nil {
			return &CI{Red: p.Status == "failed", At: p.UpdatedAt}, nil
		}

		return nil, nil
	}

	var runs struct {
		CheckRuns []struct {
			Conclusion  string    `json:"conclusion"`
			CompletedAt time.Time `json:"completed_at"`
		} `json:"check_runs"`
	}

	if err := h.Decode(ctx, &runs, "api", "repos/{owner}/{repo}/commits/"+sha+"/check-runs?per_page=100"); err != nil {
		return nil, err
	}

	var status struct {
		Statuses []struct {
			State     string    `json:"state"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"statuses"`
	}

	if err := h.Decode(ctx, &status, "api", "repos/{owner}/{repo}/commits/"+sha+"/status"); err != nil {
		return nil, err
	}

	var ci *CI

	seen := func(red bool, at time.Time) {
		if ci == nil {
			ci = &CI{}
		}

		if red && !ci.At.After(at) {
			ci.Red, ci.At = true, at
		}
	}

	for _, r := range runs.CheckRuns {
		if !r.CompletedAt.IsZero() {
			seen(r.Conclusion == "failure" || r.Conclusion == "timed_out", r.CompletedAt)
		}
	}

	for _, s := range status.Statuses {
		if s.State != "pending" {
			seen(s.State == "failure" || s.State == "error", s.UpdatedAt)
		}
	}

	return ci, nil
}
