// Package driveplan proposes how to start a set of tasks. It says which to
// bundle on one branch and which to leave alone, and spells which entry
// waits for which in the start command. It reads nothing itself, so every
// rule is a table test. Gathering lives in drive.
package driveplan

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/notation"
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
	Estimated bool     `json:"estimated,omitempty"`
	Base      string   `json:"after,omitempty"`     // the body's After: line
	BuildsOn  []string `json:"buildsOn,omitempty"`  // tasks whose code this one builds on, with no After: saying so
	Flow      string   `json:"flow,omitempty"`      // the flow its areas map to, "" for the batch's
	Profile   string   `json:"profile,omitempty"`   // its :profile as typed
	Risky     string   `json:"risky,omitempty"`     // the word that marks it risky (migration), "" for none
	Alone     bool     `json:"alone,omitempty"`     // the user kept it alone
	With      string   `json:"with,omitempty"`      // the user bundled it with this task
	After     []string `json:"afterEdit,omitempty"` // the user's bases for it, ["none"] to drop every base
	// Suffix is its -suffix as typed, one attempt of the task, which never bundles
	Suffix string `json:"suffix,omitempty"`
}

// NoOrder is the user's After: that drops every base of a task.
const NoOrder = "none"

// Input is the candidates in the parent's order, with the caps.
type Input struct {
	Tasks    []Task
	MaxLines int // bundle_max_lines
	Parallel int // the ceiling on --parallel, 0 for none
	// TypedParallel says the user typed that ceiling, so the command prints
	// it even at 1, where a default ceiling of 1 prints nothing
	TypedParallel bool
	// Carry is the start flags the user typed, put on the command as typed
	Carry []string
	// Joins says the run's flow finishes, so an entry may wait on several
	// bases. f10 start refuses a join otherwise, so only the first is kept.
	Joins bool
	// Finished is the bases outside the list that are finished, the only
	// outside bases f10 start lets a join wait on
	Finished []string
}

// An Entry is one item of the start command: a task or a bundle.
type Entry struct {
	IDs     []string `json:"ids"`
	Reason  string   `json:"reason"`
	Bases   []Base   `json:"bases,omitempty"` // the tasks outside the entry it waits for
	Lines   int      `json:"lines,omitempty"`
	Profile string   `json:"profile,omitempty"`
	Suffix  string   `json:"suffix,omitempty"` // a lone task's -suffix as typed
	spike   bool
	named   bool // holds a pair the user bundled, so the rules did not form it
}

// A Base is one task an entry waits for, and why.
type Base struct {
	ID      string `json:"id"`
	Reason  string `json:"reason"`
	Dropped bool   `json:"dropped,omitempty"` // the flow does not finish, so the join was cut to one base
}

// Token is how the proposal's rows name the entry. The command adds its bases.
func (e Entry) Token() string {
	return notation.Format(notation.Entry{Task: e.task(), Profile: e.Profile})
}

// task is the entry's tasks as the command line spells them.
func (e Entry) task() string {
	return notation.JoinSuffix(strings.Join(e.IDs, notation.Bundle), e.Suffix)
}

// waits is the ids of the bases the entry keeps.
func (e Entry) waits() []string {
	var out []string

	for _, b := range e.Bases {
		if !b.Dropped {
			out = append(out, b.ID)
		}
	}

	return out
}

// A Level is the entries at one depth of the base graph: every base of an
// entry sits in an earlier level.
type Level struct {
	Entries []Entry `json:"entries"`
}

// Formed is a saving the rules found by themselves.
type Formed struct {
	Bundles int `json:"bundles"`
	Bundled int `json:"bundled"`
	Waits   int `json:"ciWaitsSaved"`
}

// A Plan is the proposal.
type Plan struct {
	Levels []Level  `json:"levels"`
	Args   []string `json:"args"` // the start command's arguments, the order spelled with , and @
	// Carry is the typed start flags the command ends in. Command is the one
	// place the line is built, for the text, the JSON and the hint.
	Carry []string `json:"carried,omitempty"`
	// Unordered is a line per task whose order the user dropped. Its body
	// still names a base, which the start would read back.
	Unordered []string `json:"unordered,omitempty"`
	Parallel  int      `json:"parallel"`
	Waits     int      `json:"ciWaitsSaved"`
	Finishes  int      `json:"finishesSaved"`
	Bundles   int      `json:"bundles"`
	Bundled   int      `json:"bundled"` // tasks inside a bundle
	// Formed counts the bundles the rules formed, the user's own left out, so
	// a hint never offers what the user already bundled
	Formed Formed `json:"formed"`
}

