package facts

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/flow"
)

// A pace is how fast a project delivers: dials keyed by step, laid over
// whichever flow runs. A pace tunes or skips the entries a flow has and
// never adds one, since adding work is the flow's job.

// PaceFinishKey is the key, written beside the step keys, that names no
// flow entry: whether a run that would stop at an open PR goes on to finish.
const PaceFinishKey = "finish"

// PaceOriginBuiltin is the origin of a dial f10 ships.
var PaceOriginBuiltin = OriginOf(OriginBuiltin)

// Dials is one step's settings under a pace. Each field belongs to the step
// its row in dials names; a zero field sets nothing.
type Dials struct {
	Off    bool   `yaml:"-"`
	On     bool   `yaml:"-"` // a ceiling's word: the step runs under every pace
	By     string `yaml:"by,omitempty"`
	Rounds int    `yaml:"rounds,omitempty"`
	Depth  string `yaml:"depth,omitempty"`
	Effort string `yaml:"effort,omitempty"`
	Marker string `yaml:"marker,omitempty"`

	word   string // a scalar shorthand no rule read, refused by the checks
	mapped bool   // written as a map, which the finish key refuses
}

// A dialSpec is everything one dial means, so reading, checking, capping
// and resolving walk one table instead of a switch each.
type dialSpec struct {
	name, step string
	get        func(Dials) string
	set        func(*Dials, string)
	check      func(string) string // why a value is refused; "" when it reads
	order      []string            // fast to slow; nil leaves it uncappable
	silent     string              // the value a cap reads an unset dial as; "" leaves it uncapped
	agent      bool                // laid on the agents map, not on the flow entry
}

// dials is every dial, in the order they print.
var dials = []dialSpec{
	{
		name: "by", step: "review",
		get: func(d Dials) string { return d.By }, set: func(d *Dials, v string) { d.By = v },
		check: func(v string) string {
			if validBy(v) {
				return ""
			}

			return "by " + v + " is not local, host, ci, local+ci, ci? or local+ci?"
		},
	},
	{
		name: "rounds", step: "review",
		get: func(d Dials) string {
			if d.Rounds == 0 {
				return ""
			}

			return strconv.Itoa(d.Rounds)
		},
		set: func(d *Dials, v string) { d.Rounds, _ = strconv.Atoi(v) },
		check: func(v string) string {
			if v == "1" || v == "2" {
				return ""
			}

			return "rounds " + v + " is not 1 or 2"
		},
		order:  []string{"1", "2"},
		silent: "1", // an unset review runs one round, the fast end
	},
	{
		name: "depth", step: "review",
		get: func(d Dials) string { return d.Depth }, set: func(d *Dials, v string) { d.Depth = v },
		check: func(v string) string {
			if v == "full" || v == "light" {
				return ""
			}

			return "depth " + v + " is not full or light"
		},
		order: []string{"light", "full"},
	},
	{
		name: "effort", step: "judge",
		get: func(d Dials) string { return d.Effort }, set: func(d *Dials, v string) { d.Effort = v },
		check: func(v string) string {
			prof, err := agentctl.ParseProfile(v)
			if err == nil && (prof.Effort == "" || prof.Model != "" || prof.Kind != "") {
				err = fmt.Errorf("%q is not an effort word", v)
			}

			if err != nil {
				return fmt.Sprintf("effort %v", err)
			}

			return ""
		},
		// an unset effort keeps the agents map's, the project's own choice
		order: agentctl.Kinds[agentctl.DefaultKind].Efforts,
		agent: true,
	},
	{
		name: "marker", step: "pr",
		get: func(d Dials) string { return d.Marker }, set: func(d *Dials, v string) { d.Marker = v },
		check: func(string) string { return "" },
	},
}

