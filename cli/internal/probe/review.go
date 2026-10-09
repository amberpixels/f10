package probe

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Review is the remote review a CI workflow declares: a workflow under
// .github/workflows that runs the Claude Code action, read for who posts
// the review and what fires it. Everything else about the review (where it
// arrives, what marks it handled) is the action's out-of-the-box behavior
// and is written by facts.Scaffold as declared defaults, so a project whose
// workflow does otherwise edits one line.
type Review struct {
	Reviewer string `json:"reviewer"`          // the login that posts: claude[bot], or github-actions[bot] with a github_token
	Trigger  string `json:"trigger"`           // "automatic", "manual", or "automatic, manual"
	Mention  string `json:"mention,omitempty"` // the comment that triggers a manual run, "@claude" by default
	Workflow string `json:"workflow"`          // the workflow file, relative to the checkout root: the evidence
}

// The action that reviews, in any version (`@v1`, `@beta`, a sha).
const claudeAction = "anthropics/claude-code-action"

// Event names a workflow's `on:` block may carry, split by what they mean
// for a review: a pull_request event fires on its own, a comment event
// waits for someone to write the mention.
var (
	automaticEvents = []string{"pull_request", "pull_request_target"}
	manualEvents    = []string{
		"issue_comment",
		"pull_request_review_comment",
		"pull_request_review",
		"workflow_dispatch",
	}

	// contains(github.event.comment.body, '@claude') - the expression the
	// action's own templates gate a manual run on
	mentionRE = regexp.MustCompile(`contains\(\s*github\.event\.(?:comment|review|issue)\.body\s*,\s*['"](@[\w-]+)['"]`)
)

// reviewWorkflow finds the workflow using the Claude Code action, or nil.
// The stock setup installs two (a pull_request review and an `@claude`
// answerer), so the first that fires on its own wins whatever the file names,
// and the first of any trigger stands only when none does. Files are read in
// name order; one yaml cannot parse is skipped, a broken workflow being CI's
// problem to report.
func reviewWorkflow(root string) *Review {
	dir := filepath.Join(root, ".github", "workflows")

	var files []string

	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		files = append(files, matches...)
	}

	sort.Strings(files)

	var first *Review

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		rv := parseWorkflow(data)
		if rv == nil {
			continue
		}

		rel, err := filepath.Rel(root, file)
		if err != nil {
			rel = file
		}

		rv.Workflow = rel

		if strings.Contains(rv.Trigger, "automatic") {
			return rv
		}

		if first == nil {
			first = rv
		}
	}

	return first
}

// parseWorkflow reads one workflow. nil when no step uses the action.
func parseWorkflow(data []byte) *Review {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}

	step := actionStep(doc)
	if step == nil {
		return nil
	}

	rv := &Review{Reviewer: "claude[bot]"}

	// the action posts as the GitHub App unless the workflow hands it the
	// runner's own token, which posts as github-actions[bot]
	if with, ok := step["with"].(map[string]any); ok {
		if _, ok := with["github_token"]; ok {
			rv.Reviewer = "github-actions[bot]"
		}
	}

	events := onEvents(doc)

	automatic := hasAny(events, automaticEvents)
	manual := hasAny(events, manualEvents)

	switch {
	case automatic && manual:
		rv.Trigger = "automatic, manual"
	case automatic:
		rv.Trigger = "automatic"
	case manual:
		rv.Trigger = "manual"
	default:
		// a workflow with no trigger detection can name is still a review
		// workflow; which event fires it is left for the human to write
		rv.Trigger = "manual"
	}

	if manual || !automatic {
		rv.Mention = "@claude"
		if m := mentionRE.FindSubmatch(data); m != nil {
			rv.Mention = string(m[1])
		}
	}

	return rv
}

// actionStep is the first step in any job whose `uses:` names the action.
func actionStep(doc map[string]any) map[string]any {
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		return nil
	}

	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		job, ok := jobs[name].(map[string]any)
		if !ok {
			continue
		}

		steps, ok := job["steps"].([]any)
		if !ok {
			continue
		}

		for _, s := range steps {
			step, ok := s.(map[string]any)
			if !ok {
				continue
			}

			if uses, ok := step["uses"].(string); ok && strings.HasPrefix(uses, claudeAction) {
				return step
			}
		}
	}

	return nil
}

// onEvents lists the events under `on:`, whichever of its three shapes the
// file used: a bare string, a list, or a mapping keyed by event. yaml.v3
// decodes the key `on` as the string it is, not as a YAML 1.1 boolean.
func onEvents(doc map[string]any) []string {
	on, ok := doc["on"]
	if !ok {
		return nil
	}

	switch v := on.(type) {
	case string:
		return []string{v}
	case []any:
		events := make([]string, 0, len(v))

		for _, e := range v {
			if s, ok := e.(string); ok {
				events = append(events, s)
			}
		}

		return events
	case map[string]any:
		events := make([]string, 0, len(v))
		for e := range v {
			events = append(events, e)
		}

		sort.Strings(events)

		return events
	}

	return nil
}

func hasAny(events, wanted []string) bool {
	return slices.ContainsFunc(events, func(e string) bool { return slices.Contains(wanted, e) })
}
