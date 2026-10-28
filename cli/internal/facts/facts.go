// Package facts interprets the declared project.md layers, merges them with
// the detection probes and the contract's defaults, and attributes every
// effective value to its origin. It is the one component of f10 allowed to
// interpret instruction prose - bin/f10-load-project.sh stays a concatenator.
//
// Interpretation stays shallow: only the sub-facts with a fixed vocabulary
// (Layering, Visibility, Storage, the tracker kind and its id prefix, the
// remote review's four facts) become structure; everything else is shown as
// the declared prose, attributed but unparsed. The binary may be incomplete,
// never wrong - what it cannot place it shows as-is instead of guessing.
//
// It writes that prose too, in scaffold.go: `f10 init` renders detection as
// a project.md. Reader and writer share a package so the vocabulary one
// keys on and the words the other emits cannot drift apart.
package facts

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
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

	// The fixed-vocabulary sub-facts.
	Layering        string `json:"layering"`
	Visibility      string `json:"visibility"`
	Storage         string `json:"storage"`
	TrackerKind     string `json:"trackerKind,omitempty"`
	IDPrefix        string `json:"idPrefix,omitempty"`        // "ABC", "F10" - the task id format's letters
	IDPrefixOrigin  string `json:"idPrefixOrigin,omitempty"`  // "declared (<layer>)" or "derived from project name <name>"
	IDPrefixDerived bool   `json:"idPrefixDerived,omitempty"` // the prefix came from the project's name, not a declaration

	// PriorityLabels is tracker.priority inverted: a lower-cased host label
	// to the level it carries; nil when none is declared.
	PriorityLabels map[string]string `json:"priorityLabels,omitempty"`

	// TitleTemplate is tracker.title, the template a task's title is written
	// through on the tracker; "" when none is declared, and titles stay bare.
	TitleTemplate tasktitle.Template `json:"titleTemplate,omitempty"`

	// TrackerStatus is tracker.status: `started` and `shipped` to the
	// tracker's own word; nil when none is declared, and f10 moves no status.
	TrackerStatus map[string]string `json:"trackerStatus,omitempty"`

	// The remote review's four facts (review.go); zero when Review declares
	// no remote reviewer.
	Review Review `json:"review"`

	// The rest of the typed facts (resolve.go). MergeMethod "" means none
	// declared: finish asks the repo.
	MergeMethod   string              `json:"mergeMethod,omitempty"`
	Catchup       string              `json:"catchup"`
	Traces        bool                `json:"traces"`                // traces: on; a step's commits carry trailers, the squash commit keeps the task, a review or judge after the PR leaves a status and a comment
	Lore          string              `json:"lore"`                  // local: lore under the storage root; committed: .lore/ at the checkout root
	LoreGate      string              `json:"loreGate"`              // lore_gate: warn or block, what f10 finish does with a failing lore check before a merge
	StaleAfter    string              `json:"staleAfter,omitempty"`  // stale_after: a task older than this is judged before it is planned; "" is off
	DriverTimeout time.Duration       `json:"driverTimeout"`         // driver_timeout: one driver verb's limit
	Flows         map[string][]string `json:"flows"`                 // a flow's name to its entries, in the colon form
	FlowByArea    map[string]string   `json:"flowByArea,omitempty"`  // flow_by_area: an area to the flow its tasks run
	DriveFinish   bool                `json:"driveFinish"`           // drive_finish: f10 drive ends each task with finish
	DriveEnv      []string            `json:"driveEnv,omitempty"`    // drive_env: environment keys a drive passes on, beyond the built-in ones
	Watch         string              `json:"watch"`                 // watch: report, finish or off, for tasks no drive owns; finish unless declared
	ReportBack    bool                `json:"reportBack"`            // report_back: a ship run posts its shipment on the task; on unless declared off
	UpkeepPaths   []string            `json:"upkeepPaths,omitempty"` // standup.upkeep: globs on top of the defaults

	// Facts is every typed value with its origin; Problems what is wrong
	// with how they were declared.
	Facts    []Fact    `json:"facts"`
	Problems []Problem `json:"problems,omitempty"`

	proseRead [][2]string // file, key: a fact settled from prose
	twice     [][2]string // file, key: a fact both places declare alike
}