func dialNames() []string {
	names := make([]string, len(dials))
	for i, s := range dials {
		names[i] = s.name
	}

	return names
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
		if !slices.Contains(dialNames(), key) {
			return fmt.Errorf(
				"line %d: %q is not a dial (%s)",
				node.Content[i].Line,
				key,
				strings.Join(dialNames(), ", "),
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

// MarshalYAML writes the shorthand a word stands for, so a migrated off
// reads back as it was written.
func (d Dials) MarshalYAML() (any, error) {
	switch {
	case d.Off:
		return "off", nil
	case d.On:
		return "on", nil
	}

	type plain Dials

	return plain(d), nil
}

// Shown is the dials as `by local, rounds 2`, or off or on.
func (d Dials) Shown() string {
	switch {
	case d.Off:
		return "off"
	case d.On:
		return "on"
	}

	var parts []string
	for _, s := range dials {
		if v := s.get(d); v != "" {
			parts = append(parts, s.name+" "+v)
		}
	}

	return strings.Join(parts, ", ")
}

// Pace is one pace: dials by step, and the finish word beside them.
type Pace struct {
	Steps  map[string]Dials
	Finish string // on or off; "" says nothing

	finishBad bool // finish was written as something other than on or off
}

// UnmarshalYAML reads each key's shorthand: off for any step, a half for
// review (`review: local`), and on or off for finish.
func (p *Pace) UnmarshalYAML(node *yaml.Node) error {
	var raw map[string]Dials
	if err := node.Decode(&raw); err != nil {
		return err
	}

	*p = Pace{Steps: map[string]Dials{}}

	for key, d := range raw {
		key = strings.ToLower(strings.TrimSpace(key))

		if key == PaceFinishKey {
			if d.mapped || (d.word != "on" && d.word != "off") {
				p.finishBad = true
			} else {
				p.Finish = d.word
			}

			continue
		}

		switch {
		case d.word == "":
		case d.word == "off":
			d = Dials{Off: true}
		case d.word == "on":
			d = Dials{On: true}
		case key == "review":
			d = Dials{By: d.word}
		}

		p.Steps[key] = d
	}

	return nil
}

// MarshalYAML writes the steps and finish side by side, as they are read.
func (p Pace) MarshalYAML() (any, error) {
	out := map[string]any{}
	for key, d := range p.Steps {
		out[key] = d
	}

	if p.Finish != "" {
		out[PaceFinishKey] = p.Finish
	}

	return out, nil
}

// Keys is every key the pace sets, finish among the steps, sorted.
func (p Pace) Keys() []string {
	keys := slices.Collect(maps.Keys(p.Steps))
	if p.written() {
		keys = append(keys, PaceFinishKey)
	}

	slices.Sort(keys)

	return keys
}

// Shown is one key's value as the pace states it.
func (p Pace) Shown(key string) string {
	if key == PaceFinishKey {
		return p.Finish
	}

	return p.Steps[key].Shown()
}

// Empty reports a pace that sets nothing.
func (p Pace) Empty() bool { return len(p.Steps) == 0 && !p.written() }

// written reports a finish key the pace was given, well formed or not.
func (p Pace) written() bool { return p.Finish != "" || p.finishBad }

func (p Pace) clone() Pace {
	c := p
	c.Steps = maps.Clone(p.Steps)

	if c.Steps == nil {
		c.Steps = map[string]Dials{}
	}

	return c
}

// keyRules is what a pace and a ceiling check differently.
type keyRules struct {
	catchup  string                     // why the catchup key is refused
	word     string                     // how a stray shorthand is corrected
	refuse   func(string, Dials) string // a step's dials only this one refuses
	ordered  bool                       // a cap needs an order from fast to slow
	location string                     // paces.<name>. or ceiling.
}

// checkKeys reports what is wrong with each key of p, each line naming its
// location.
func checkKeys(p Pace, r keyRules) []string {
	var bad []string

	for _, key := range p.Keys() {
		at := r.location + key

		if key == PaceFinishKey {
			if p.finishBad || (p.Finish != "on" && p.Finish != "off") {
				bad = append(bad, at+": finish is on or off")
			}

			continue
		}

		d := p.Steps[key]

		switch {
		case key == "catchup":
			bad = append(bad, at+": "+r.catchup)

			continue
		case !stepNameRE.MatchString(key):
			bad = append(bad, fmt.Sprintf("%s: %q is not a step name", at, key))

			continue
		case d.word != "":
			bad = append(bad, fmt.Sprintf("%s: %q is not a shorthand; %s", at, d.word, r.word))

			continue
		}

		if why := r.refuse(key, d); why != "" {
			bad = append(bad, at+": "+why)

			continue
		}

		if r.ordered {
			for _, s := range dials {
				if s.order == nil && s.get(d) != "" {
					bad = append(bad,
						fmt.Sprintf("%s: %s has no order from fast to slow, so a ceiling cannot cap it", at, s.name))
				}
			}
		}

		for _, s := range dials {
			if s.get(d) != "" && s.step != key {
				bad = append(bad, fmt.Sprintf("%s: %s is a %s dial", at, s.name, s.step))
			}
		}

		for _, s := range dials {
			if v := s.get(d); v != "" {
				if why := s.check(v); why != "" {
					bad = append(bad, at+": "+why)
				}
			}
		}
	}

	return bad
}

// CheckPace reports what is wrong with one pace, each line naming
// paces.<name>.<key>.
func CheckPace(name string, p Pace) []string {
	var bad []string

	if !flowNameRE.MatchString(name) {
		bad = append(bad, fmt.Sprintf("paces: %q is not a lowercase name", name))
	}

	return append(bad, checkKeys(p, keyRules{
		location: "paces." + name + ".",
		catchup:  "catchup is never a flow entry; the catchup key picks merge, rebase or off",
		word:     "write off, or a map of dials",
		refuse: func(key string, d Dials) string {
			switch {
			case d.On:
				return "on is a ceiling's word; a pace runs every entry it does not turn off"
			case d.Off && key == "implement":
				return "implement cannot be off, since a flow without it does no work"
			}

			return ""
		},
	})...)
}

// A Ceiling caps every pace but the ones Except names: a dial the pace
// sets faster than the ceiling's takes the ceiling's value. It is the
// project's alone, since a cap a user sets on themselves is just a pace.
type Ceiling struct {
	Pace   Pace     `yaml:"-"` // read and written by the methods below
	Except []string `yaml:"-"`
}

// UnmarshalYAML takes except out and reads the rest as a pace, so every
// shorthand reads the same way in both.
func (c *Ceiling) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: ceiling is a map of dials", node.Line)
	}

	rest := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "except" {
			rest.Content = append(rest.Content, node.Content[i], node.Content[i+1])

			continue
		}

		value := node.Content[i+1]
		if value.Kind == yaml.ScalarNode {
			c.Except = []string{value.Value}

			continue
		}

		if err := value.Decode(&c.Except); err != nil {
			return fmt.Errorf("line %d: ceiling.except is a list of pace names", value.Line)
		}
	}

	return rest.Decode(&c.Pace)
}

