package facts

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// A pace is how fast a project delivers: dials keyed by step, laid over
// whichever flow runs. A pace tunes or skips the entries a flow has and
// never adds one, since adding work is the flow's job.

// PaceFinishKey is the one key that names no flow entry: whether a run that
// would stop at an open PR goes on to finish.
const PaceFinishKey = "finish"

// PaceOriginBuiltin is the origin of a dial f10 ships.
const PaceOriginBuiltin = "built-in"

// Dials is one step's settings under a pace. Each field belongs to one
// step (dialSteps); a zero field sets nothing.
type Dials struct {
	Off    bool   `yaml:"-"`
	Finish string `yaml:"-"` // on or off, the finish key only
	By     string `yaml:"by,omitempty"`
	Rounds int    `yaml:"rounds,omitempty"`
	Depth  string `yaml:"depth,omitempty"`
	Effort string `yaml:"effort,omitempty"`
	Marker string `yaml:"marker,omitempty"`

	word   string // a scalar shorthand no rule read, refused by CheckPace
	mapped bool   // written as a map, which the finish key refuses
}

// dialSteps is the step each dial belongs to, in the order they print.
var dialSteps = []struct{ dial, step string }{
	{"by", "review"},
	{"rounds", "review"},
	{"depth", "review"},
	{"effort", "judge"},
	{"marker", "pr"},
}

// UnmarshalYAML reads a map of dials or one scalar word; which word means
// what depends on the step, so Pace reads the word once it knows the key.
func (d *Dials) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*d = Dials{word: strings.ToLower(strings.TrimSpace(node.Value))}

		return nil
	case yaml.MappingNode:
	default:
		return fmt.Errorf("line %d: a map of dials or one word like off", node.Line)
	}

	// node.Decode keeps no KnownFields, so an unknown dial is refused here
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !slices.ContainsFunc(dialSteps, func(s struct{ dial, step string }) bool { return s.dial == key }) {
			return fmt.Errorf(
				"line %d: %q is not a dial (by, rounds, depth, effort, marker)",
				node.Content[i].Line,
				key,
			)
		}
	}

	type plain Dials

	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}

	*d = Dials(p)
	d.mapped = true

	return nil
}

// Pace is one pace's dials by step key.
type Pace map[string]Dials

// UnmarshalYAML reads each key's shorthand: off for any step, on or off
// for finish, a half for review (`review: local`).
func (p *Pace) UnmarshalYAML(node *yaml.Node) error {
	var raw map[string]Dials
	if err := node.Decode(&raw); err != nil {
		return err
	}

	*p = Pace{}

	for key, d := range raw {
		key = strings.ToLower(strings.TrimSpace(key))

		switch {
		case d.word == "":
		case key == PaceFinishKey && (d.word == "on" || d.word == "off"):
			d = Dials{Finish: d.word}
		case d.word == "off":
			d = Dials{Off: true}
		case key == "review" && d.word != "on":
			d = Dials{By: d.word}
		}

		(*p)[key] = d
	}

	return nil
}

// Shown is the dials as `by local, rounds 2`, or off, or the finish word.
func (d Dials) Shown() string {
	switch {
	case d.Off:
		return "off"
	case d.Finish != "":
		return d.Finish
	}

	var parts []string
	for _, s := range dialSteps {
		if v := d.value(s.dial); v != "" {
			parts = append(parts, s.dial+" "+v)
		}
	}

	return strings.Join(parts, ", ")
}

func (d Dials) value(dial string) string {
	switch dial {
	case "by":
		return d.By
	case "rounds":
		if d.Rounds != 0 {
			return strconv.Itoa(d.Rounds)
		}
	case "depth":
		return d.Depth
	case "effort":
		return d.Effort
	case "marker":
		return d.Marker
	}

	return ""
}

// CheckPace reports what is wrong with one pace, each line naming
// paces.<name>.<key>.
func CheckPace(name string, p Pace) []string {
	var bad []string

	if !flowNameRE.MatchString(name) {
		bad = append(bad, fmt.Sprintf("paces: %q is not a lowercase name", name))
	}

	for _, key := range slices.Sorted(maps.Keys(p)) {
		d, at := p[key], "paces."+name+"."+key

		switch {
		case key == PaceFinishKey:
			if d.Finish == "" || d.mapped {
				bad = append(bad, at+": finish is on or off")
			}

			continue
		case key == "catchup":
			bad = append(bad, at+": catchup is never a flow entry; the catchup key picks merge, rebase or off")

			continue
		case !stepNameRE.MatchString(key):
			bad = append(bad, fmt.Sprintf("%s: %q is not a step name", at, key))

			continue
		case d.word != "":
			bad = append(bad, fmt.Sprintf("%s: %q is not a shorthand; write off, or a map of dials", at, d.word))

			continue
		case d.Off && key == "implement":
			bad = append(bad, at+": implement cannot be off, since a flow without it does no work")

			continue
		}

		for _, s := range dialSteps {
			if d.value(s.dial) != "" && s.step != key {
				bad = append(bad, fmt.Sprintf("%s: %s is a %s dial", at, s.dial, s.step))
			}
		}

		bad = append(bad, d.checkValues(at)...)
	}

	return bad
}

