// Package facts interprets the declared project.md layers, merges them with
// the detection probes and the contract's defaults, and attributes every
// effective value to its origin. It is the one component of f10 allowed to
// interpret instruction prose - bin/resolve.sh stays a concatenator.
//
// Interpretation is deliberately shallow: only the sub-facts with a fixed
// vocabulary (Layering, Visibility, Storage, the tracker kind) become
// structure; everything else is presented as the declared prose, attributed
// but unparsed. The binary may be incomplete, never wrong - what it cannot
// place it shows as-is instead of guessing.
package facts

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/internal/probe"
	"github.com/amberpixels/f10/internal/resolve"
)

// Origins an effective value can have. Declared origins are qualified by
// the layer label at build time: "declared (main)", "declared (worktree)".
const (
	OriginDetected = "detected"
	OriginDefault  = "default"
	OriginAbsent   = "absent"
)

// A Field is one effective configuration value with its provenance.
type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Prose  bool   `json:"prose,omitempty"`  // multi-line prose, not a scalar
	Origin string `json:"origin"`           // "detected", "declared (<layer>)", "default", "absent"
	Source string `json:"source,omitempty"` // file for declared, evidence for detected
}

// Effective is the merged, attributed view of a project's configuration.
type Effective struct {
	Fields       []Field `json:"fields"`
	Unrecognized []Field `json:"unrecognized,omitempty"`

	// The fixed-vocabulary sub-facts v1 extracts.
	Layering    string `json:"layering"`
	Visibility  string `json:"visibility"`
	Storage     string `json:"storage"`
	TrackerKind string `json:"trackerKind,omitempty"`
}

// contractFields is the project.md contract, in the order
// conventions/context.md lists it.
func contractFields() []string {
	return []string{
		"Project", "Layering", "Roles", "Tracker", "Hosting & PR",
		"Ship pipeline(s)", "Verify", "Review", "Guardrails", "Visibility", "Storage",
	}
}

// canonicalName maps a declared section name to its contract field, or ""
// when it matches none.
func canonicalName(declared string) string {
	norm := strings.ToLower(declared)
	norm = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}

		return -1
	}, norm)

	aliases := map[string]string{
		"project":       "Project",
		"layering":      "Layering",
		"roles":         "Roles",
		"tracker":       "Tracker",
		"hosting":       "Hosting & PR",
		"hostingpr":     "Hosting & PR",
		"shippipeline":  "Ship pipeline(s)",
		"shippipelines": "Ship pipeline(s)",
		"pipeline":      "Ship pipeline(s)",
		"pipelines":     "Ship pipeline(s)",
		"verify":        "Verify",
		"review":        "Review",
		"guardrails":    "Guardrails",
		"visibility":    "Visibility",
		"storage":       "Storage",
	}

	return aliases[norm]
}

// A section is one declared block of a project.md: a contract field or an
// unrecognized heading, with the layer and file it came from.
type section struct {
	name  string // as written
	body  string
	layer string
	file  string
}

// Build merges the declared layers with detection and the contract's
// defaults into one attributed view.
func Build(res *resolve.Resolution, pb *probe.Probes) *Effective {
	declared := map[string]section{}

	var unknown []section

	// field-level merge: a field a later layer mentions wins outright,
	// silence keeps the earlier layer's value (conventions/context.md)
	for _, layer := range res.Layers {
		file := filepath.Join(layer.Dir, "project.md")

		fields, unrec := parseFile(file, layer.Label)
		maps.Copy(declared, fields)

		unknown = append(unknown, unrec...)
	}

	eff := &Effective{
		Layering:   res.Layering,
		Visibility: "stealth",
		Storage:    "in-repo",
	}

	for _, sec := range unknown {
		eff.Unrecognized = append(eff.Unrecognized, Field{
			Name:   sec.name,
			Value:  sec.body,
			Prose:  isProse(sec.body),
			Origin: "declared (" + sec.layer + ")",
			Source: sec.file,
		})
	}

	for _, name := range contractFields() {
		var field Field
		if sec, ok := declared[name]; ok {
			field = Field{
				Name:   name,
				Value:  sec.body,
				Prose:  isProse(sec.body),
				Origin: "declared (" + sec.layer + ")",
				Source: sec.file,
			}
		} else {
			field = fallbackFor(name, res, pb)
		}

		if field.Name == "" {
			continue // a field with nothing to say for this checkout
		}

		eff.Fields = append(eff.Fields, field)
		eff.absorb(field, pb)
	}

	return eff
}

// absorb folds a resolved field's fixed-vocabulary sub-facts into the
// structured summary.
func (e *Effective) absorb(f Field, pb *probe.Probes) {
	lower := strings.ToLower(f.Value)

	switch f.Name {
	case "Visibility":
		if strings.Contains(lower, "public") {
			e.Visibility = "public"
		}
	case "Storage":
		if strings.Contains(lower, "out-of-tree") {
			e.Storage = "out-of-tree"
		}
	case "Tracker":
		e.TrackerKind = trackerKind(lower, pb)
	}
}