// MarshalYAML writes the dials and except side by side, as they are read.
func (c Ceiling) MarshalYAML() (any, error) {
	out := map[string]any{}
	for key, d := range c.Pace.Steps {
		out[key] = d
	}

	if c.Pace.Finish != "" {
		out[PaceFinishKey] = c.Pace.Finish
	}

	if len(c.Except) > 0 {
		out["except"] = c.Except
	}

	return out, nil
}

// CheckCeiling reports what is wrong with a ceiling, each line naming
// ceiling.<key>. A cap needs an order from fast to slow, so off and the
// dials without one are refused.
func CheckCeiling(c Ceiling) []string {
	bad := checkKeys(c.Pace, keyRules{
		location: "ceiling.",
		catchup:  "catchup is never a flow entry",
		word:     "write on, or a map of dials",
		refuse: func(_ string, d Dials) string {
			if d.Off {
				return "a ceiling never skips a step; write on to keep it running under every pace"
			}

			return ""
		},
		ordered: true,
	})

	for _, name := range c.Except {
		if !flowNameRE.MatchString(name) {
			bad = append(bad, fmt.Sprintf("ceiling.except: %q is not a lowercase name", name))
		}
	}

	return bad
}

// BuiltinPaces are the paces f10 ships. medium only finishes: naming it
// runs every flow as declared, the way to switch a faster default off.
func BuiltinPaces() map[string]Pace {
	return map[string]Pace{
		"slow": {
			Steps:  map[string]Dials{"review": {By: "local+ci?", Rounds: 2}},
			Finish: "off",
		},
		"medium": {
			Steps:  map[string]Dials{},
			Finish: "on",
		},
		"fast": {
			Steps: map[string]Dials{
				"review": {By: "local", Rounds: 1},
				"judge":  {Effort: "low"},
			},
			Finish: "on",
		},
		"ultra": {
			Steps: map[string]Dials{
				"plan":    {Off: true},
				"judge":   {Off: true},
				"cleanup": {Off: true},
				"demo":    {Off: true},
				"review":  {By: "local", Depth: "light"},
				"pr":      {Marker: "fast"},
			},
			Finish: "on",
		},
	}
}