// Propose applies the rules in order. Alone comes first, then bundles within
// a class by shared files or areas, split at the cap, then bases and levels.
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

	base := make([][]Base, len(tasks))
	for i, t := range tasks {
		base[i] = basesOf(t)
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

	conflictOrders(tasks, entries, byID)

	for k := range entries {
		collapse(entries, k)
		cutJoin(&entries[k], in, byID)
	}

	levels, err := levelsOf(entries)
	if err != nil {
		return Plan{}, err
	}

	p := Plan{Levels: levels, Args: commandArgs(levels), Carry: in.Carry, Unordered: unordered(tasks, levels, byID)}

	for _, st := range levels {
		p.Parallel = max(p.Parallel, len(st.Entries))

		for _, e := range st.Entries {
			if len(e.IDs) < 2 {
				continue
			}

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

	if in.Parallel > 0 {
		p.Parallel = min(p.Parallel, in.Parallel)
	}

	// a cap of 1 is the start's own default only when nobody typed it
	if p.Parallel > 1 || in.TypedParallel && in.Parallel > 0 {
		p.Args = append(p.Args, "--parallel", strconv.Itoa(p.Parallel))
	}

	return p, nil
}

// basesOf is a task's bases: the user's, else its After: line and the tasks
// it builds on. An id named twice keeps its first reason.
func basesOf(t Task) []Base {
	if len(t.After) > 0 {
		if dropsOrder(t) {
			return nil
		}

		var out []Base
		for _, id := range t.After {
			out = addBase(out, Base{ID: id, Reason: "you ordered it"})
		}

		return out
	}

	var out []Base
	if t.Base != "" {
		out = addBase(out, Base{ID: t.Base, Reason: "its After: line"})
	}

	for _, id := range t.BuildsOn {
		out = addBase(out, Base{ID: id, Reason: "builds on " + id + "'s code"})
	}

	return out
}

func dropsOrder(t Task) bool {
	return len(t.After) == 1 && strings.EqualFold(t.After[0], NoOrder)
}

func addBase(list []Base, b Base) []Base {
	if b.ID == "" || slices.ContainsFunc(list, func(o Base) bool { return strings.EqualFold(o.ID, b.ID) }) {
		return list
	}

	return append(list, b)
}

func baseIDs(list []Base) []string {
	out := make([]string, 0, len(list))
	for _, b := range list {
		out = append(out, b.ID)
	}

	return out
}

// sameSet compares two id lists, ignoring order and case.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for _, x := range a {
		if !slices.ContainsFunc(b, func(y string) bool { return strings.EqualFold(x, y) }) {
			return false
		}
	}

	return true
}

