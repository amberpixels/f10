// Package driveplan proposes how to drive a set of tasks: which to bundle on
// one branch, which to leave alone, and which After: lines to add. It reads
// nothing itself, so every rule is a table test; gathering lives in drive.
package driveplan

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The kinds a task may carry, as fetch settles them.
const (
	KindFeature  = "feature"
	KindBug      = "bug"
	KindRefactor = "refactor"
	KindSpike    = "spike"
)

// A Task is one candidate and what is known about it.
type Task struct {
	ID    string   `json:"id"`
	Title string   `json:"title,omitempty"`
	Kind  string   `json:"kind,omitempty"` // "" reads as feature
	Areas []string `json:"areas,omitempty"`
	Files []string `json:"files,omitempty"`
	Lines int      `json:"lines,omitempty"` // changed lines; 0 is not sized
	// Estimated marks Lines as a model's guess rather than a measured diff
	Estimated bool   `json:"estimated,omitempty"`
	Base      string `json:"after,omitempty"`     // the body's After: line
	BuildsOn  string `json:"buildsOn,omitempty"`  // a task whose code this one builds on, with no After: saying so
	Flow      string `json:"flow,omitempty"`      // the flow its areas map to, "" for the drive's
	Profile   string `json:"profile,omitempty"`   // its :profile as typed
	Risky     string `json:"risky,omitempty"`     // the word that marks it risky (migration), "" for none
	Alone     bool   `json:"alone,omitempty"`     // the user kept it alone
	With      string `json:"with,omitempty"`      // the user bundled it with this task
	After     string `json:"afterEdit,omitempty"` // the user's After: for it, "none" to drop the body's
}

// Input is the candidates in the parent's order, with the caps.
type Input struct {
	Tasks    []Task
	MaxLines int // bundle_max_lines
	Parallel int // the ceiling on --parallel, 0 for none
}

// An Entry is one item of the drive command: a task or a bundle.
type Entry struct {
	IDs     []string `json:"ids"`
	Reason  string   `json:"reason"`
	Base    string   `json:"after,omitempty"` // the one base outside the entry
	Lines   int      `json:"lines,omitempty"`
	Profile string   `json:"profile,omitempty"`
	spike   bool
	named   bool // holds a pair the user bundled, so the rules did not form it
}

// Token is how the drive command names the entry.
func (e Entry) Token() string {
	tok := strings.Join(e.IDs, "+")
	if e.Profile != "" {
		tok += ":" + e.Profile
	}

	return tok
}

// A Stage is the entries at one depth of the After: graph.
type Stage struct {
	Entries []Entry `json:"entries"`
}

// An Edit is an After: line the proposal adds to a task, applied only on a yes.
type Edit struct {
	Task   string `json:"task"`
	After  string `json:"after"` // "none" drops the line
	Reason string `json:"reason"`
}

// Formed is a saving the rules found by themselves.
type Formed struct {
	Bundles int `json:"bundles"`
	Bundled int `json:"bundled"`
	Waits   int `json:"ciWaitsSaved"`
}

// A Plan is the proposal.
type Plan struct {
	Stages   []Stage  `json:"stages"`
	Edits    []Edit   `json:"edits,omitempty"`
	Args     []string `json:"args"` // the drive command's arguments
	Parallel int      `json:"parallel"`
	Waits    int      `json:"ciWaitsSaved"`
	Finishes int      `json:"finishesSaved"`
	Bundles  int      `json:"bundles"`
	Bundled  int      `json:"bundled"` // tasks inside a bundle
	// Formed counts the bundles the rules formed, the user's own left out, so
	// a hint never offers what the user already bundled
	Formed Formed `json:"formed"`
}