func (d Dials) checkValues(at string) []string {
	var bad []string

	switch {
	case strings.Contains(d.By, "?"):
		bad = append(bad, fmt.Sprintf("%s: by %s: an optional half (?) is not supported yet", at, d.By))
	case d.By != "" && !anyHalf(d.By):
		bad = append(bad, fmt.Sprintf("%s: by %s is not local, host, ci or local+ci", at, d.By))
	}

	if d.Rounds != 0 && d.Rounds != 1 && d.Rounds != 2 {
		bad = append(bad, fmt.Sprintf("%s: rounds %d is not 1 or 2", at, d.Rounds))
	}

	if d.Depth != "" && d.Depth != "full" && d.Depth != "light" {
		bad = append(bad, fmt.Sprintf("%s: depth %s is not full or light", at, d.Depth))
	}

	if d.Effort != "" {
		prof, err := agentctl.ParseProfile(d.Effort)
		if err == nil && (prof.Effort == "" || prof.Model != "" || prof.Kind != "") {
			err = fmt.Errorf("%q is not an effort word", d.Effort)
		}

		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: effort %v", at, err))
		}
	}

	return bad
}

// BuiltinPaces are the paces f10 ships. medium is empty: naming it runs
// every flow as declared, the way to switch a faster default off.
func BuiltinPaces() map[string]Pace {
	return map[string]Pace{
		"slow": {
			"review":      {By: "local", Rounds: 2},
			PaceFinishKey: {Finish: "off"},
		},
		"medium": {},
		"fast": {
			"review":      {By: "local", Rounds: 1},
			"judge":       {Effort: "low"},
			PaceFinishKey: {Finish: "on"},
		},
		"ultra": {
			"plan":        {Off: true},
			"judge":       {Off: true},
			"cleanup":     {Off: true},
			"demo":        {Off: true},
			"review":      {By: "local", Depth: "light"},
			"pr":          {Marker: "fast"},
			PaceFinishKey: {Finish: "on"},
		},
	}
}

// PaceSet is every pace a run can name, merged key by key: the built-ins,
// the user's ~/.f10/paces.md, then the project's paces.
type PaceSet struct {
	Paces         map[string]Pace
	Origins       map[string]string // <pace>.<key> to the layer that set it
	Default       string            // pace: the pace a run takes when it names none; "" for none
	DefaultOrigin string
}

// NewPaceSet is the built-ins alone.
func NewPaceSet() *PaceSet {
	s := &PaceSet{Paces: map[string]Pace{}, Origins: map[string]string{}}
	s.Over(BuiltinPaces(), func(string, string) string { return PaceOriginBuiltin })

	return s
}

// Over lays paces on the set, key by key: a layer tuning fast's review
// keeps the lower layer's judge. An empty pace still declares its name.
func (s *PaceSet) Over(paces map[string]Pace, origin func(name, key string) string) {
	for name, p := range paces {
		if s.Paces[name] == nil {
			s.Paces[name] = Pace{}
		}

		for key, d := range p {
			s.Paces[name][key] = d
			s.Origins[name+"."+key] = origin(name, key)
		}
	}
}

// Names is every pace, sorted.
func (s *PaceSet) Names() []string { return slices.Sorted(maps.Keys(s.Paces)) }

// Lookup is the named pace, and an error listing the names when none is.
func (s *PaceSet) Lookup(name string) (Pace, error) {
	if p, ok := s.Paces[name]; ok {
		return p, nil
	}

	return nil, fmt.Errorf("no pace %q; the paces are %s (f10 config lists their dials)",
		name, strings.Join(s.Names(), ", "))
}

// A Dial is one resolved setting on an entry, with where it came from.
type Dial struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// A ResolvedEntry is one flow entry as a pace leaves it.
type ResolvedEntry struct {
	Entry    string `json:"entry"` // as it runs: review:local when the pace named the half
	Declared string `json:"declared"`
	Step     string `json:"step"`
	Skipped  string `json:"skipped,omitempty"` // the origin of the off that skips it; "" when it runs
	Dials    []Dial `json:"dials,omitempty"`
}

// Resolved is a flow under a pace.
type Resolved struct {
	Flow    string          `json:"flow"`
	Pace    string          `json:"pace,omitempty"`
	Entries []ResolvedEntry `json:"entries"`
	Finish  *Dial           `json:"finish,omitempty"` // set only when the pace says and the run ends at a PR
}

// Run is the entries that run, in order.
func (r Resolved) Run() []string {
	var run []string

	for _, e := range r.Entries {
		if e.Skipped == "" {
			run = append(run, e.Entry)
		}
	}

	return run
}

// deliveringSteps are the generic steps whose output is a run's shipment.
var deliveringSteps = []string{"implement", "commit", "push", "pr", "deploy"}

