package facts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// OriginKind is where an effective value came from.
type OriginKind string

// The kinds an origin can have.
const (
	OriginDeclared OriginKind = "declared"
	OriginDetected OriginKind = "detected"
	OriginDefault  OriginKind = "default"
	OriginAbsent   OriginKind = "absent"
	// OriginJudgment is guidance the probe could not turn into commands
	OriginJudgment OriginKind = "judgment"
	// OriginDerived is a task id prefix taken from the project's name
	OriginDerived OriginKind = "derived"
	// OriginBuiltin is a pace dial f10 ships
	OriginBuiltin OriginKind = "built-in"
)

// An Origin is an effective value's provenance. Readers ask it what it
// means; only String formats it, so printers and JSON show one wording.
type Origin struct {
	Kind        OriginKind
	Layer       string // declared: "main", "worktree", "user", or "main + worktree"
	Detail      string // declared: the file a user layer came from; derived: the project's name
	Frontmatter bool   // declared in project.md's frontmatter, not its prose
}

// OriginOf is an origin with no layer: detected, default, judgment, absent.
func OriginOf(kind OriginKind) Origin { return Origin{Kind: kind} }

// DeclaredIn is a prose section a layer's project.md wrote.
func DeclaredIn(layer string) Origin { return Origin{Kind: OriginDeclared, Layer: layer} }

// DeclaredFrontmatter is a key a layer's frontmatter set.
func DeclaredFrontmatter(layer string) Origin {
	return Origin{Kind: OriginDeclared, Layer: layer, Frontmatter: true}
}

// DeclaredByUser is a value the user's own file at path set.
func DeclaredByUser(path string) Origin {
	return Origin{Kind: OriginDeclared, Layer: "user", Detail: path}
}

// DerivedFrom is a task id prefix the project's name gave.
func DerivedFrom(project string) Origin { return Origin{Kind: OriginDerived, Detail: project} }

// declaredAcross is a frontmatter value several layers each set part of,
// such as a map merged key by key.
func declaredAcross(layers map[string]bool) Origin {
	return DeclaredFrontmatter(strings.Join(slices.Sorted(maps.Keys(layers)), " + "))
}

// Declared says a project.md layer or the user's file set the value.
func (o Origin) Declared() bool { return o.Kind == OriginDeclared }

func (o Origin) String() string {
	switch o.Kind {
	case OriginDeclared:
		parts := []string{o.Layer}
		if o.Detail != "" {
			parts = append(parts, o.Detail)
		}

		if o.Frontmatter {
			parts = append(parts, "frontmatter")
		}

		return "declared (" + strings.Join(parts, ", ") + ")"
	case OriginDerived:
		return "derived from project name " + o.Detail
	default:
		return string(o.Kind)
	}
}

// MarshalText keeps the JSON an origin's printed form.
func (o Origin) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

// UnmarshalText reads String's form back, for JSON that f10 itself wrote.
func (o *Origin) UnmarshalText(text []byte) error {
	s := string(text)

	switch {
	case strings.HasPrefix(s, "declared (") && strings.HasSuffix(s, ")"):
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(s, "declared ("), ")"), ", ")
		*o = Origin{Kind: OriginDeclared, Layer: parts[0]}

		if parts = parts[1:]; len(parts) > 0 && parts[len(parts)-1] == "frontmatter" {
			o.Frontmatter, parts = true, parts[:len(parts)-1]
		}

		o.Detail = strings.Join(parts, ", ")
	case strings.HasPrefix(s, "derived from project name "):
		*o = DerivedFrom(strings.TrimPrefix(s, "derived from project name "))
	default:
		*o = OriginOf(OriginKind(s))
	}

	return nil
}

// TrackerKind is the tracker a project's tasks live in, as the frontmatter
// names it; String gives the name the rest of f10 shows.
type TrackerKind string

// The trackers f10 knows.
const (
	TrackerGitHub TrackerKind = "github"
	TrackerGitLab TrackerKind = "gitlab"
	TrackerNotion TrackerKind = "notion"
	TrackerJira   TrackerKind = "jira"
)

// TrackerKinds lists the kinds in the order the vocabulary shows them.
var TrackerKinds = []TrackerKind{TrackerGitHub, TrackerGitLab, TrackerNotion, TrackerJira}

var trackerNames = map[TrackerKind]string{
	TrackerGitHub: "GitHub Issues",
	TrackerGitLab: "GitLab work items",
	TrackerNotion: "Notion",
	TrackerJira:   "Jira",
}

func (k TrackerKind) String() string { return trackerNames[k] }

// HostIssues says the tasks are the code host's own issues, which the host
// CLI reads without a driver.
func (k TrackerKind) HostIssues() bool { return k == TrackerGitHub || k == TrackerGitLab }

// MarshalText keeps the JSON the shown name, as config and standup print it.
func (k TrackerKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText reads the shown name back, or the frontmatter word.
func (k *TrackerKind) UnmarshalText(text []byte) error {
	for kind, name := range trackerNames {
		if string(text) == name || string(text) == string(kind) {
			*k = kind

			return nil
		}
	}

	if len(text) > 0 {
		return fmt.Errorf("%q is not a tracker kind", text)
	}

	*k = ""

	return nil
}

// StaleAfter is the age past which a task is judged before it is planned;
// zero is off.
type StaleAfter time.Duration

const week = 7 * 24 * time.Hour

// String is the value in the frontmatter's form: whole weeks as `3w`,
// anything else as days.
func (s StaleAfter) String() string {
	d := time.Duration(s)

	switch {
	case d <= 0:
		return ""
	case d%week == 0:
		return fmt.Sprintf("%dw", d/week)
	default:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
}

// MarshalText keeps the JSON the frontmatter's form.
func (s StaleAfter) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads the frontmatter's form.
func (s *StaleAfter) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*s = 0

		return nil
	}

	d, err := ParseStaleAfter(string(text))
	*s = StaleAfter(d)

	return err
}