// Propose applies the rules: alone first, then bundles within a class by
// shared files or areas, split at the cap, then stages by After: depth.
func Propose(in Input) (Plan, error) {
	if in.MaxLines <= 0 {
		return Plan{}, errors.New("the bundle cap must be a positive number of lines")
	}

	tasks := slices.Clone(in.Tasks)
	byID := map[string]int{}

	for i := range tasks {
		key := strings.ToUpper(tasks[i].ID)
		if _, dup := byID[key]; dup {
			return Plan{}, fmt.Errorf("%s is listed twice", tasks[i].ID)
		}

		byID[key] = i
	}

	var edits []Edit

	base := make([]string, len(tasks))

	for i, t := range tasks {
		base[i] = t.Base

		switch {
		case strings.EqualFold(t.After, "none"):
			base[i] = ""

			if t.Base != "" {
				edits = append(edits, Edit{Task: t.ID, After: "none", Reason: "you dropped its order"})
			}
		case t.After != "":
			base[i] = t.After

			if !strings.EqualFold(t.After, t.Base) {
				edits = append(edits, Edit{Task: t.ID, After: t.After, Reason: "you ordered it"})
			}
		case t.Base == "" && t.BuildsOn != "":
			base[i] = t.BuildsOn
			edits = append(edits, Edit{
				Task:   t.ID,
				After:  t.BuildsOn,
				Reason: "builds on " + t.BuildsOn + "'s code",
			})
		}
	}

	alone := make([]string, len(tasks))
	for i, t := range tasks {
		alone[i] = aloneReason(t, in.MaxLines)
	}

	groups := bundle(tasks, base, alone, byID, in.MaxLines)
	entries := make([]Entry, 0, len(groups))

	groupLines := make([]int, len(tasks))

	for _, g := range groups {
		sum := 0
		for _, i := range g {
			sum += tasks[i].Lines
		}

		for _, i := range g {
			groupLines[i] = sum
		}
	}

	for _, g := range groups {
		entries = append(entries, entryOf(tasks, base, alone, groupLines, g, in.MaxLines))
	}

	edits = append(edits, conflictOrders(tasks, entries, base, byID)...)

	stages, err := stagesOf(entries)
	if err != nil {
		return Plan{}, err
	}

	p := Plan{Stages: stages, Edits: edits}

	for _, st := range stages {
		p.Parallel = max(p.Parallel, len(st.Entries))

		for _, e := range st.Entries {
			p.Args = append(p.Args, e.Token())

			if len(e.IDs) > 1 {
				p.Bundles++
				p.Bundled += len(e.IDs)
				p.Finishes += len(e.IDs) - 1

				if !e.spike {
					p.Waits += len(e.IDs) - 1
				}

				if !e.named {
					p.Formed.Bundles++
					p.Formed.Bundled += len(e.IDs)

					if !e.spike {
						p.Formed.Waits += len(e.IDs) - 1
					}
				}
			}
		}
	}

	if in.Parallel > 0 {
		p.Parallel = min(p.Parallel, in.Parallel)
	}

	if p.Parallel > 1 {
		p.Args = append(p.Args, "--parallel", strconv.Itoa(p.Parallel))
	}

	return p, nil
}

// aloneReason is why a task never bundles, "" when it may.
func aloneReason(t Task, maxLines int) string {
	switch {
	case t.Alone:
		return "alone: you kept it alone"
	case t.With != "":
		return "" // the user's bundle overrules the rules below
	case kindOf(t) == KindSpike:
		return "alone: spike, it merges nothing"
	case t.Risky != "":
		return "alone: " + t.Risky
	case t.Lines <= 0:
		return "alone: not sized"
	case t.Lines > maxLines:
		return fmt.Sprintf("alone: %s, over the cap of %d", linesOf(t.Lines, t.Estimated), maxLines)
	}

	return ""
}

func kindOf(t Task) string {
	if t.Kind == "" {
		return KindFeature
	}

	return strings.ToLower(t.Kind)
}

// class is what two tasks must share to bundle: a flow, an agent profile,
// and a kind class, since a bug fix keeps a PR apart from features.
func class(t Task) string {
	kind := "change"
	if kindOf(t) == KindBug {
		kind = KindBug
	}

	return strings.ToLower(t.Flow) + "|" + t.Profile + "|" + kind
}

// shared is the files, else the areas, two tasks have in common.
func shared(a, b Task) (string, []string) {
	if f := common(a.Files, b.Files); len(f) > 0 {
		return "files", f
	}

	if ar := common(a.Areas, b.Areas); len(ar) > 0 {
		return "area", ar
	}

	return "", nil
}

func common(a, b []string) []string {
	var out []string

	for _, x := range a {
		if slices.ContainsFunc(b, func(y string) bool { return strings.EqualFold(x, y) }) &&
			!slices.ContainsFunc(out, func(y string) bool { return strings.EqualFold(x, y) }) {
			out = append(out, x)
		}
	}

	return out
}

