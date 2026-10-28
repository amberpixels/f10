package facts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// The typed half of project.md: the facts the binary acts on, declared in a
// leading YAML block so they are read, not parsed out of prose. Every key is
// optional; an unknown key or a value outside its vocabulary is an error,
// never a default, since a silent default is what the block exists to end.

// Declared is one layer's frontmatter.
type Declared struct {
	Visibility   string            `yaml:"visibility,omitempty"`
	Storage      string            `yaml:"storage,omitempty"`
	Layering     string            `yaml:"layering,omitempty"`
	Tracker      TrackerDecl       `yaml:"tracker,omitempty"`
	MergeMethod  string            `yaml:"merge_method,omitempty"`
	Catchup      string            `yaml:"catchup,omitempty"`
	Traces       string            `yaml:"traces,omitempty"`
	Lore         string            `yaml:"lore,omitempty"`
	StaleAfter   string            `yaml:"stale_after,omitempty"`   // a task older than this is judged before it is planned
	ReviewTraces string            `yaml:"review_traces,omitempty"` // the old spelling of traces, read and folded in
	Flows        map[string]Flow   `yaml:"flows,omitempty"`
	FlowByArea   map[string]string `yaml:"flow_by_area,omitempty"` // an area, in Roles' plain words, to the flow its tasks run
	DriveFinish  string            `yaml:"drive_finish,omitempty"`
	DriveEnv     []string          `yaml:"drive_env,omitempty"` // environment keys a drive passes to its commands, beyond the built-in ones
	ReportBack   string            `yaml:"report_back,omitempty"`
	Review       ReviewDecl        `yaml:"review,omitempty"`
	Standup      StandupDecl       `yaml:"standup,omitempty"`

	// The keys flows replaces, read and converted with a warning.
	Pipelines  map[string]Steps  `yaml:"pipelines,omitempty"`
	Planless   []string          `yaml:"planless,omitempty"`
	ByArea     map[string]string `yaml:"pipeline_by_area,omitempty"`
	DriveChain Steps             `yaml:"drive_chain,omitempty"`
}

// TrackerDecl is the tracker's kind, its task id format (`FT-###`), the
// priority map for host issues, which have no priority field of their own
// (a level of the driver contract's vocabulary to the labels that carry
// it), the status map: a moment f10 reports, `started` or `shipped`, to
// the tracker's own word for it, and the title template (`{id}: {title}`).
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

// ReviewDecl is the remote review's four facts and the optional settled
// mark, in the vocabulary the prose items use, so the same sub-parsers read
// both.
type ReviewDecl struct {
	Reviewer string `yaml:"reviewer,omitempty"`
	Arrives  string `yaml:"arrives,omitempty"`
	Trigger  string `yaml:"trigger,omitempty"`
	Done     string `yaml:"done,omitempty"`
	Settled  string `yaml:"settled,omitempty"`
}

// StandupDecl is what `f10 standup` reads: the paths that, alone in a
// commit, make it upkeep, on top of the lockfiles, generated files and
// snapshots it knows (schema dumps, a project's own codegen output).
type StandupDecl struct {
	Upkeep []string `yaml:"upkeep,omitempty"`
}

// Steps is an ordered list of names, written as a YAML list or as one
// `a → b → c` string.
type Steps []string

// UnmarshalYAML accepts a sequence of strings or one arrow-separated string.
func (s *Steps) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}

		*s = trimSteps(list)
	case yaml.ScalarNode:
		*s = splitSteps(node.Value)
	default:
		return fmt.Errorf("line %d: a list or an `a → b` string", node.Line)
	}

	return nil
}

// splitSteps reads `implement → review (local) → pr`: arrows and commas
// separate, so a step's own words and parentheses stay whole.
func splitSteps(text string) Steps {
	return trimSteps(strings.FieldsFunc(
		strings.NewReplacer("→", ",", "->", ",").Replace(text),
		func(r rune) bool { return r == ',' },
	))
}

func trimSteps(list []string) Steps {
	var out Steps

	for _, step := range list {
		if step = strings.Trim(strings.TrimSpace(step), "*_`"); step != "" {
			out = append(out, step)
		}
	}

	return out
}

// decodeDeclared reads one frontmatter block with unknown keys refused.
func decodeDeclared(block []byte) (Declared, error) {
	var d Declared

	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return Declared{}, err
	}

	return d, nil
}

// The vocabularies the scalar keys accept.
var vocab = map[string][]string{
	"visibility":    {"stealth", "public"},
	"storage":       {"in-repo", "out-of-tree"},
	"layering":      {layout.LayeringExtends, layout.LayeringReplaces},
	"tracker.kind":  {"github", "gitlab", "notion", "jira"},
	"merge_method":  {"squash", "merge", "rebase"},
	"catchup":       {"rebase", "merge", "off"},
	"traces":        {"on", "off"},
	"lore":          {"local", "committed"},
	"review_traces": {"on", "off"},
	"report_back":   {"on", "off"},
	"drive_finish":  {"on", "off"},
}

// staleAfterRE is a stale_after value: whole days or weeks. Hours mean
// nothing against a tracker's created date, and a month has no fixed length.
var staleAfterRE = regexp.MustCompile(`^([1-9]\d*)([dw])$`)

