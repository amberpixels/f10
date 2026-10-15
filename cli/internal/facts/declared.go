package facts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// The typed half of project.md: the facts the binary acts on, declared in a
// leading YAML block so they are read, not parsed out of prose. Every key is
// optional; an unknown key or a value outside its vocabulary is an error,
// never a default, since a silent default is what the block exists to end.

// Declared is one layer's frontmatter.
type Declared struct {
	Visibility  string           `yaml:"visibility,omitempty"`
	Storage     string           `yaml:"storage,omitempty"`
	Layering    string           `yaml:"layering,omitempty"`
	Tracker     TrackerDecl      `yaml:"tracker,omitempty"`
	MergeMethod string           `yaml:"merge_method,omitempty"`
	Catchup     string           `yaml:"catchup,omitempty"`
	Pipelines   map[string]Steps `yaml:"pipelines,omitempty"`
	Planless    []string         `yaml:"planless,omitempty"`
	DriveChain  Steps            `yaml:"drive_chain,omitempty"`
	Review      ReviewDecl       `yaml:"review,omitempty"`
	Standup     StandupDecl      `yaml:"standup,omitempty"`
}

// TrackerDecl is the tracker's kind and its task id format (`FT-###`).
type TrackerDecl struct {
	Kind string `yaml:"kind,omitempty"`
	ID   string `yaml:"id,omitempty"`
}

// ReviewDecl is the remote review's four facts, in the vocabulary the
// prose items use, so the same sub-parsers read both.
type ReviewDecl struct {
	Reviewer string `yaml:"reviewer,omitempty"`
	Arrives  string `yaml:"arrives,omitempty"`
	Trigger  string `yaml:"trigger,omitempty"`
	Done     string `yaml:"done,omitempty"`
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
	"visibility":   {"stealth", "public"},
	"storage":      {"in-repo", "out-of-tree"},
	"layering":     {layout.LayeringExtends, layout.LayeringReplaces},
	"tracker.kind": {"github", "gitlab", "notion", "jira"},
	"merge_method": {"squash", "merge", "rebase"},
	"catchup":      {"rebase", "merge"},
}

// trackerKinds maps the declared kind to the name the rest of f10 shows.
var trackerKinds = map[string]string{
	"github": "GitHub Issues",
	"gitlab": "GitLab work items",
	"notion": "Notion",
	"jira":   "Jira",
}

// check reports every value outside its vocabulary, and a planless name
// that names no pipeline.
func (d Declared) check() []string {
	var bad []string

	for key, value := range map[string]string{
		"visibility":   d.Visibility,
		"storage":      d.Storage,
		"layering":     d.Layering,
		"tracker.kind": d.Tracker.Kind,
		"merge_method": d.MergeMethod,
		"catchup":      d.Catchup,
	} {
		if value != "" && !slices.Contains(vocab[key], value) {
			bad = append(bad, fmt.Sprintf("%s: %q is not one of %s", key, value, strings.Join(vocab[key], ", ")))
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

	slices.Sort(bad)

	return bad
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
	set("tracker", d.Tracker != TrackerDecl{}, func() { l.Tracker = d.Tracker })
	set("merge_method", d.MergeMethod != "", func() { l.MergeMethod = d.MergeMethod })
	set("catchup", d.Catchup != "", func() { l.Catchup = d.Catchup })
	set("pipelines", d.Pipelines != nil, func() { l.Pipelines = d.Pipelines })
	set("planless", d.Planless != nil, func() { l.Planless = d.Planless })
	set("drive_chain", d.DriveChain != nil, func() { l.DriveChain = d.DriveChain })
	set("review", d.Review != ReviewDecl{}, func() { l.Review = d.Review })
	set("standup", d.Standup.Upkeep != nil, func() { l.Standup = d.Standup })
}