// bundle joins tasks of one class that share a file or an area, then cuts
// each joined set into bundles with one outside base each, under the cap.
// Groups come back in the parent's order of their first member.
func bundle(tasks []Task, base, alone []string, byID map[string]int, maxLines int) [][]int {
	parent := make([]int, len(tasks))
	for i := range parent {
		parent[i] = i
	}

	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}

		return i
	}

	union := func(a, b int) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[max(ra, rb)] = min(ra, rb)
		}
	}

	for i := range tasks {
		if alone[i] != "" {
			continue
		}

		if tasks[i].With != "" {
			if w, ok := byID[strings.ToUpper(tasks[i].With)]; ok && !tasks[w].Alone {
				union(i, w)
			}

			continue
		}

		for j := i + 1; j < len(tasks); j++ {
			if alone[j] != "" || tasks[j].With != "" || class(tasks[i]) != class(tasks[j]) {
				continue
			}

			if why, _ := shared(tasks[i], tasks[j]); why != "" {
				union(i, j)
			}
		}
	}

	sets := map[int][]int{}

	var roots []int

	for i := range tasks {
		r := find(i)
		if _, seen := sets[r]; !seen {
			roots = append(roots, r)
		}

		sets[r] = append(sets[r], i)
	}

	var groups [][]int

	for _, r := range roots {
		groups = append(groups, split(tasks, base, sets[r], byID, maxLines)...)
	}

	slices.SortStableFunc(groups, func(a, b []int) int { return a[0] - b[0] })

	return groups
}

// split cuts one joined set: a member joins the first bundle it shares its
// base with (none, or a member already in it) and fits under the cap. Only
// a pair the user named passes the cap; a task the rules joined never does.
func split(tasks []Task, base []string, members []int, byID map[string]int, maxLines int) [][]int {
	if len(members) == 1 {
		return [][]int{members}
	}

	type part struct {
		ids   []int
		ext   string
		lines int
	}

	var parts []part

	inPart := func(p part, id string) bool {
		j, ok := byID[strings.ToUpper(id)]

		return ok && slices.Contains(p.ids, j)
	}

	named := func(p part, i int) bool {
		return tasks[i].With != "" && inPart(p, tasks[i].With) ||
			slices.ContainsFunc(p.ids, func(j int) bool { return strings.EqualFold(tasks[j].With, tasks[i].ID) })
	}

	for _, i := range members {
		placed := false

		for k := range parts {
			p := &parts[k]

			b := base[i]
			fits := named(*p, i) || p.lines+tasks[i].Lines <= maxLines

			switch {
			case !fits:
				continue
			case b != "" && inPart(*p, b):
			case strings.EqualFold(p.ext, b):
			default:
				continue
			}

			p.ids = append(p.ids, i)
			p.lines += tasks[i].Lines
			placed = true

			break
		}

		if !placed {
			parts = append(parts, part{ids: []int{i}, ext: base[i], lines: tasks[i].Lines})
		}
	}

	out := make([][]int, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.ids)
	}

	return out
}

// entryOf names a group's reason: why it is bundled, or why it stands alone.
func entryOf(tasks []Task, base, alone []string, groupLines, g []int, maxLines int) Entry {
	e := Entry{Profile: tasks[g[0]].Profile}
	estimated := false

	for _, i := range g {
		e.IDs = append(e.IDs, tasks[i].ID)
		e.Lines += tasks[i].Lines
		estimated = estimated || tasks[i].Estimated
		e.spike = e.spike || kindOf(tasks[i]) == KindSpike
		e.named = e.named || tasks[i].With != ""
	}

	for _, i := range g {
		if b := base[i]; b != "" &&
			!slices.ContainsFunc(e.IDs, func(id string) bool { return strings.EqualFold(id, b) }) {
			e.Base = b

			break
		}
	}

	if len(g) == 1 {
		i := g[0]

		e.Reason = alone[i]
		if e.Reason == "" {
			e.Reason = loneReason(tasks, base, alone, groupLines, i, maxLines)
		}

		return e
	}

	if e.named {
		e.Reason = "bundle: you bundled them"
	} else {
		e.Reason = "bundle: " + sharedReason(tasks, g)
	}

	e.Reason += ", " + linesOf(e.Lines, estimated) + " together"

	if e.Base != "" {
		e.Reason += ", After: " + e.Base
	}

	return e
}

func sharedReason(tasks []Task, g []int) string {
	var files, areas []string

	for x, i := range g {
		for _, j := range g[x+1:] {
			why, what := shared(tasks[i], tasks[j])

			switch why {
			case "files":
				files = append(files, what...)
			case "area":
				areas = append(areas, what...)
			}
		}
	}

	files, areas = dedupe(files), dedupe(areas)

	switch {
	case len(files) > 0:
		return "same files (" + strings.Join(files, ", ") + ")"
	case len(areas) > 0:
		return "same area (" + strings.Join(areas, ", ") + ")"
	}

	return "bundled"
}

func dedupe(list []string) []string {
	var out []string

	for _, s := range list {
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, s) }) {
			out = append(out, s)
		}
	}

	return out
}