// trackerKind extracts the tracker's kind from declared prose, falling back
// to what the CLI routing implies.
func trackerKind(declared string, pb *probe.Probes) string {
	for _, k := range []struct{ needle, kind string }{
		{needle: "notion", kind: "Notion"},
		{needle: "github", kind: "GitHub Issues"},
		{needle: "gitlab", kind: "GitLab work items"},
		{needle: "jira", kind: "Jira"},
	} {
		if strings.Contains(declared, k.needle) {
			return k.kind
		}
	}

	switch pb.CLI.Value {
	case "gh":
		return "GitHub Issues"
	case "glab":
		return "GitLab work items"
	}

	return ""
}

// fallbackFor answers for a contract field no layer declared: detection
// where a probe can, the contract's default where one exists, absent
// otherwise. A zero Field means the field has nothing to say here.
func fallbackFor(name string, res *resolve.Resolution, pb *probe.Probes) Field {
	switch name {
	case "Project":
		return detectedList(name, pb.Stack)
	case "Layering":
		// meaningful only in a linked worktree; elsewhere there is
		// nothing to layer onto
		if res.MainRoot == "" || res.CheckoutRoot == res.MainRoot {
			return Field{}
		}

		return Field{Name: name, Value: res.Layering, Origin: OriginDefault}
	case "Tracker":
		return detectedTracker(name, pb)
	case "Hosting & PR":
		if pb.Host == "" {
			return Field{Name: name, Origin: OriginAbsent}
		}

		return Field{
			Name:   name,
			Value:  pb.Host + " (" + pb.CLI.Value + ")",
			Origin: OriginDetected,
			Source: pb.CLI.Evidence,
		}
	case "Ship pipeline(s)":
		return Field{Name: name, Value: "implement → pr", Origin: OriginDefault}
	case "Verify":
		return detectedList(name, pb.Verify)
	case "Visibility":
		return Field{Name: name, Value: "stealth", Origin: OriginDefault}
	case "Storage":
		return storageField(name, res)
	default: // Roles, Review, Guardrails: nothing infers them
		return Field{Name: name, Origin: OriginAbsent}
	}
}

// detectedList renders probe signals as one detected value, or absent when
// no signal fired.
func detectedList(name string, signals []probe.Signal) Field {
	if len(signals) == 0 {
		return Field{Name: name, Origin: OriginAbsent}
	}

	values := make([]string, 0, len(signals))
	evidence := make([]string, 0, len(signals))

	for _, s := range signals {
		values = append(values, s.Value+" ("+s.Evidence+")")
		evidence = append(evidence, s.Evidence)
	}

	return Field{
		Name:   name,
		Value:  strings.Join(values, ", "),
		Origin: OriginDetected,
		Source: strings.Join(evidence, ", "),
	}
}

func detectedTracker(name string, pb *probe.Probes) Field {
	switch pb.CLI.Value {
	case "gh":
		return Field{Name: name, Value: "GitHub Issues (via gh)", Origin: OriginDetected, Source: pb.CLI.Evidence}
	case "glab":
		return Field{Name: name, Value: "GitLab work items (via glab)", Origin: OriginDetected, Source: pb.CLI.Evidence}
	case "unknown":
		return Field{Name: name, Value: "unknown - " + pb.CLI.Evidence, Origin: OriginDetected, Source: pb.CLI.Evidence}
	default:
		return Field{Name: name, Origin: OriginAbsent}
	}
}

// storageField reports where storage landed. Out-of-tree declares itself by
// location, so finding it there is detection, not a default.
func storageField(name string, res *resolve.Resolution) Field {
	for _, l := range res.Layers {
		if l.Label == "out-of-tree" {
			return Field{
				Name:   name,
				Value:  "out-of-tree (" + res.StorageRoot + ")",
				Origin: OriginDetected,
				Source: l.Dir,
			}
		}
	}

	return Field{Name: name, Value: "in-repo (" + res.StorageRoot + ")", Origin: OriginDefault}
}

// parseFile splits one project.md into contract sections. Both declared
// shapes open a section: an unindented bold list item (`- **Tracker** - …`)
// and a markdown heading (`## Tracker`). Everything until the next opener
// belongs to the section - indented sub-bullets stay in their field's body.
// Openers matching no contract field are returned separately, not dropped.
func parseFile(path, layer string) (map[string]section, []section) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}

	boldItem := regexp.MustCompile(`^[-*]\s+\*\*([^*]+)\*\*\s*[-:]?\s*(.*)$`)
	heading := regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

	fields := map[string]section{}

	var (
		unknown []section
		current *section
	)

	flush := func() {
		if current == nil {
			return
		}

		current.body = strings.TrimSpace(current.body)
		if canonical := canonicalName(current.name); canonical != "" {
			fields[canonical] = *current
		} else {
			unknown = append(unknown, *current)
		}

		current = nil
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if m := boldItem.FindStringSubmatch(line); m != nil {
			flush()

			current = &section{name: strings.TrimSpace(m[1]), body: m[2], layer: layer, file: path}

			continue
		}

		if m := heading.FindStringSubmatch(line); m != nil {
			flush()

			current = &section{name: m[1], layer: layer, file: path}

			continue
		}

		if current != nil {
			current.body += "\n" + line
		}
	}

	flush()

	return fields, unknown
}

func isProse(value string) bool {
	return strings.Contains(strings.TrimSpace(value), "\n")
}