// contractFields is the project.md contract. The order is presentation
// order, not the contract's listing order: the short identity facts
// (Project, Visibility, Storage, Layering) lead, the prose fields follow.
func contractFields() []string {
	return []string{
		"Project", "Visibility", "Storage", "Layering", "Roles", "Tracker",
		"Hosting & PR", "Verify", "Review", "Guardrails",
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
		"drivechain":    "Drive chain",
		"chain":         "Drive chain",
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
func Build(lay *layout.Layout, pb *probe.Probes) *Effective {
	declared := map[string]section{}

	var (
		unknown  []section
		fm       layered
		problems []Problem
	)

	// field-level merge: a field a later layer mentions wins outright,
	// silence keeps the earlier layer's value (conventions/context.md); the
	// frontmatter merges key by key on the same rule
	for _, layer := range lay.Layers {
		file := filepath.Join(layer.Dir, "project.md")

		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		block, body, ok := frontmatter.Split(data)
		if ok {
			d, err := decodeDeclared(block)
			if err != nil {
				problems = append(problems, Problem{Error: true, Message: file + " frontmatter: " + err.Error()})
			}

			for _, bad := range d.check() {
				problems = append(problems, Problem{Error: true, Message: file + " frontmatter: " + bad})
			}

			folded, p, found := d.foldTraces(file)
			if found {
				problems = append(problems, p)
			}

			d = folded

			fm.merge(d, layer.Label)
		}

		fields, unrec := parseFile(body, file, layer.Label)
		maps.Copy(declared, fields)

		unknown = append(unknown, unrec...)
	}

	eff := &Effective{
		Layering:   lay.Layering,
		Visibility: "stealth",
		Storage:    "in-repo",
		Problems:   problems,
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
			value := sec.body
			if name == "Roles" {
				value = rolesLines(value)
			}

			field = Field{
				Name:   name,
				Value:  value,
				Prose:  isProse(value),
				Origin: "declared (" + sec.layer + ")",
				Source: sec.file,
			}
		} else {
			field = fallbackFor(name, lay, pb)
		}

		if field.Name == "" {
			continue // a field with nothing to say for this checkout
		}

		eff.Fields = append(eff.Fields, field)
	}

	eff.resolve(&fm, declared, lay, pb)

	return eff
}

// The two shapes a declared task id format takes. The contract's placeholder
// form (`ABC-####`) is trusted first; a literal sample (`ABC-1234`) is the
// fallback, since that shape also fits UTF-8 or ISO-8601. Neither ends on \b:
// `#` is not a word character, so `**`ABC-####`**` would never match.
var (
	idPlaceholderRE = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9})-#+`)
	idSampleRE      = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9})-\d+\b`)
)

// idPrefix extracts the task id format's letters from declared Tracker
// prose, or "" when it declares none. The prefix is the half the ref
// lookup needs: it is how a branch name is searched and how a plan file
// is named.
func idPrefix(declared string) string {
	for _, re := range []*regexp.Regexp{idPlaceholderRE, idSampleRE} {
		if m := re.FindStringSubmatch(declared); m != nil {
			return m[1]
		}
	}

	return ""
}

// The shape a usable prefix has: what idPlaceholderRE accepts, anchored,
// since f10 init writes a derived prefix back and it must parse again.
var prefixRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// The two cuts projectPrefix makes on a single word: a name this short
// that holds a digit is already an abbreviation, a longer word gives up
// its first letters.
const (
	shortName  = 4
	wordPrefix = 3
)

// ProjectPrefix derives a task id prefix from the project's name, for a
// project that declares no id format. The host's letters would name every
// GitLab project GL; the project's own name says which project in a branch
// or a plan filename. A short name with a digit keeps its letters (f10 ->
// F10, r3 -> R3), several segments give initials (git-undo -> GU), one word
// gives its first three characters (herdr -> HER). A name that yields
// nothing usable (one letter, a leading digit) derives no prefix, so only
// explicit ids are accepted and f10 init leaves the Tracker section to a human.
func ProjectPrefix(name string) string {
	segments := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})

	for i, seg := range segments {
		segments[i] = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}

			return -1
		}, seg)
	}

	segments = slices.DeleteFunc(segments, func(s string) bool { return s == "" })

	var derived strings.Builder

	switch {
	case len(segments) == 0:
		return ""
	case len(segments) == 1:
		word := segments[0]
		if len(word) <= shortName && strings.ContainsAny(word, "0123456789") {
			derived.WriteString(word)
		} else {
			derived.WriteString(word[:min(wordPrefix, len(word))])
		}
	default:
		for _, seg := range segments {
			derived.WriteByte(seg[0])
		}
	}

	prefix := strings.ToUpper(derived.String())
	if !prefixRE.MatchString(prefix) {
		return ""
	}

	return prefix
}

// fallbackFor answers for a contract field no layer declared: detection
// where a probe can, the contract's default where one exists, absent
// otherwise. A zero Field means the field has nothing to say here.
func fallbackFor(name string, lay *layout.Layout, pb *probe.Probes) Field {
	switch name {
	case "Project":
		return detectedList(name, pb.Stack)
	case "Layering":
		// meaningful only in a linked worktree; elsewhere there is
		// nothing to layer onto
		if lay.MainRoot == "" || lay.CheckoutRoot == lay.MainRoot {
			return Field{}
		}

		return Field{Name: name, Value: lay.Layering, Origin: OriginDefault}
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
	case "Verify":
		return detectedList(name, pb.Verify)
	case "Visibility":
		return Field{Name: name, Value: "stealth", Origin: OriginDefault}
	case "Storage":
		return storageField(name, lay)
	case "Review":
		// a CI workflow running the Claude Code action is a remote review
		// detection can name; the local half has nothing to infer
		if body := reviewBody(pb); body != "" {
			return Field{Name: name, Value: body, Prose: true, Origin: OriginDetected, Source: pb.Review.Workflow}
		}

		return Field{Name: name, Origin: OriginAbsent}
	default: // Roles, Guardrails: nothing infers them
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
func storageField(name string, lay *layout.Layout) Field {
	for _, l := range lay.Layers {
		if l.Label == "out-of-tree" {
			return Field{
				Name:   name,
				Value:  "out-of-tree (" + lay.StorageRoot + ")",
				Origin: OriginDetected,
				Source: l.Dir,
			}
		}
	}

	return Field{Name: name, Value: "in-repo (" + lay.StorageRoot + ")", Origin: OriginDefault}
}

// parseFile splits one project.md into contract sections. Three shapes open
// one: a markdown heading (`## Tracker`), an unindented bold list item naming
// a contract field (`- **Tracker** - …`), and a plain label naming one
// (`Roles: plan as …`). A bold item the contract does not know stays in the
// open section's body (`- **Fetch:** …` under Tracker, a named pipeline under
// Ship pipelines). Unknown headings are returned separately, not dropped; an
// empty one (a document title) is skipped. data is the file below any
// frontmatter.
func parseFile(data []byte, path, layer string) (map[string]section, []section) {
	boldItem := regexp.MustCompile(`^[-*]\s+\*\*([^*]+)\*\*\s*[-:]?\s*(.*)$`)
	heading := regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
	plainLabel := regexp.MustCompile(`^\s{0,3}([A-Za-z][A-Za-z &()/]{0,30}):\s*(.*)$`)

	fields := map[string]section{}

	var (
		unknown []section
		current *section
	)

	flush := func() {
		if current == nil {
			return
		}

		current.body = normalizeBody(current.body)
		if canonical := canonicalName(current.name); canonical != "" {
			fields[canonical] = *current
		} else if current.body != "" {
			unknown = append(unknown, *current)
		}

		current = nil
	}

	open := func(name, rest string) {
		flush()

		current = &section{name: strings.TrimSpace(name), body: rest, layer: layer, file: path}
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		switch {
		case matchOpener(boldItem, line, func(m []string) bool {
			if canonicalName(m[1]) != "" || current == nil {
				open(m[1], m[2])
				return true
			}
			return false // an unknown bold item continues the open section
		}):
		case matchOpener(heading, line, func(m []string) bool {
			open(m[1], "")
			return true
		}):
		case matchOpener(plainLabel, line, func(m []string) bool {
			if canonicalName(m[1]) == "" {
				return false // an unknown label is ordinary prose
			}
			open(m[1], m[2])
			return true
		}):
		default:
			if current != nil {
				current.body += "\n" + line
			}
		}
	}

	flush()

	return fields, unknown
}