// PaceSet is every pace a run can name, merged key by key: the built-ins,
// the user's ~/.f10/paces.md, then the project's paces.
type PaceSet struct {
	Remote        bool // Review declares a remote reviewer, which an optional CI review needs
	Paces         map[string]Pace
	Origins       map[string]Origin // <pace> and <pace>.<key> to the layer that set it
	Default       string            // pace: the pace a run takes when it names none; "" for none
	DefaultOrigin Origin

	Ceiling        Pace              // the project's cap on every pace Except does not name
	CeilingOrigins map[string]Origin // <key> to the layer that set it
	Except         []string
	ExceptOrigin   Origin
}

// NewPaceSet is the built-ins alone.
func NewPaceSet() *PaceSet {
	s := &PaceSet{Paces: map[string]Pace{}, Origins: map[string]Origin{}}
	s.Over(BuiltinPaces(), func(string, string) Origin { return PaceOriginBuiltin })

	return s
}

// Over lays paces on the set, key by key: a layer tuning fast's review
// keeps the lower layer's judge. An empty pace still declares its name.
func (s *PaceSet) Over(paces map[string]Pace, origin func(name, key string) Origin) {
	for name, p := range paces {
		cur, ok := s.Paces[name]
		if !ok {
			cur = Pace{Steps: map[string]Dials{}}
			s.Origins[name] = origin(name, "")
		}

		cur = cur.clone()

		for key, d := range p.Steps {
			cur.Steps[key] = d
			s.Origins[name+"."+key] = origin(name, key)
		}

		if p.Finish != "" {
			cur.Finish = p.Finish
			s.Origins[name+"."+PaceFinishKey] = origin(name, PaceFinishKey)
		}

		s.Paces[name] = cur
	}
}

func dialRank(s dialSpec, v string) int { return slices.Index(s.order, v) }

// Exempt says whether the ceiling leaves the named pace alone.
func (s *PaceSet) Exempt(name string) bool {
	return s.Ceiling.Empty() || slices.Contains(s.Except, name)
}

// CeilingOrigin is how a dial the ceiling set names its source.
func (s *PaceSet) CeilingOrigin(key string) string {
	return "ceiling (" + s.CeilingOrigins[key].String() + ")"
}