// envKeyRE is an environment variable name, as drive_env lists them.
var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseStaleAfter reads a stale_after value, `10d` or `3w`.
func ParseStaleAfter(value string) (time.Duration, error) {
	m := staleAfterRE.FindStringSubmatch(value)

	var n int
	if m != nil {
		n, _ = strconv.Atoi(m[1])
	}

	// a count past 3650 is a typo, and a large one would overflow the Duration
	if n == 0 || n > 3650 {
		return 0, fmt.Errorf("%q is not a number of days or weeks, like 10d or 3w", value)
	}

	unit := 24 * time.Hour
	if m[2] == "w" {
		unit *= 7
	}

	return time.Duration(n) * unit, nil
}

// trackerKinds maps the declared kind to the name the rest of f10 shows.
var trackerKinds = map[string]string{
	"github": "GitHub Issues",
	"gitlab": "GitLab work items",
	"notion": "Notion",
	"jira":   "Jira",
}

// check reports every value outside its vocabulary, a malformed flow, and
// a planless name that names no pipeline.
func (d Declared) check() []string {
	var bad []string

	for key, value := range map[string]string{
		"visibility":    d.Visibility,
		"storage":       d.Storage,
		"layering":      d.Layering,
		"tracker.kind":  d.Tracker.Kind,
		"merge_method":  d.MergeMethod,
		"catchup":       d.Catchup,
		"traces":        d.Traces,
		"lore":          d.Lore,
		"review_traces": d.ReviewTraces,
		"report_back":   d.ReportBack,
		"drive_finish":  d.DriveFinish,
	} {
		if value != "" && !slices.Contains(vocab[key], value) {
			bad = append(bad, fmt.Sprintf("%s: %q is not one of %s", key, value, strings.Join(vocab[key], ", ")))
		}
	}

	if d.StaleAfter != "" {
		if _, err := ParseStaleAfter(d.StaleAfter); err != nil {
			bad = append(bad, "stale_after: "+err.Error())
		}
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

	for _, name := range d.Planless {
		if _, ok := d.Pipelines[name]; !ok {
			bad = append(bad, fmt.Sprintf("planless: %q names no pipeline", name))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(d.Flows)) {
		bad = append(bad, checkFlow(name, d.Flows[name])...)
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
		if _, ok := driver.PriorityRank(level); !ok || level != strings.ToLower(level) {
			bad = append(
				bad,
				fmt.Sprintf("tracker.priority: %q is not one of %s", level, strings.Join(driver.Priorities, ", ")),
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

// foldTraces moves the old spelling review_traces into traces, so one key
// goes through the merge and a later layer's statement wins whichever word
// it used. Reading it is a warning that names the migration; a layer
// saying both is the ambiguity the block exists to end.
func (d Declared) foldTraces(file string) (Declared, Problem, bool) {
	switch {
	case d.ReviewTraces == "":
		return d, Problem{}, false
	case d.Traces != "":
		return d, Problem{
			Error:   true,
			Message: file + " frontmatter: traces and review_traces both declared; keep traces",
		}, true
	default:
		d.Traces, d.ReviewTraces = d.ReviewTraces, ""

		return d, Problem{
			Message: file + " frontmatter: review_traces is now traces; f10 init --migrate renames it",
		}, true
	}
}

// layered is the frontmatter of every layer merged key by key: a key a
// later layer sets wins outright, as a later layer's prose section does.
// origin records which layer set each key.
type layered struct {
	Declared

	origin map[string]string
}

func (l *layered) merge(d Declared, label string) {
	if l.origin == nil {
		l.origin = map[string]string{}
	}

	set := func(key string, ok bool, apply func()) {
		if ok {
			apply()

			l.origin[key] = label
		}
	}

	set("visibility", d.Visibility != "", func() { l.Visibility = d.Visibility })
	set("storage", d.Storage != "", func() { l.Storage = d.Storage })
	set("layering", d.Layering != "", func() { l.Layering = d.Layering })
	set("tracker", !d.Tracker.empty(), func() { l.Tracker = d.Tracker })
	set("merge_method", d.MergeMethod != "", func() { l.MergeMethod = d.MergeMethod })
	set("catchup", d.Catchup != "", func() { l.Catchup = d.Catchup })
	set("traces", d.Traces != "", func() { l.Traces = d.Traces })
	set("lore", d.Lore != "", func() { l.Lore = d.Lore })
	set("stale_after", d.StaleAfter != "", func() { l.StaleAfter = d.StaleAfter })
	set("flows", d.Flows != nil, func() { l.Flows = d.Flows })
	set("flow_by_area", d.FlowByArea != nil, func() { l.FlowByArea = d.FlowByArea })
	set("drive_finish", d.DriveFinish != "", func() { l.DriveFinish = d.DriveFinish })
	set("drive_env", d.DriveEnv != nil, func() { l.DriveEnv = d.DriveEnv })
	set("pipelines", d.Pipelines != nil, func() { l.Pipelines = d.Pipelines })
	set("planless", d.Planless != nil, func() { l.Planless = d.Planless })
	set("pipeline_by_area", d.ByArea != nil, func() { l.ByArea = d.ByArea })
	set("report_back", d.ReportBack != "", func() { l.ReportBack = d.ReportBack })
	set("drive_chain", d.DriveChain != nil, func() { l.DriveChain = d.DriveChain })
	set("review", d.Review != ReviewDecl{}, func() { l.Review = d.Review })
	set("standup", d.Standup.Upkeep != nil, func() { l.Standup = d.Standup })
}
