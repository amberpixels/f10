package archive

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// JudgeOutcome is a verdict and, from its file, what the ship run did with
// it: the decision line and how many changes it listed.
type JudgeOutcome struct {
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason,omitempty"`
	Decision string `json:"decision,omitempty"`
	Changes  int    `json:"changes,omitempty"`
}

// Done is what the ship run did with the verdict, from the decision line;
// continue and a file with no line say nothing, and a new word shows as is.
func (j *JudgeOutcome) Done() string {
	switch j.Decision {
	case "", "continue":
		return ""
	case "built":
		if j.Changes > 0 {
			return strconv.Itoa(j.Changes) + " built"
		}

		return "built"
	case "refactor as agreed":
		return "refactor built"
	case "folded":
		return "folded into plan"
	case "block", "stop":
		return "blocked"
	case "asked":
		return "waiting on an answer"
	case "overridden":
		return "overridden by the user"
	}

	return j.Decision
}

// VerdictFile is one verdict file that judges the task, not an idea.
type VerdictFile struct {
	Outcome JudgeOutcome
	Judged  string // task or plan when its subject is one, else change
	At      time.Time
	Path    string
}

// JudgeFiles is the task's verdict files that parse, oldest first.
func JudgeFiles(storageRoot, task string) []VerdictFile {
	var out []VerdictFile

	for _, path := range verdict.Series(storage.Root(storageRoot).Verdicts(task)) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		v, err := verdict.Parse(string(raw))
		if err != nil || v.Idea() {
			continue
		}

		judged, _, _ := strings.Cut(v.Subject, " ")
		if judged != "task" && judged != "plan" {
			judged = "change"
		}

		out = append(out, VerdictFile{
			Outcome: JudgeOutcome{Verdict: v.Verdict, Reason: v.Reason, Decision: v.Decision, Changes: v.Changes},
			Judged:  judged,
			At:      ModTime(path),
			Path:    path,
		})
	}

	return out
}

func ModTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}

	return fi.ModTime()
}
