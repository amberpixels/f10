package drive

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/driveplan"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

// PlanInput is what drive plan reads beside its arguments. It writes
// nothing: no record, no lease, no tracker change.
type PlanInput struct {
	Storage         storage.Root
	Tracker         stack.Tracker
	FlowByArea      map[string]string
	MaxLines        int
	DefaultParallel int
}

// PlanResult is a proposal.
type PlanResult struct {
	Plan  driveplan.Plan
	Tasks []driveplan.Task
	Notes []string // tasks left out, and why
	Extra []string // the drive flags carried onto the command
}

// A SheetTask is one task's row in the facts sheet the drive skill writes:
// what only a read of the task against the code can tell, and the user's
// edits to the proposal.
type SheetTask struct {
	Kind     string   `json:"kind,omitempty"`
	Areas    []string `json:"areas,omitempty"`
	Files    []string `json:"files,omitempty"`
	Lines    int      `json:"lines,omitempty"`
	BuildsOn string   `json:"buildsOn,omitempty"`
	Risky    string   `json:"risky,omitempty"`
	Alone    bool     `json:"alone,omitempty"`
	With     string   `json:"with,omitempty"`
	After    string   `json:"after,omitempty"`
}

// Labels that name a kind, as fetch reads them.
var kindLabels = map[string]string{
	"bug": driveplan.KindBug, "defect": driveplan.KindBug, "regression": driveplan.KindBug,
	"refactor": driveplan.KindRefactor, "tech-debt": driveplan.KindRefactor,
	"spike": driveplan.KindSpike, "research": driveplan.KindSpike, "investigation": driveplan.KindSpike,
}

// riskyLabels mark a task that never bundles.
var riskyLabels = []string{"migration", "risky"}

// Plan proposes how to drive the arguments' tasks.
func Plan(ctx context.Context, in PlanInput, a drivectl.DriveArgs) (PlanResult, error) {
	pa := a.Plan
	if pa == nil {
		return PlanResult{}, errors.New("drive plan: no plan arguments")
	}

	sheet, err := readSheet(pa.Facts)
	if err != nil {
		return PlanResult{}, err
	}

	tasks, notes, err := in.candidates(ctx, a)
	if err != nil {
		return PlanResult{}, err
	}

	for i := range tasks {
		in.lay(&tasks[i], a.Flow != "", sheet)
	}

	p, err := driveplan.Propose(driveplan.Input{
		Tasks:    tasks,
		MaxLines: in.MaxLines,
		Parallel: cmp.Or(a.Parallel, in.DefaultParallel),
	})
	if err != nil {
		return PlanResult{}, err
	}

	return PlanResult{Plan: p, Tasks: tasks, Notes: notes, Extra: pa.Pass}, nil
}

// readSheet reads the facts sheet, keyed by upper-cased task id; an unknown
// field is refused, so a typo never reads as "not sized".
func readSheet(path string) (map[string]SheetTask, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the facts sheet: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw map[string]SheetTask
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("the facts sheet %s: %w", path, err)
	}

	out := make(map[string]SheetTask, len(raw))
	for id, row := range raw {
		out[strings.ToUpper(strings.TrimSpace(id))] = row
	}

	return out, nil
}

// candidates expands each argument: a parent into its open subtasks, drafts
// left out, a bundle into members bundled with its lead, any other id as
// itself. Closed tasks are left out.
func (in PlanInput) candidates(ctx context.Context, a drivectl.DriveArgs) ([]driveplan.Task, []string, error) {
	var (
		out   []driveplan.Task
		notes []string
	)

	for i, token := range a.Tasks {
		profile := ""
		if i < len(a.Profiles) {
			profile = a.Profiles[i]
		}

		r, err := in.Tracker.Resolve(ctx, token)
		if err != nil {
			return nil, nil, err
		}

		if len(r.Riders()) > 0 {
			members, err := in.members(ctx, r, profile)
			if err != nil {
				return nil, nil, err
			}

			out = append(out, members...)

			continue
		}

		rows, err := in.children(ctx, r)
		if err != nil {
			return nil, nil, err
		}

		if len(rows) == 0 {
			t, closed, err := in.single(ctx, r, profile)
			if err != nil {
				return nil, nil, err
			}

			if closed {
				notes = append(notes, "left out: "+r.ID+" is closed")

				continue
			}

			out = append(out, t)

			continue
		}

		open := 0

		for _, row := range rows {
			if row.Finished {
				continue
			}

			if slices.ContainsFunc(row.Labels, func(l string) bool { return strings.EqualFold(l, "draft") }) {
				notes = append(notes, "left out: "+row.ID+" is a draft; name it to plan it")

				continue
			}

			open++

			out = append(out, driveplan.Task{
				ID: row.ID, Title: row.Title, Base: row.After, Profile: profile,
				Kind: labelKind(row.Labels), Risky: labelRisky(row.Labels),
			})
		}

		if open == 0 {
			notes = append(notes, "left out: "+r.ID+" has no open subtask to drive")
		}
	}

	return out, notes, nil
}