// matchOpener runs the handler on a regexp match and reports whether the
// line was consumed as a section opener.
func matchOpener(re *regexp.Regexp, line string, handle func([]string) bool) bool {
	m := re.FindStringSubmatch(line)

	return m != nil && handle(m)
}

func isProse(value string) bool {
	return strings.Contains(strings.TrimSpace(value), "\n")
}

// normalizeBody reduces declared prose to logical lines: hard-wrapped
// continuations (broken at the author's editor width, which is not a
// clause boundary) rejoin their paragraph or bullet, so renderers wrap to
// the reader's width instead of double-wrapping. A bullet or a blank line
// starts a new logical line; blanks collapse to one paragraph break.
func normalizeBody(body string) string {
	var logical []string

	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			if n := len(logical); n > 0 && logical[n-1] != "" {
				logical = append(logical, "")
			}
		case isBullet(trimmed) || len(logical) == 0 || logical[len(logical)-1] == "":
			logical = append(logical, trimmed)
		default:
			logical[len(logical)-1] += " " + trimmed
		}
	}

	return strings.TrimSpace(strings.Join(logical, "\n"))
}

func isBullet(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
}

// rolesLines reshapes Roles prose so each role sits on its own line:
// the paragraph is unwrapped (declared prose is hard-wrapped at the
// author's editor width, which is not a clause boundary), then split at
// semicolons and at sentence breaks. A value already structured as
// markdown bullets - the contract's Always/Conditional shape - keeps its
// own lines.
func rolesLines(value string) string {
	for line := range strings.SplitSeq(value, "\n") {
		if isBullet(strings.TrimSpace(line)) {
			return value
		}
	}

	unwrapped := strings.Join(strings.Fields(value), " ")
	unwrapped = regexp.MustCompile(`;\s*`).ReplaceAllString(unwrapped, "\n")

	return regexp.MustCompile(`\.\s+([A-Z])`).ReplaceAllString(unwrapped, ".\n$1")
}