// loneReason is why a task that may bundle stands alone: the task beside it
// it could have joined, else what it touches.
func loneReason(tasks []Task, base, alone []string, groupLines []int, i, maxLines int) string {
	t := tasks[i]

	for j, o := range tasks {
		if j == i || alone[j] != "" {
			continue
		}

		if why, _ := shared(t, o); why == "" {
			continue
		}

		switch {
		case !strings.EqualFold(t.Flow, o.Flow):
			return "alone: other flow than " + o.ID
		case t.Profile != o.Profile:
			return "alone: other agent profile than " + o.ID
		case (kindOf(t) == KindBug) != (kindOf(o) == KindBug):
			return "alone: a bug fix keeps its own PR"
		case !strings.EqualFold(base[i], base[j]):
			return "alone: its After: differs from " + o.ID + "'s"
		default:
			return fmt.Sprintf("split: %d lines with %s's group, over the cap of %d",
				t.Lines+groupLines[j], o.ID, maxLines)
		}
	}

	if len(t.Areas) == 0 {
		return "alone: nothing shares its area"
	}

	return "alone: " + strings.Join(t.Areas, ", ")
}

func linesOf(n int, estimated bool) string {
	if estimated {
		return fmt.Sprintf("~%d lines", n)
	}

	return fmt.Sprintf("%d lines", n)
}

// conflictOrders orders two entries that share a file but cannot bundle,
// the later after the earlier, since run side by side they would conflict.
// Only an entry with no base of its own takes one.
func conflictOrders(tasks []Task, entries []Entry, base []string, byID map[string]int) []Edit {
	var edits []Edit

	for b := range entries {
		// a base, or an order the user set or dropped, is not second-guessed
		if entries[b].Base != "" || slices.ContainsFunc(entries[b].IDs, func(id string) bool {
			i := byID[strings.ToUpper(id)]

			return base[i] != "" || tasks[i].After != ""
		}) {
			continue
		}

		for a := range b {
			file := sharedFile(tasks, entries[a], entries[b], byID)
			if file == "" || dependsOn(entries, a, b) {
				continue
			}

			lead := entries[a].IDs[0]
			entries[b].Base = lead
			edits = append(edits, Edit{
				Task:   entries[b].IDs[0],
				After:  lead,
				Reason: "shares " + file + " with " + lead + ": run side by side they would conflict",
			})

			break
		}
	}

	return edits
}

func sharedFile(tasks []Task, a, b Entry, byID map[string]int) string {
	for _, x := range a.IDs {
		for _, y := range b.IDs {
			if f := common(tasks[byID[strings.ToUpper(x)]].Files, tasks[byID[strings.ToUpper(y)]].Files); len(f) > 0 {
				return f[0]
			}
		}
	}

	return ""
}

// dependsOn says entry a already waits, directly or not, for entry b.
func dependsOn(entries []Entry, a, b int) bool {
	of := entryIndex(entries)

	for seen := 0; a >= 0 && seen <= len(entries); seen++ {
		if a == b {
			return true
		}

		next, ok := of[strings.ToUpper(entries[a].Base)]
		if !ok {
			return false
		}

		a = next
	}

	return false
}

func entryIndex(entries []Entry) map[string]int {
	of := map[string]int{}

	for k, e := range entries {
		for _, id := range e.IDs {
			of[strings.ToUpper(id)] = k
		}
	}

	return of
}

// stagesOf puts each entry at its depth in the After: graph; a base outside
// the list counts as done. Inside a stage, entries keep the parent's order.
func stagesOf(entries []Entry) ([]Stage, error) {
	of := entryIndex(entries)
	depth := make([]int, len(entries))
	state := make([]int, len(entries)) // 0 new, 1 on the path, 2 done

	var visit func(k int, path []string) error

	visit = func(k int, path []string) error {
		switch state[k] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf(
				"the dependencies run in a circle: %s",
				strings.Join(append(path, entries[k].Token()), " after "),
			)
		}

		state[k] = 1

		if b, ok := of[strings.ToUpper(entries[k].Base)]; ok && entries[k].Base != "" {
			if err := visit(b, append(path, entries[k].Token())); err != nil {
				return err
			}

			depth[k] = depth[b] + 1
		}

		state[k] = 2

		return nil
	}

	for k := range entries {
		if err := visit(k, nil); err != nil {
			return nil, err
		}
	}

	var stages []Stage

	for k, e := range entries {
		for len(stages) <= depth[k] {
			stages = append(stages, Stage{})
		}

		stages[depth[k]].Entries = append(stages[depth[k]].Entries, e)
	}

	return stages, nil
}