// Capped is the pace with the ceiling laid on, and each replaced dial keyed
// `<step>.<dial>` (bare finish). Silence on finish or rounds is the faster
// default, so it is capped; silence on effort leaves the agents map alone.
func (s *PaceSet) Capped(name string) (Pace, map[string]Dial) {
	src := s.Paces[name]
	if s.Exempt(name) {
		return src, nil
	}

	p := src.clone()
	caps := map[string]Dial{}
	said := func(v string) string {
		who := "pace " + name
		if name == "" {
			who = "no pace"
		}

		if v == "" {
			return who + " left it unset"
		}

		return who + " said " + v
	}

	if s.Ceiling.Finish == "off" && p.Finish != "off" {
		origin := s.CeilingOrigin(PaceFinishKey)
		caps[PaceFinishKey] = Dial{Name: PaceFinishKey, Value: "off", Origin: origin, Capped: said(p.Finish)}
		p.Finish = "off"
	}

	for key, c := range s.Ceiling.Steps {
		d, origin := p.Steps[key], s.CeilingOrigin(key)
		before := d

		if c.On && d.Off {
			caps[key+".run"] = Dial{Name: "run", Value: "on", Origin: origin, Capped: said("off")}
			d.Off = false
		}

		for _, spec := range dials {
			limit, cur := spec.get(c), spec.get(d)
			if limit == "" || spec.order == nil || d.Off {
				continue
			}

			if cur == "" && spec.silent == "" {
				continue
			}

			if dialRank(spec, cmp.Or(cur, spec.silent)) < dialRank(spec, limit) {
				caps[key+"."+spec.name] = Dial{Name: spec.name, Value: limit, Origin: origin, Capped: said(cur)}
				spec.set(&d, limit)
			}
		}

		if d != before {
			p.Steps[key] = d
		}
	}

	return p, caps
}

// Names is every pace, sorted.
func (s *PaceSet) Names() []string { return slices.Sorted(maps.Keys(s.Paces)) }

// Lookup is the named pace, and an error listing the names when none is.
func (s *PaceSet) Lookup(name string) (Pace, error) {
	if p, ok := s.Paces[name]; ok {
		return p, nil
	}

	return Pace{}, fmt.Errorf("no pace %q; the paces are %s (f10 config lists their dials)",
		name, strings.Join(s.Names(), ", "))
}

// A Dial is one resolved setting on an entry, with where it came from.
type Dial struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Origin string `json:"origin"`
	Capped string `json:"capped,omitempty"` // what the pace had before the ceiling replaced it
}

// A ResolvedEntry is one flow entry as a pace leaves it.
type ResolvedEntry struct {
	Entry    string `json:"entry"` // as it runs: review:local when the pace named who reviews
	Declared string `json:"declared"`
	Step     string `json:"step"`
	Label    string `json:"label,omitempty"`   // the entry's display name, from its [label]
	Skipped  string `json:"skipped,omitempty"` // why it does not run: the pace's off, or an optional CI review dropped; "" when it runs
	Dials    []Dial `json:"dials,omitempty"`
}

// Resolved is a flow under a pace.
type Resolved struct {
	Flow    string          `json:"flow"`
	Pace    string          `json:"pace,omitempty"`
	Entries []ResolvedEntry `json:"entries"`
	Finish  *Dial           `json:"finish,omitempty"` // set only when the pace says and the run ends at a PR
}

