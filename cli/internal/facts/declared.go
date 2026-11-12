package facts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// The typed half of project.md, a leading YAML block. An unknown key or a
// value outside its vocabulary is an error, never a default, since a silent
// default is what the block exists to end.

// Declared is one layer's frontmatter.
type Declared struct {
	Visibility     string            `yaml:"visibility,omitempty"`
	Storage        string            `yaml:"storage,omitempty"`
	Layering       string            `yaml:"layering,omitempty"`
	Tracker        TrackerDecl       `yaml:"tracker,omitempty"`
	MergeMethod    string            `yaml:"merge_method,omitempty"`
	Catchup        string            `yaml:"catchup,omitempty"`
	CatchupStacked string            `yaml:"catchup_stacked,omitempty"` // how a branch catches up with a base that is another task's branch
	Traces         string            `yaml:"traces,omitempty"`
	Lore           string            `yaml:"lore,omitempty"`
	LoreGate       string            `yaml:"lore_gate,omitempty"`      // what finish does with a failing lore check before a merge
	StaleAfter     string            `yaml:"stale_after,omitempty"`    // a task older than this is judged before it is planned
	DriverTimeout  string            `yaml:"driver_timeout,omitempty"` // one driver verb's limit, a Go duration
	Flows          map[string]Flow   `yaml:"flows,omitempty"`
	FlowByArea     map[string]string `yaml:"flow_by_area,omitempty"`  // an area, in Roles' plain words, to the flow its tasks run
	DriveEnv       []string          `yaml:"drive_env,omitempty"`     // environment keys a drive passes to its commands, beyond the built-in ones
	Watch          string            `yaml:"watch,omitempty"`         // what the daemon does for tasks no drive owns
	WatchCatchup   string            `yaml:"watch_catchup,omitempty"` // the watcher also catches an undriven open PR up with its target
	ReportBack     string            `yaml:"report_back,omitempty"`
	Review         ReviewDecl        `yaml:"review,omitempty"`
	Standup        StandupDecl       `yaml:"standup,omitempty"`
	Verify         []VerifyDecl      `yaml:"verify,omitempty"`  // verify commands, each scoped to the paths it checks
	Agents         map[string]string `yaml:"agents,omitempty"`  // a step, `default` or `step.kind` to the agent profile it runs on
	Paces          map[string]Pace   `yaml:"paces,omitempty"`   // a pace's name to its dials, laid over the built-ins key by key
	Pace           string            `yaml:"pace,omitempty"`    // the pace a run takes when it names none
	Ceiling        *Ceiling          `yaml:"ceiling,omitempty"` // the cap on every pace but the ones it excepts
}

// TrackerDecl is the frontmatter's tracker key. Priority maps a contract
// level to host labels, since issues have no priority field of their own.
type TrackerDecl struct {
	Kind     string            `yaml:"kind,omitempty"`
	ID       string            `yaml:"id,omitempty"`
	Priority map[string]Labels `yaml:"priority,omitempty"`
	Status   map[string]string `yaml:"status,omitempty"`
	Title    string            `yaml:"title,omitempty"`
}

func (t TrackerDecl) empty() bool {
	return t.Kind == "" && t.ID == "" && t.Priority == nil && t.Status == nil && t.Title == ""
}

// StatusMoments are the moments tracker.status may name.
var StatusMoments = []string{"started", "shipped"}

// Labels is a list of label names, written as a YAML list or as one string.
type Labels []string

// UnmarshalYAML accepts a sequence of strings or one string; blanks drop.
func (l *Labels) UnmarshalYAML(node *yaml.Node) error {
	var list []string

	switch node.Kind {
	case yaml.SequenceNode:
		if err := node.Decode(&list); err != nil {
			return err
		}
	case yaml.ScalarNode:
		list = []string{node.Value}
	default:
		return fmt.Errorf("line %d: a label or a list of labels", node.Line)
	}

	*l = Labels{}

	for _, name := range list {
		if name = strings.TrimSpace(name); name != "" {
			*l = append(*l, name)
		}
	}

	return nil
}

// ReviewDecl is the remote review's facts in the prose items' vocabulary, so
// the same sub-parsers read both.
type ReviewDecl struct {
	Reviewer string `yaml:"reviewer,omitempty"`
	Arrives  string `yaml:"arrives,omitempty"`
	Trigger  string `yaml:"trigger,omitempty"`
	Done     string `yaml:"done,omitempty"`
	Settled  string `yaml:"settled,omitempty"`
}

// StandupDecl lists paths that, alone in a commit, make it upkeep, on top of
// the lockfiles and generated files standup already knows.
type StandupDecl struct {
	Upkeep []string `yaml:"upkeep,omitempty"`
}

// VerifyDecl is one verify command and the paths it checks, in standup's
// glob syntax; a command with no paths runs on every diff.
type VerifyDecl struct {
	Run   string   `json:"run"             yaml:"run"`
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
}

