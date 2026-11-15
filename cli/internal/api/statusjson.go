package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// runKeys are the keys of a run's own state in status --json, which
// RunStatus carries in its run message; the rest sit beside it.
var runKeys = []string{
	"session", "task", "url", "root", "skill", "capture", "plan", "ship", "leaf", "at", "final", "pipeline",
	"labels", "note", "next", "ask", "wait", "stop", "done", "done_at", "updated",
}

// runStatuses reads what f10 status --json printed into RunStatus messages,
// so the reply carries everything the command's JSON does. It prints three
// shapes: live runs, a task with none (its fallback), and task rows.
func runStatuses(raw []byte) ([]*f10v1.RunStatus, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil
	}

	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("reading status: %w", err)
	}

	var objs []map[string]any

	switch v := top.(type) {
	case []any:
		for _, o := range v {
			if m, ok := o.(map[string]any); ok {
				objs = append(objs, m)
			}
		}
	case map[string]any:
		objs = append(objs, v)
	}

	var out []*f10v1.RunStatus

	for _, o := range objs {
		for _, shaped := range shapeStatus(o) {
			rs, err := decodeRunStatus(shaped)
			if err != nil {
				return nil, err
			}

			out = append(out, rs)
		}
	}

	return out, nil
}

func shapeStatus(o map[string]any) []map[string]any {
	switch {
	case o["session"] != nil:
		ship, _ := o["shipment"].(map[string]any)

		return []map[string]any{{"run": runOf(o), "agent": o["agent"], "gates": o["gates"], "shipment": ship}}
	case o["others"] != nil:
		ship, _ := o["shipment"].(map[string]any)
		if ship == nil {
			ship = map[string]any{"task": o["task"], "plan": o["plan"], "pr": o["pr"], "pr_state": o["pr_state"]}
		}

		out := []map[string]any{{"shipment": ship}}

		others, _ := o["others"].([]any)
		for _, r := range others {
			if m, ok := r.(map[string]any); ok {
				out = append(out, map[string]any{"run": runOf(m)})
			}
		}

		return out
	}

	ship := map[string]any{
		"task": o["id"], "title": o["title"], "url": o["url"], "pr": o["pr"], "pr_state": o["pr_state"],
	}

	return []map[string]any{{"shipment": ship, "gates": o["gates"]}}
}

func runOf(o map[string]any) map[string]any {
	run := map[string]any{}

	for _, k := range runKeys {
		if v, ok := o[k]; ok && v != nil {
			run[k] = v
		}
	}

	for _, k := range []string{"capture", "plan", "ship"} {
		if s, ok := run[k].(string); ok {
			run[k] = phaseOf(s)
		}
	}

	return run
}

// phaseOf is a phase's status word as the PhaseStatus enum names it; a word
// the enum lacks reads as unspecified rather than failing the reply.
func phaseOf(s string) string {
	name := "PHASE_STATUS_" + strings.ToUpper(s)
	if _, ok := f10v1.PhaseStatus_value[name]; !ok {
		return f10v1.PhaseStatus_PHASE_STATUS_UNSPECIFIED.String()
	}

	return name
}

func decodeRunStatus(shaped map[string]any) (*f10v1.RunStatus, error) {
	if ship, ok := shaped["shipment"].(map[string]any); ok {
		ship["verdicts"] = fileRefs(ship["verdicts"], "subject")
		ship["reviews"] = fileRefs(ship["reviews"], "round")
	}

	for k, v := range shaped {
		if v == nil {
			delete(shaped, k)
		}
	}

	raw, err := json.Marshal(shaped)
	if err != nil {
		return nil, err
	}

	var rs f10v1.RunStatus
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("reading status: %w", err)
	}

	return &rs, nil
}

// fileRefs is a list of task files as FileRef carries them: the key that
// names each (a verdict's subject, a findings file's round) becomes its name.
func fileRefs(v any, nameKey string) any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}

	out := make([]any, 0, len(list))

	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, map[string]any{"name": m[nameKey], "path": m["path"]})
		}
	}

	return out
}