// aloneReason is why a task never bundles, "" when it may.
func aloneReason(t Task, maxLines int) string {
	switch {
	case t.Suffix != "":
		return "alone: its own attempt, -" + t.Suffix
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
// each joined set into bundles with one set of outside bases each, under
// the cap.
// Groups come back in the parent's order of their first member.
func bundle(tasks []Task, base [][]Base, alone []string, byID map[string]int, maxLines int) [][]int {
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

// split cuts one joined set. A member joins the first bundle that fits it
// under the cap and has the same outside bases, or holds every base it has.
// A member's outside bases are read against the whole set and through the
// members it builds on, so the cut does not depend on the order the members
// were listed in. A pair the user named always joins, and only such a pair
// passes the cap. A task the rules joined never does.
func split(tasks []Task, base [][]Base, members []int, byID map[string]int, maxLines int) [][]int {
	if len(members) == 1 {
		return [][]int{members}
	}

	type part struct {
		ids   []int
		ext   []string
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

	// outside is the bases of i outside the set, its own and those of the
	// members it builds on; seen stops a circle, which levelsOf reports
	var outside func(i int, seen map[int]bool) []string

	outside = func(i int, seen map[int]bool) []string {
		if seen[i] {
			return nil
		}

		seen[i] = true

		var out []string

		for _, b := range base[i] {
			j, ok := byID[strings.ToUpper(b.ID)]
			if !ok || !slices.Contains(members, j) {
				if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, b.ID) }) {
					out = append(out, b.ID)
				}

				continue
			}

			for _, id := range outside(j, seen) {
				if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, id) }) {
					out = append(out, id)
				}
			}
		}

		return out
	}

	for _, i := range members {
		placed := false
		ext := outside(i, map[int]bool{})

		for k := range parts {
			p := &parts[k]

			holdsAll := len(base[i]) > 0 &&
				!slices.ContainsFunc(base[i], func(b Base) bool { return !inPart(*p, b.ID) })

			switch {
			case named(*p, i):
			case p.lines+tasks[i].Lines > maxLines:
				continue
			case holdsAll, sameSet(p.ext, ext):
			default:
				continue
			}

			p.ids = append(p.ids, i)
			p.lines += tasks[i].Lines
			placed = true

			break
		}

		if !placed {
			parts = append(parts, part{ids: []int{i}, ext: ext, lines: tasks[i].Lines})
		}
	}

	out := make([][]int, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.ids)
	}

	return out
}