// Changed reports a resolution that differs from the declared flow.
func (r Resolved) Changed() bool {
	return slices.ContainsFunc(r.Entries, func(e ResolvedEntry) bool {
		return e.Skipped != "" || e.Entry != e.Declared || len(e.Dials) > 0
	})
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

// RunFlow is the entries that run, each with its label.
func (r Resolved) RunFlow() flow.Flow {
	var run flow.Flow

	for _, e := range r.Entries {
		if e.Skipped == "" {
			run = append(run, e.Parsed())
		}
	}

	return run
}

// Labeled is the entries that run as written, each with its label: the
// list /f10:ship runs and passes to its badge.
func (r Resolved) Labeled() []string { return r.RunFlow().Written() }

// Parsed is the entry as it runs, with its label.
func (e ResolvedEntry) Parsed() flow.Entry {
	p := flow.Parse(e.Entry)
	p.Label = e.Label

	return p
}

// handoffStep is the step whose pace key also reaches every pr entry.
const handoffStep = "handoff"

// ResolveFlow lays the named pace on a flow. An entry argument beats the
// pace for the dial it expresses (review:local fixes by), and the pace
// beats the step's own default. No pace ("") leaves the flow as declared, an optional CI review settled.
func (s *PaceSet) ResolveFlow(flowName string, declared flow.Flow, pace string) (Resolved, error) {
	r := Resolved{Flow: flowName, Pace: pace}

	if pace != "" {
		if _, err := s.Lookup(pace); err != nil {
			return r, err
		}
	}

	p, caps := s.Capped(pace)

	from := func(key string) string { return "pace " + pace + " (" + s.Origins[pace+"."+key].String() + ")" }
	dial := func(key, name, value string) Dial {
		if c, ok := caps[key+"."+name]; ok {
			return c
		}

		return Dial{Name: name, Value: value, Origin: from(key)}
	}

	for _, entry := range declared {
		step, arg := entry.Step, entry.Arg
		e := ResolvedEntry{Entry: entry.String(), Declared: entry.String(), Step: step, Label: entry.Label}
		d, ok := p.Steps[step]

		if c, unskipped := caps[step+".run"]; unskipped {
			e.Dials = append(e.Dials, c)
		}

		switch {
		case !ok:
		case d.Off:
			e.Skipped = "off in " + from(step)
		default:
			for _, spec := range dials {
				v := spec.get(d)
				if v == "" || spec.step != step || spec.agent {
					continue // an agent dial reaches the step through its profile (f10 agents)
				}

				if spec.name == "by" {
					if arg != "" {
						continue // the entry's argument names the half
					}

					e.Entry = step + ":" + v
				}

				e.Dials = append(e.Dials, dial(step, spec.name, v))
			}
		}

		if step == "review" && arg != "" && ok && d.By != "" && !d.Off {
			e.Dials = append([]Dial{{Name: "by", Value: arg, Origin: "entry argument"}}, e.Dials...)
		}

		if step == "review" && e.Skipped == "" {
			s.settleCI(&e, published(r.Entries))
		}

		// The pr step runs the handoff itself, so it learns handoff: off from a
		// dial; the dial table cannot carry it, since it reads another step's key.
		if h := p.Steps[handoffStep]; step == "pr" && e.Skipped == "" && h.Off {
			e.Dials = append(e.Dials, Dial{Name: handoffStep, Value: "off", Origin: from(handoffStep)})
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

	if p.Finish != "" && flow.Of(run).Delivered() == "pr" {
		fin := Dial{Name: PaceFinishKey, Value: p.Finish, Origin: from(PaceFinishKey)}
		if c, ok := caps[PaceFinishKey]; ok {
			fin = c
		}

		r.Finish = &fin
	}

	return r, nil
}

// PaceProblems resolves every flow under every pace and reports each pace
// that breaks one, so a broken pace shows before a run picks it.
func (s *PaceSet) PaceProblems(flows map[string]flow.Flow) []string {
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

// settleCI decides an optional CI review on a review entry: the entry is
// rewritten to what runs, or skipped when nothing does, and its by dial
// says why.
func (s *PaceSet) settleCI(e *ResolvedEntry, published bool) {
	arg := flow.Parse(e.Entry).Arg

	by, ok := ReviewBy(arg)
	if !ok || !by.CIOptional {
		return
	}

	settled, why := by.Settle(s.Remote, published)
	origin := arg

	i := slices.IndexFunc(e.Dials, func(d Dial) bool { return d.Name == "by" })
	if i >= 0 {
		origin = e.Dials[i].Origin + ", " + arg
	}

	if why != "" {
		origin += " (" + why + ")"
	}

	dial := Dial{Name: "by", Value: settled.Arg(), Origin: origin}

	switch {
	case settled.Arg() == "":
		e.Skipped = origin
	case i >= 0:
		e.Dials[i] = dial
	default:
		e.Dials = append([]Dial{dial}, e.Dials...)
	}

	if settled.Arg() != "" {
		e.Entry = "review:" + settled.Arg()
	}
}

// published reports a pr or push among the entries that run.
func published(entries []ResolvedEntry) bool {
	return slices.ContainsFunc(entries, func(e ResolvedEntry) bool {
		return e.Skipped == "" && (e.Step == "pr" || e.Step == "push")
	})
}

// DefaultPaceName is the pace an unnamed run takes when no layer names one.
const DefaultPaceName = "medium"

// resolvePaces records the merged paces, default pace and ceiling, the
// user's file under the project's layers, and builds the one pace set every
// run resolves against.
func (e *Effective) resolvePaces(fm *layered) {
	if fm.Paces != nil {
		e.Paces = map[string]Pace{}
		e.PacesOrigin = map[string]Origin{}

		for name, p := range fm.Paces {
			e.Paces[name] = p.clone()
			e.PacesOrigin[name] = fm.originOf("paces." + name)

			for _, key := range p.Keys() {
				e.PacesOrigin[name+"."+key] = fm.originOf("paces." + name + "." + key)
			}
		}
	}

	if fm.Pace != "" {
		e.Pace = fm.Pace
		e.PaceOrigin = fm.originOf("pace")
	}

	e.resolveCeiling(fm)

	s := NewPaceSet()
	s.Remote = e.Review.Remote()
	s.Over(e.Paces, func(name, key string) Origin {
		if key == "" {
			return e.PacesOrigin[name]
		}

		return e.PacesOrigin[name+"."+key]
	})

	if e.Pace != "" {
		s.Default, s.DefaultOrigin = e.Pace, e.PaceOrigin
	}

	s.Ceiling, s.CeilingOrigins = e.Ceiling, e.CeilingOrigin
	s.Except, s.ExceptOrigin = e.CeilingExcept, e.CeilingExceptOrigin
	e.PaceSet = s

	e.paceFacts()

	for _, name := range s.Except {
		if _, err := s.Lookup(name); err != nil {
			e.problem(true, "ceiling.except (%s): %v", s.ExceptOrigin, err)
		}
	}

	// the user's default serves every project, and one that only some
	// declare must not stop the rest; a run that takes it still fails
	if s.Default != "" {
		if _, err := s.Lookup(s.Default); err != nil {
			e.problem(s.DefaultOrigin.Layer != userLayer, "pace (%s): %v", s.DefaultOrigin, err)
		}
	}

	for _, bad := range s.PaceProblems(e.Flows) {
		e.problem(false, "%s", bad)
	}
}

// paceFacts is every pace key, the built-ins included, then the default
// pace and the ceiling, each with the layer that set it.
func (e *Effective) paceFacts() {
	s := e.PaceSet

	for _, name := range s.Names() {
		p := s.Paces[name]
		if p.Empty() {
			e.Facts = append(e.Facts, Fact{
				Key:    "paces." + name,
				Value:  "(no dials: flows run as declared)",
				Origin: s.Origins[name],
			})

			continue
		}

		for _, key := range p.Keys() {
			e.Facts = append(e.Facts, Fact{
				Key:    "paces." + name + "." + key,
				Value:  p.Shown(key),
				Origin: s.Origins[name+"."+key],
			})
		}
	}

	if s.Default != "" {
		e.Facts = append(e.Facts, Fact{Key: "pace", Value: s.Default, Origin: s.DefaultOrigin})
	}

	for _, key := range s.Ceiling.Keys() {
		e.Facts = append(
			e.Facts,
			Fact{Key: "ceiling." + key, Value: s.Ceiling.Shown(key), Origin: s.CeilingOrigins[key]},
		)
	}

	if len(s.Except) > 0 {
		e.Facts = append(e.Facts, Fact{
			Key:    "ceiling.except",
			Value:  strings.Join(s.Except, ", "),
			Origin: s.ExceptOrigin,
		})
	}
}

// resolveCeiling records the merged ceiling with each key's layer.
func (e *Effective) resolveCeiling(fm *layered) {
	if fm.Ceiling == nil {
		return
	}

	e.Ceiling = fm.Ceiling.Pace.clone()
	e.CeilingOrigin = map[string]Origin{}

	for _, key := range e.Ceiling.Keys() {
		e.CeilingOrigin[key] = fm.originOf("ceiling." + key)
	}

	if e.CeilingExcept = fm.Ceiling.Except; e.CeilingExcept != nil {
		e.CeilingExceptOrigin = fm.originOf("ceiling.except")
	}
}