// trimSteps is the list with markup and blanks cut.
func trimSteps(list []string) []string {
	var out []string

	for _, step := range list {
		if step = strings.Trim(strings.TrimSpace(step), "*_`"); step != "" {
			out = append(out, step)
		}
	}

	return out
}

// retiredKeys name the key that replaced each retired one, because deleting
// a retired key alone can change behavior: without drive_finish: off, a
// drive merges.
var retiredKeys = map[string]string{
	"drive_finish":     "declare `ceiling: { finish: off }` to keep drives from merging, or `paces.<name>.finish` for one pace",
	"review_traces":    "rename it to `traces`",
	"pipelines":        "declare each pipeline under `flows`, led by `plan` unless it was planless",
	"planless":         "drop `plan` from those `flows`",
	"pipeline_by_area": "rename it to `flow_by_area`",
	"drive_chain":      "fold its steps into `flows.default`; a pace's `finish` says whether drive merges",
}

// decodeDeclared reads one frontmatter block with unknown keys refused, and
// a retired key refused with what replaced it.
func decodeDeclared(block []byte) (Declared, error) {
	var d Declared

	var top map[string]yaml.Node
	if yaml.Unmarshal(block, &top) == nil {
		var bad []string

		for _, key := range slices.Sorted(maps.Keys(top)) {
			if repl, ok := retiredKeys[key]; ok {
				bad = append(bad, key+" is retired: "+repl)
			}
		}

		if len(bad) > 0 {
			return Declared{}, errors.New(strings.Join(bad, "; "))
		}
	}

	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		// YAML refuses a plain ci? inside { } with a syntax error that names
		// neither; a TypeError (an unknown key) is never that
		var te *yaml.TypeError
		if !errors.As(err, &te) && bytes.Contains(block, []byte("ci?")) {
			err = fmt.Errorf("%w (inside { } quote an optional CI review: \"local+ci?\")", err)
		}

		return Declared{}, err
	}

	return d, nil
}

// A scalarKey is one frontmatter key holding a single word, so it is
// declared once here and every list that handles such keys reads the table:
// the check, the layer merge, scaffold's emptiness test and resolve's facts.
type scalarKey struct {
	key   string
	field func(*Declared) *string
	vocab []string           // the words it accepts; nil when parse checks it
	parse func(string) error // checks a value no fixed word list covers, such as a duration
	def   string             // what f10 config shows when no layer sets it; "" shows nothing
}

var scalarKeys = []scalarKey{
	{
		key:   "visibility",
		field: func(d *Declared) *string { return &d.Visibility },
		vocab: []string{"stealth", "public"},
		def:   "stealth",
	},
	{
		key:   "storage",
		field: func(d *Declared) *string { return &d.Storage },
		vocab: []string{"in-repo", "out-of-tree"},
		def:   "in-repo",
	},
	{
		key:   "layering",
		field: func(d *Declared) *string { return &d.Layering },
		vocab: []string{layout.LayeringExtends, layout.LayeringReplaces},
	},
	{
		key:   "merge_method",
		field: func(d *Declared) *string { return &d.MergeMethod },
		vocab: []string{"squash", "merge", "rebase"},
	},
	{
		key:   "catchup",
		field: func(d *Declared) *string { return &d.Catchup },
		vocab: []string{"rebase", "merge", "off"},
		def:   "rebase",
	},
	// a stacked base is merged unless the project takes the replay a rebase risks
	{
		key:   "catchup_stacked",
		field: func(d *Declared) *string { return &d.CatchupStacked },
		vocab: []string{"merge", "rebase"},
		def:   "merge",
	},
	{key: "traces", field: func(d *Declared) *string { return &d.Traces }, vocab: []string{"on", "off"}, def: "off"},
	{
		key:   "lore",
		field: func(d *Declared) *string { return &d.Lore },
		vocab: []string{"local", "committed"},
		def:   "local",
	},
	{
		key:   "lore_gate",
		field: func(d *Declared) *string { return &d.LoreGate },
		vocab: []string{"warn", "block"},
		def:   "warn",
	},
	{
		key:   "stale_after",
		field: func(d *Declared) *string { return &d.StaleAfter },
		parse: discard(ParseStaleAfter),
		def:   "off",
	},
	{
		key:   "driver_timeout",
		field: func(d *Declared) *string { return &d.DriverTimeout },
		parse: discard(ParseDriverTimeout),
		def:   trackerdriver.DefaultTimeout.String(),
	},
	{
		key:   "watch",
		field: func(d *Declared) *string { return &d.Watch },
		vocab: []string{"report", "finish", "off"},
		def:   "finish",
	},
	// off unless declared: an unattended catchup pushed with a lease is the
	// riskiest thing the watcher does
	{
		key:   "watch_catchup",
		field: func(d *Declared) *string { return &d.WatchCatchup },
		vocab: []string{"on", "off"},
		def:   "off",
	},
	{
		key:   "report_back",
		field: func(d *Declared) *string { return &d.ReportBack },
		vocab: []string{"on", "off"},
		def:   "on",
	},
	{key: "pace", field: func(d *Declared) *string { return &d.Pace }, parse: checkPaceName},
}