// entryOf names a group's reason: why it is bundled, or why it stands alone.
func entryOf(tasks []Task, base [][]Base, alone []string, groupLines, g []int, maxLines int) Entry {
	e := Entry{Profile: tasks[g[0]].Profile}
	if len(g) == 1 {
		e.Suffix = tasks[g[0]].Suffix
	}

	estimated := false

	for _, i := range g {
		e.IDs = append(e.IDs, tasks[i].ID)
		e.Lines += tasks[i].Lines
		estimated = estimated || tasks[i].Estimated
		e.spike = e.spike || kindOf(tasks[i]) == KindSpike
		e.named = e.named || tasks[i].With != ""
	}

	for _, i := range g {
		for _, b := range base[i] {
			if !slices.ContainsFunc(e.IDs, func(id string) bool { return strings.EqualFold(id, b.ID) }) {
				e.Bases = addBase(e.Bases, b)
			}
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
func loneReason(tasks []Task, base [][]Base, alone []string, groupLines []int, i, maxLines int) string {
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
		case !sameSet(baseIDs(base[i]), baseIDs(base[j])):
			return "alone: its bases differ from " + o.ID + "'s"
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
// It walks the earlier entries nearest first, so an entry already reached
// through another base adds nothing.
func conflictOrders(tasks []Task, entries []Entry, byID map[string]int) {
	for b := range entries {
		// an order the user set or dropped is not second-guessed
		if slices.ContainsFunc(entries[b].IDs, func(id string) bool {
			return len(tasks[byID[strings.ToUpper(id)]].After) > 0
		}) {
			continue
		}

		for a := b - 1; a >= 0; a-- {
			file := sharedFile(tasks, entries[a], entries[b], byID)
			if file == "" || dependsOn(entries, b, a) || dependsOn(entries, a, b) {
				continue
			}

			lead := entries[a].IDs[0]
			entries[b].Bases = append(entries[b].Bases, Base{
				ID:     lead,
				Reason: "shares " + file + " with " + lead + ": run side by side they would conflict",
			})
		}
	}
}

// collapse names each base by the lead of the entry that holds it, once, so
// two members of one bundle are one base and never an @ twice.
func collapse(entries []Entry, k int) {
	of := entryIndex(entries)

	var out []Base

	for _, b := range entries[k].Bases {
		if j, ok := of[strings.ToUpper(b.ID)]; ok {
			if j == k {
				continue
			}

			b.ID = entries[j].IDs[0]
		}

		out = addBase(out, b)
	}

	entries[k].Bases = out
}

// cutJoin keeps one base of an entry that f10 start would refuse as a join.
// It refuses one when the flow does not finish, or when a base outside the
// list is open, since nothing in the batch would merge it. The open base is
// kept, because the entry needs its code. The rest stay named as dropped.
func cutJoin(e *Entry, in Input, byID map[string]int) {
	if len(e.Bases) < 2 {
		return
	}

	keep, why := 0, "dropped, since a join needs a flow that finishes"

	if in.Joins {
		keep = slices.IndexFunc(e.Bases, func(b Base) bool {
			_, listed := byID[strings.ToUpper(b.ID)]

			done := slices.ContainsFunc(in.Finished, func(f string) bool { return strings.EqualFold(f, b.ID) })

			return !listed && !done
		})
		if keep < 0 {
			return
		}

		why = "dropped, since " + e.Bases[keep].ID + " is open outside the list and a join waits for every base to merge"
	}

	for k := range e.Bases {
		if k != keep {
			e.Bases[k].Dropped = true
			e.Bases[k].Reason += "; " + why
		}
	}
}

// unordered names each task whose order the user dropped but whose body
// still says After:, when its entry spells no base. The start reads the
// body of an entry with no typed base, so only the tracker can drop it.
func unordered(tasks []Task, levels []Level, byID map[string]int) []string {
	var out []string

	for _, st := range levels {
		for _, e := range st.Entries {
			if len(e.waits()) > 0 {
				continue
			}

			for _, id := range e.IDs {
				t := tasks[byID[strings.ToUpper(id)]]
				if dropsOrder(t) && t.Base != "" {
					out = append(out, fmt.Sprintf("%s: f10 start still reads its After: %s line; "+
						"`f10 task update %s --after none` drops it", t.ID, t.Base, t.ID))
				}
			}
		}
	}

	return out
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
	seen := make([]bool, len(entries))
	todo := []int{a}

	for len(todo) > 0 {
		k := todo[len(todo)-1]
		todo = todo[:len(todo)-1]

		if k == b {
			return true
		}

		if seen[k] {
			continue
		}

		seen[k] = true

		for _, id := range entries[k].waits() {
			if next, ok := of[strings.ToUpper(id)]; ok {
				todo = append(todo, next)
			}
		}
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

// levelsOf puts each entry one level below its deepest base. A base outside
// the list counts as done. Inside a level, entries keep the parent's order.
func levelsOf(entries []Entry) ([]Level, error) {
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

		for _, id := range entries[k].waits() {
			b, ok := of[strings.ToUpper(id)]
			if !ok {
				continue
			}

			if err := visit(b, append(path, entries[k].Token())); err != nil {
				return err
			}

			depth[k] = max(depth[k], depth[b]+1)
		}

		state[k] = 2

		return nil
	}

	for k := range entries {
		if err := visit(k, nil); err != nil {
			return nil, err
		}
	}

	var levels []Level

	for k, e := range entries {
		for len(levels) <= depth[k] {
			levels = append(levels, Level{})
		}

		levels[depth[k]].Entries = append(levels[depth[k]].Entries, e)
	}

	return levels, nil
}

// commandArgs spells the levels as f10 start's arguments. An entry whose one
// base ends a chain not yet extended joins it with a comma (A,B). Any other
// base is @ and the lead of the base's entry (C@A), and a profile comes last.
func commandArgs(levels []Level) []string {
	var all []Entry
	for _, st := range levels {
		all = append(all, st.Entries...)
	}

	of := entryIndex(all)

	var chains [][]notation.Entry

	tail := map[int]int{} // an entry ending a chain -> that chain

	for k, e := range all {
		waits := e.waits()
		link := notation.Entry{Task: e.task(), Profile: e.Profile}

		if len(waits) == 1 {
			if b, ok := of[strings.ToUpper(waits[0])]; ok {
				if c, open := tail[b]; open {
					// the comma says this base, as the parser reads it back
					link.Bases = []string{chains[c][len(chains[c])-1].ID()}
					chains[c] = append(chains[c], link)
					delete(tail, b)
					tail[k] = c

					continue
				}
			}
		}

		for _, id := range waits {
			if b, ok := of[strings.ToUpper(id)]; ok {
				id = all[b].IDs[0]
			}

			link.Bases = append(link.Bases, id)
		}

		chains = append(chains, []notation.Entry{link})
		tail[k] = len(chains) - 1
	}

	args := make([]string, 0, len(chains))
	for _, c := range chains {
		args = append(args, notation.FormatChain(c))
	}

	return args
}