// children is the task's subtasks, none when the tracker keeps no parent
// link, so the id stays a task of its own.
func (in PlanInput) children(ctx context.Context, r ref.Ref) ([]stack.ChildRow, error) {
	if in.Tracker.Children == nil {
		return nil, nil
	}

	rows, err := in.Tracker.Children(ctx, r)
	if errors.Is(err, tasksource.ErrNoParentLink) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%s: reading its subtasks: %w", r.ID, err)
	}

	return rows, nil
}

func (in PlanInput) single(ctx context.Context, r ref.Ref, profile string) (driveplan.Task, bool, error) {
	info, err := in.Tracker.Info(ctx, r)
	if err != nil {
		return driveplan.Task{}, false, fmt.Errorf("reading %s: %w", r.ID, err)
	}

	return driveplan.Task{
		ID: r.ID, Base: hostops.AfterIn(info.Body), Profile: profile,
		Kind: labelKind(info.Labels), Risky: labelRisky(info.Labels),
	}, info.Closed, nil
}

// members is a typed bundle: the user already chose to bundle it, so each
// rider is bundled with the lead.
func (in PlanInput) members(ctx context.Context, r ref.Ref, profile string) ([]driveplan.Task, error) {
	var out []driveplan.Task

	refs := r.MemberRefs()

	for _, m := range refs {
		t, _, err := in.single(ctx, m, profile)
		if err != nil {
			return nil, err
		}

		if m.ID != refs[0].ID {
			t.With = refs[0].ID
		}

		out = append(out, t)
	}

	return out, nil
}

// lay fills what the tracker did not say: a saved plan's Kind and Areas
// lines, then the facts sheet. A label's kind wins over the plan's, and the
// plan's over the sheet's, as fetch settles it.
func (in PlanInput) lay(t *driveplan.Task, flowGiven bool, sheet map[string]SheetTask) {
	planKind, planAreas, planSmall := planHeader(in.Storage, t.ID)
	row := sheet[strings.ToUpper(t.ID)]

	t.Kind = cmp.Or(t.Kind, planKind, strings.ToLower(row.Kind))
	t.Risky = cmp.Or(t.Risky, row.Risky)
	t.Files = row.Files
	t.BuildsOn = row.BuildsOn
	t.Alone, t.With, t.After = row.Alone, cmp.Or(row.With, t.With), row.After

	t.Areas = planAreas
	if len(t.Areas) == 0 {
		t.Areas = row.Areas
	}

	switch {
	case row.Lines > 0:
		t.Lines, t.Estimated = row.Lines, true
	case planSmall:
		// a small plan is one change in one area, so without a read of the
		// code it counts as a quarter of the cap
		t.Lines, t.Estimated = max(in.MaxLines/smallShare, 1), true
	}

	if !flowGiven {
		t.Flow = flowOf(in.FlowByArea, t.Areas)
	}
}

// flowOf is the flow the first mapped area names, default when none maps.
func flowOf(byArea map[string]string, areas []string) string {
	for _, area := range areas {
		for key, flow := range byArea {
			if strings.EqualFold(key, area) {
				return flow
			}
		}
	}

	return "default"
}

// smallShare is the part of the cap a small plan counts as.
const smallShare = 4

// planHeader reads a saved plan's Kind:, Areas: and Size: header lines. No
// Kind: line comes back empty, so a label or the facts sheet can still
// settle it.
func planHeader(root storage.Root, id string) (string, []string, bool) {
	if !root.PlanLive(id) {
		return "", nil, false
	}

	data, err := os.ReadFile(root.Plan(id))
	if err != nil {
		return "", nil, false
	}

	var (
		kind  string
		areas []string
		small bool
	)

	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			break // the header lines come before the first section
		}

		if v, ok := strings.CutPrefix(line, "Kind:"); ok {
			kind = strings.ToLower(strings.TrimSpace(v))
		}

		if v, ok := strings.CutPrefix(line, "Size:"); ok {
			small = strings.HasPrefix(strings.TrimSpace(v), "small")
		}

		if v, ok := strings.CutPrefix(line, "Areas:"); ok {
			for area := range strings.SplitSeq(v, ",") {
				if area = strings.TrimSpace(area); area != "" {
					areas = append(areas, area)
				}
			}
		}
	}

	return kind, areas, small
}

func labelKind(labels []string) string {
	for _, l := range labels {
		if kind, ok := kindLabels[strings.ToLower(l)]; ok {
			return kind
		}
	}

	return ""
}

func labelRisky(labels []string) string {
	for _, l := range labels {
		if slices.Contains(riskyLabels, strings.ToLower(l)) {
			return strings.ToLower(l)
		}
	}

	return ""
}