// scalarNamed is the registry's entry for key; a key the table lacks is a
// programming error.
func scalarNamed(key string) scalarKey {
	for _, k := range scalarKeys {
		if k.key == key {
			return k
		}
	}

	panic("facts: no scalar key " + key)
}

func discard[T any](parse func(string) (T, error)) func(string) error {
	return func(value string) error {
		_, err := parse(value)

		return err
	}
}

func checkPaceName(value string) error {
	if !flowNameRE.MatchString(value) {
		return fmt.Errorf("%q is not a lowercase name", value)
	}

	return nil
}

// envKeyRE is an environment variable name, as drive_env lists them.
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseStaleAfter reads a stale_after value, `10d` or `3w`.
func ParseStaleAfter(value string) (time.Duration, error) {
	days, err := calendar.OffsetDays(value)
	if err != nil {
		return 0, err
	}

	return time.Duration(days) * 24 * time.Hour, nil
}

// ParseDriverTimeout reads a driver_timeout value, a positive Go duration
// like `45s` or `2m`.
func ParseDriverTimeout(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a positive duration, like 45s or 2m", value)
	}

	return d, nil
}

// check reports every value outside its vocabulary and a malformed flow.
func (d Declared) check() []string {
	var bad []string

	for _, k := range scalarKeys {
		value := *k.field(&d)

		switch {
		case value == "":
		case k.parse != nil:
			if err := k.parse(value); err != nil {
				bad = append(bad, k.key+": "+err.Error())
			}
		case !slices.Contains(k.vocab, value):
			bad = append(bad, fmt.Sprintf("%s: %q is not one of %s", k.key, value, strings.Join(k.vocab, ", ")))
		}
	}

	if kind := TrackerKind(d.Tracker.Kind); kind != "" && !slices.Contains(TrackerKinds, kind) {
		words := make([]string, len(TrackerKinds))
		for i, k := range TrackerKinds {
			words[i] = string(k)
		}

		bad = append(bad, fmt.Sprintf("tracker.kind: %q is not one of %s", kind, strings.Join(words, ", ")))
	}

	for _, key := range d.DriveEnv {
		if !envKeyRE.MatchString(key) {
			bad = append(bad, fmt.Sprintf("drive_env: %q is not an environment variable name", key))
		}
	}

	if d.Tracker.ID != "" && idPrefix(d.Tracker.ID) == "" {
		bad = append(bad, fmt.Sprintf("tracker.id: %q has no id format like `ABC-###`", d.Tracker.ID))
	}

	for _, glob := range d.Standup.Upkeep {
		if err := standup.CheckGlob(glob); err != nil {
			bad = append(bad, fmt.Sprintf("standup.upkeep: %q: %v", glob, err))
		}
	}

	if d.Verify != nil && len(d.Verify) == 0 {
		bad = append(bad, "verify: the list is empty; name a command, or drop the key to run the Verify heading")
	}

	for i, v := range d.Verify {
		if strings.TrimSpace(v.Run) == "" {
			bad = append(bad, fmt.Sprintf("verify[%d]: run is empty; name the command", i))
		}

		for _, glob := range v.Paths {
			if err := standup.CheckGlob(glob); err != nil {
				bad = append(bad, fmt.Sprintf("verify[%d].paths: %q: %v", i, glob, err))
			}
		}
	}

	// the values here; the keys are checked against the merged flows once
	// those are resolved (agents.go)
	for _, key := range slices.Sorted(maps.Keys(d.Agents)) {
		if _, err := agentctl.ParseProfile(d.Agents[key]); err != nil {
			bad = append(bad, fmt.Sprintf("agents.%s: %v", key, err))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(d.Paces)) {
		bad = append(bad, CheckPace(name, d.Paces[name])...)
	}

	if d.Ceiling != nil {
		bad = append(bad, CheckCeiling(*d.Ceiling)...)
	}

	steps := FlowSteps(d.Flows)
	for _, name := range slices.Sorted(maps.Keys(d.Flows)) {
		bad = append(bad, checkFlow(name, d.Flows[name], steps...)...)
	}

	for _, moment := range slices.Sorted(maps.Keys(d.Tracker.Status)) {
		switch {
		case !slices.Contains(StatusMoments, moment):
			bad = append(
				bad,
				fmt.Sprintf("tracker.status: %q is not one of %s", moment, strings.Join(StatusMoments, ", ")),
			)
		case strings.TrimSpace(d.Tracker.Status[moment]) == "":
			bad = append(bad, fmt.Sprintf("tracker.status: %s has no status word", moment))
		}
	}

	bad = append(bad, d.Tracker.checkPriority()...)

	if d.Tracker.Title != "" {
		if _, err := tasktitle.Parse(d.Tracker.Title); err != nil {
			bad = append(bad, "tracker.title: "+err.Error())
		}
	}

	slices.Sort(bad)

	return bad
}

// checkPriority reports a level outside the contract's vocabulary and a
// label listed under two levels, which would make the index ambiguous. A
// level with no labels maps nothing and is not an error.
func (t TrackerDecl) checkPriority() []string {
	var bad []string

	seen := map[string]string{} // lower-cased label to the level it was first seen under

	for _, level := range slices.Sorted(maps.Keys(t.Priority)) {
		if _, ok := trackerdriver.PriorityRank(level); !ok || level != strings.ToLower(level) {
			bad = append(
				bad,
				fmt.Sprintf(
					"tracker.priority: %q is not one of %s",
					level,
					strings.Join(trackerdriver.Priorities, ", "),
				),
			)

			continue
		}

		for _, name := range t.Priority[level] {
			key := strings.ToLower(name)
			if other, dup := seen[key]; dup && other != level {
				bad = append(
					bad,
					fmt.Sprintf("tracker.priority: label %q is listed under both %s and %s", name, other, level),
				)

				continue
			}

			seen[key] = level
		}
	}

	return bad
}

// layered is every layer's frontmatter merged key by key, a later layer
// winning outright as its prose sections do; origin records who set each key.
type layered struct {
	Declared

	origin map[string]string
	user   map[string]string // a label the user's own file merged under, to that file's path
}

// originOf is the layer that set key: the user's file, or a project.md's
// frontmatter.
func (l *layered) originOf(key string) Origin {
	label := l.origin[key]
	if path, ok := l.user[label]; ok {
		return DeclaredByUser(path)
	}

	return DeclaredFrontmatter(label)
}

// mergePace lays p over the pace at name, key by key, recording each key's
// layer under prefix.
func (l *layered) mergePace(into, p Pace, prefix, label string) Pace {
	into = into.clone()

	for key, dials := range p.Steps {
		into.Steps[key] = dials
		l.origin[prefix+key] = label
	}

	if p.written() {
		into.Finish, into.finishBad = p.Finish, p.finishBad
		l.origin[prefix+PaceFinishKey] = label
	}

	return into
}

func (l *layered) merge(d Declared, label string) {
	if l.origin == nil {
		l.origin = map[string]string{}
	}

	for _, k := range scalarKeys {
		if value := *k.field(&d); value != "" {
			*k.field(&l.Declared) = value
			l.origin[k.key] = label
		}
	}

	set := func(key string, ok bool, apply func()) {
		if ok {
			apply()

			l.origin[key] = label
		}
	}

	set("tracker", !d.Tracker.empty(), func() { l.Tracker = d.Tracker })
	set("flows", d.Flows != nil, func() { l.Flows = d.Flows })
	set("flow_by_area", d.FlowByArea != nil, func() { l.FlowByArea = d.FlowByArea })
	set("drive_env", d.DriveEnv != nil, func() { l.DriveEnv = d.DriveEnv })
	set("review", d.Review != ReviewDecl{}, func() { l.Review = d.Review })
	set("standup", d.Standup.Upkeep != nil, func() { l.Standup = d.Standup })
	set("verify", d.Verify != nil, func() { l.Verify = d.Verify })

	// paces merge per pace and per key, as agents do: a worktree tuning
	// fast's review keeps main's fast judge
	for name, p := range d.Paces {
		if l.Paces == nil {
			l.Paces = map[string]Pace{}
		}

		cur, ok := l.Paces[name]
		if !ok {
			l.origin["paces."+name] = label
		}

		l.Paces[name] = l.mergePace(cur, p, "paces."+name+".", label)
	}

	// the ceiling merges key by key as paces do; except is one list, and a
	// later layer's replaces it whole
	if d.Ceiling != nil {
		if l.Ceiling == nil {
			l.Ceiling = &Ceiling{}
		}

		l.Ceiling.Pace = l.mergePace(l.Ceiling.Pace, d.Ceiling.Pace, "ceiling.", label)

		if d.Ceiling.Except != nil {
			l.Ceiling.Except = d.Ceiling.Except
			l.origin["ceiling.except"] = label
		}
	}

	// the agents map merges key by key: a worktree tuning one step keeps
	// main's others, and each key remembers the layer that set it
	for key, value := range d.Agents {
		if l.Agents == nil {
			l.Agents = map[string]string{}
		}

		l.Agents[key] = value
		l.origin["agents."+key] = label
	}
}