// ResolveFlow lays the named pace on a flow. An entry argument beats the
// pace for the dial it expresses (review:local fixes by), and the pace
// beats the step's own default. No pace ("") leaves the flow as declared.
func (s *PaceSet) ResolveFlow(flowName string, flow []string, pace string) (Resolved, error) {
	r := Resolved{Flow: flowName, Pace: pace}

	var p Pace

	if pace != "" {
		var err error
		if p, err = s.Lookup(pace); err != nil {
			return r, err
		}
	}

	from := func(key string) string { return "pace " + pace + " (" + s.Origins[pace+"."+key] + ")" }

	for _, entry := range flow {
		step, arg := SplitEntry(entry)
		e := ResolvedEntry{Entry: entry, Declared: entry, Step: step}
		d, ok := p[step]

		switch {
		case !ok:
		case d.Off:
			e.Skipped = from(step)
		default:
			for _, ds := range dialSteps {
				v := d.value(ds.dial)
				if v == "" || ds.step != step {
					continue
				}

				if ds.dial == "by" {
					if arg != "" {
						continue // the entry's argument names the half
					}

					e.Entry = step + ":" + v
				}

				e.Dials = append(e.Dials, Dial{Name: ds.dial, Value: v, Origin: from(step)})
			}
		}

		if step == "review" && arg != "" && ok && d.By != "" && !d.Off {
			e.Dials = append([]Dial{{Name: "by", Value: arg, Origin: "entry argument"}}, e.Dials...)
		}

		r.Entries = append(r.Entries, e)
	}

	run := r.Run()
	if len(run) == 0 {
		return r, fmt.Errorf("pace %s skips every entry of the %s flow", pace, flowName)
	}

	if bad := checkFlow(flowName, run); len(bad) > 0 {
		return r, fmt.Errorf("pace %s on the %s flow: %s", pace, flowName, strings.Join(bad, "; "))
	}

	if d, ok := p[PaceFinishKey]; ok && d.Finish != "" && endsAtPR(run) {
		r.Finish = &Dial{Name: PaceFinishKey, Value: d.Finish, Origin: from(PaceFinishKey)}
	}

	return r, nil
}

// endsAtPR reports a run whose last delivering entry is pr: one that would
// otherwise stop at an open PR.
func endsAtPR(run []string) bool {
	last := ""

	for _, entry := range run {
		if step, _ := SplitEntry(entry); slices.Contains(deliveringSteps, step) {
			last = step
		}
	}

	return last == "pr"
}

// PaceProblems resolves every flow under every pace and reports each pace
// that breaks one, so a broken pace shows before a run picks it.
func (s *PaceSet) PaceProblems(flows map[string][]string) []string {
	var bad []string

	for _, pace := range s.Names() {
		for _, name := range slices.Sorted(maps.Keys(flows)) {
			if _, err := s.ResolveFlow(name, flows[name], pace); err != nil {
				bad = append(bad, err.Error())
			}
		}
	}

	return bad
}

// pacesShown is each pace's keys as `fast: review (by local, rounds 1), finish (on)`.
func pacesShown(paces map[string]Pace) string {
	var shown []string

	for _, name := range slices.Sorted(maps.Keys(paces)) {
		var keys []string
		for _, key := range slices.Sorted(maps.Keys(paces[name])) {
			keys = append(keys, key+" ("+paces[name][key].Shown()+")")
		}

		shown = append(shown, name+": "+strings.Join(keys, ", "))
	}

	return strings.Join(shown, "; ")
}

// resolvePaces records the project's paces and its default pace. The
// user's layer and the built-ins are the caller's to lay under them.
func (e *Effective) resolvePaces(fm *layered) {
	if fm.Paces != nil {
		e.Paces = fm.Paces
		e.PacesOrigin = map[string]string{}
		layers := map[string]bool{}

		for name, p := range fm.Paces {
			for key := range p {
				o := fm.origin["paces."+name+"."+key]
				e.PacesOrigin[name+"."+key] = "declared (" + o + ", frontmatter)"
				layers[o] = true
			}
		}

		e.Facts = append(e.Facts, Fact{
			Key:    "paces",
			Value:  pacesShown(e.Paces),
			Origin: "declared (" + strings.Join(slices.Sorted(maps.Keys(layers)), " + ") + ", frontmatter)",
		})

		// the user's file may change these paces, so a run reports the
		// final word; this catches the project's own mistakes early
		s := NewPaceSet()
		s.Over(e.Paces, func(name, key string) string { return e.PacesOrigin[name+"."+key] })

		for _, bad := range s.PaceProblems(e.Flows) {
			e.problem(false, "%s", bad)
		}
	}

	if fm.Pace != "" {
		e.Pace = fm.Pace
		e.PaceOrigin = "declared (" + fm.origin["pace"] + ", frontmatter)"
		e.Facts = append(e.Facts, Fact{Key: "pace", Value: e.Pace, Origin: e.PaceOrigin})
	}
}
