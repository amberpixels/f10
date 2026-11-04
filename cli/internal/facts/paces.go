package facts

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/mdline"
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
	On     bool   `yaml:"-"` // a ceiling's word: the step runs under every pace
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
		case d.word == "on":
			d = Dials{On: true}
		case key == "review" && d.word != "on":
			d = Dials{By: d.word}
		}

		(*p)[key] = d
	}

	return nil
}

// MarshalYAML writes the shorthand a word stands for, so a migrated
// finish or off reads back as it was written.
func (d Dials) MarshalYAML() (any, error) {
	switch {
	case d.Off:
		return "off", nil
	case d.On:
		return "on", nil
	case d.Finish != "":
		return d.Finish, nil
	}

	type plain Dials

	return plain(d), nil
}

// Shown is the dials as `by local, rounds 2`, or off, on, or the finish word.
func (d Dials) Shown() string {
	switch {
	case d.Off:
		return "off"
	case d.On:
		return "on"
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
		case d.On:
			bad = append(bad, at+": on is a ceiling's word; a pace runs every entry it does not turn off")

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

	if d.By != "" && !validBy(d.By) {
		bad = append(bad, fmt.Sprintf("%s: by %s is not local, host, ci, local+ci, ci? or local+ci?", at, d.By))
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
	for key, d := range c.Pace {
		out[key] = d
	}

	if len(c.Except) > 0 {
		out["except"] = c.Except
	}

	return out, nil
}

// CheckCeiling reports what is wrong with a ceiling, each line naming
// ceiling.<key>. A cap needs an order from fast to slow, so off, by and
// marker are refused.
func CheckCeiling(c Ceiling) []string {
	var bad []string

	for _, key := range slices.Sorted(maps.Keys(c.Pace)) {
		d, at := c.Pace[key], "ceiling."+key

		switch {
		case key == PaceFinishKey:
			if d.Finish == "" || d.mapped {
				bad = append(bad, at+": finish is on or off")
			}

			continue
		case key == "catchup":
			bad = append(bad, at+": catchup is never a flow entry")

			continue
		case !stepNameRE.MatchString(key):
			bad = append(bad, fmt.Sprintf("%s: %q is not a step name", at, key))

			continue
		case d.word != "":
			bad = append(bad, fmt.Sprintf("%s: %q is not a shorthand; write on, or a map of dials", at, d.word))

			continue
		case d.Off:
			bad = append(bad, at+": a ceiling never skips a step; write on to keep it running under every pace")

			continue
		}

		for _, dial := range []string{"by", "marker"} {
			if d.value(dial) != "" {
				bad = append(
					bad,
					fmt.Sprintf("%s: %s has no order from fast to slow, so a ceiling cannot cap it", at, dial),
				)
			}
		}

		for _, s := range dialSteps {
			if d.value(s.dial) != "" && s.step != key {
				bad = append(bad, fmt.Sprintf("%s: %s is a %s dial", at, s.dial, s.step))
			}
		}

		bad = append(bad, d.checkValues(at)...)
	}

	for _, name := range c.Except {
		if !flowNameRE.MatchString(name) {
			bad = append(bad, fmt.Sprintf("ceiling.except: %q is not a lowercase name", name))
		}
	}

	return bad
}

// BuiltinPaces are the paces f10 ships. medium is empty: naming it runs
// every flow as declared, the way to switch a faster default off.
func BuiltinPaces() map[string]Pace {
	return map[string]Pace{
		"slow": {
			"review":      {By: "local+ci?", Rounds: 2},
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
	Remote        bool // Review declares a remote reviewer, which an optional CI review needs
	Paces         map[string]Pace
	Origins       map[string]string // <pace>.<key> to the layer that set it
	Default       string            // pace: the pace a run takes when it names none; "" for none
	DefaultOrigin string

	Ceiling        Pace              // the project's cap on every pace Except does not name
	CeilingOrigins map[string]string // <key> to the layer that set it
	Except         []string
	ExceptOrigin   string
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
			s.Origins[name] = origin(name, "")
		}

		for key, d := range p {
			s.Paces[name][key] = d
			s.Origins[name+"."+key] = origin(name, key)
		}
	}
}

// effortRank orders effort words from fast (low) to slow (max).
func effortRank(word string) int {
	return slices.Index(agentctl.Kinds[agentctl.DefaultKind].Efforts, word)
}

// Exempt says whether the ceiling leaves the named pace alone.
func (s *PaceSet) Exempt(name string) bool {
	return len(s.Ceiling) == 0 || slices.Contains(s.Except, name)
}

// CeilingOrigin is how a dial the ceiling set names its source.
func (s *PaceSet) CeilingOrigin(key string) string {
	return "ceiling (" + s.CeilingOrigins[key] + ")"
}

// Capped is the pace with the ceiling laid on, and each replaced dial keyed
// `<step>.<dial>` (bare finish). Silence on finish or rounds is the faster
// default, so it is capped; silence on effort leaves the agents map alone.
func (s *PaceSet) Capped(name string) (Pace, map[string]Dial) {
	src := s.Paces[name]
	if s.Exempt(name) {
		return src, nil
	}

	p := maps.Clone(src)
	if p == nil {
		p = Pace{}
	}

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

	for key, c := range s.Ceiling {
		d, origin := p[key], s.CeilingOrigin(key)

		if key == PaceFinishKey {
			if c.Finish == "off" && d.Finish != "off" {
				caps[key] = Dial{Name: key, Value: "off", Origin: origin, Capped: said(d.Finish)}
				p[key] = Dials{Finish: "off"}
			}

			continue
		}

		before := d

		if c.On && d.Off {
			caps[key+".run"] = Dial{Name: "run", Value: "on", Origin: origin, Capped: said("off")}
			d.Off = false
		}

		if c.Rounds == 2 && d.Rounds != 2 && !d.Off {
			caps[key+".rounds"] = Dial{Name: "rounds", Value: "2", Origin: origin, Capped: said(d.value("rounds"))}
			d.Rounds = 2
		}

		if c.Depth == "full" && d.Depth == "light" && !d.Off {
			caps[key+".depth"] = Dial{Name: "depth", Value: "full", Origin: origin, Capped: said(d.Depth)}
			d.Depth = "full"
		}

		if c.Effort != "" && d.Effort != "" && !d.Off && effortRank(d.Effort) < effortRank(c.Effort) {
			caps[key+".effort"] = Dial{Name: "effort", Value: c.Effort, Origin: origin, Capped: said(d.Effort)}
			d.Effort = c.Effort
		}

		if d != before {
			p[key] = d
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

	return nil, fmt.Errorf("no pace %q; the paces are %s (f10 config lists their dials)",
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

// handoffStep is the step whose pace key also reaches every pr entry.
const handoffStep = "handoff"

// deliveringSteps are the generic steps whose output is a run's shipment.
var deliveringSteps = []string{"implement", "commit", "push", "pr", "deploy"}

// ResolveFlow lays the named pace on a flow. An entry argument beats the
// pace for the dial it expresses (review:local fixes by), and the pace
// beats the step's own default. No pace ("") leaves the flow as declared, an optional CI review settled.
func (s *PaceSet) ResolveFlow(flowName string, flow []string, pace string) (Resolved, error) {
	r := Resolved{Flow: flowName, Pace: pace}

	if pace != "" {
		if _, err := s.Lookup(pace); err != nil {
			return r, err
		}
	}

	p, caps := s.Capped(pace)

	from := func(key string) string { return "pace " + pace + " (" + s.Origins[pace+"."+key] + ")" }
	dial := func(key, name, value string) Dial {
		if c, ok := caps[key+"."+name]; ok {
			return c
		}

		return Dial{Name: name, Value: value, Origin: from(key)}
	}

	for _, entry := range flow {
		step, arg := SplitEntry(entry)
		e := ResolvedEntry{Entry: entry, Declared: entry, Step: step}
		d, ok := p[step]

		if c, unskipped := caps[step+".run"]; unskipped {
			e.Dials = append(e.Dials, c)
		}

		switch {
		case !ok:
		case d.Off:
			e.Skipped = "off in " + from(step)
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

				e.Dials = append(e.Dials, dial(step, ds.dial, v))
			}
		}

		if step == "review" && arg != "" && ok && d.By != "" && !d.Off {
			e.Dials = append([]Dial{{Name: "by", Value: arg, Origin: "entry argument"}}, e.Dials...)
		}

		if step == "review" && e.Skipped == "" {
			s.settleCI(&e, published(r.Entries))
		}

		// The pr step runs the handoff itself, so it learns handoff: off from a
		// dial; dialSteps cannot carry it, since it reads another step's key.
		if h := p[handoffStep]; step == "pr" && e.Skipped == "" && h.Off {
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

	if d, ok := p[PaceFinishKey]; ok && d.Finish != "" && endsAtPR(run) {
		fin := Dial{Name: PaceFinishKey, Value: d.Finish, Origin: from(PaceFinishKey)}
		if c, ok := caps[PaceFinishKey]; ok {
			fin = c
		}

		r.Finish = &fin
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

// settleCI decides an optional CI review on a review entry: the entry is
// rewritten to what runs, or skipped when nothing does, and its by dial
// says why.
func (s *PaceSet) settleCI(e *ResolvedEntry, published bool) {
	_, arg := SplitEntry(e.Entry)

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

// resolvePaces records the project's paces, default pace and ceiling, the
// old drive_finish and Rounds line read as dials. The user's layer and the
// built-ins are the caller's to lay under them.
func (e *Effective) resolvePaces(fm *layered, review section) {
	layers := map[string]bool{}

	if fm.Paces != nil {
		e.Paces = map[string]Pace{}
		e.PacesOrigin = map[string]string{}

		for name, p := range fm.Paces {
			e.Paces[name] = maps.Clone(p)

			for key := range p {
				o := fm.origin["paces."+name+"."+key]
				e.PacesOrigin[name+"."+key] = "declared (" + o + ", frontmatter)"
				layers["declared ("+o+", frontmatter)"] = true
			}
		}
	}

	if fm.Pace != "" {
		e.Pace = fm.Pace
		e.PaceOrigin = "declared (" + fm.origin["pace"] + ", frontmatter)"
	}

	for _, origin := range e.convertOldPaceKeys(fm, review) {
		layers[origin] = true
	}

	if e.Paces != nil {
		e.Facts = append(e.Facts, Fact{
			Key:    "paces",
			Value:  pacesShown(e.Paces),
			Origin: strings.Join(slices.Sorted(maps.Keys(layers)), " + "),
		})
	}

	if e.Pace != "" {
		e.Facts = append(e.Facts, Fact{Key: "pace", Value: e.Pace, Origin: e.PaceOrigin})
	}

	e.resolveCeiling(fm)

	if e.Paces == nil && e.Ceiling == nil {
		return
	}

	// the user's file may change these paces, so a run reports the final
	// word; this catches the project's own mistakes early
	s := NewPaceSet()
	s.Remote = e.Review.Remote()
	s.Over(e.Paces, func(name, key string) string { return e.PacesOrigin[name+"."+key] })
	s.Ceiling, s.CeilingOrigins, s.Except = e.Ceiling, e.CeilingOrigin, e.CeilingExcept

	for _, bad := range s.PaceProblems(e.Flows) {
		e.problem(false, "%s", bad)
	}
}

// DefaultPaceName is the pace an unnamed run takes when no layer names one.
const DefaultPaceName = "medium"

// convertOldPaceKeys reads drive_finish and a `Rounds: 2` line as dials of
// the project's default pace, unless its paces already set them, and
// returns the origins it wrote.
func (e *Effective) convertOldPaceKeys(fm *layered, review section) []string {
	name := e.Pace
	if name == "" {
		name = DefaultPaceName
	}

	var origins []string

	put := func(key, origin string, apply func(*Dials)) {
		if e.Paces == nil {
			e.Paces, e.PacesOrigin = map[string]Pace{}, map[string]string{}
		}

		if e.Paces[name] == nil {
			e.Paces[name] = Pace{}
		}

		d := e.Paces[name][key]
		apply(&d)
		e.Paces[name][key] = d

		if prior := e.PacesOrigin[name+"."+key]; prior != "" {
			origin = prior + " + " + origin
		}

		e.PacesOrigin[name+"."+key] = origin
		origins = append(origins, origin)
	}

	if fm.DriveFinish != "" {
		if e.Paces[name][PaceFinishKey].Finish != "" {
			e.problem(
				false,
				"drive_finish ignored for pace %s, since paces.%s.finish is declared; f10 init --migrate removes it",
				name,
				name,
			)
		} else {
			put(PaceFinishKey, "converted from drive_finish ("+fm.origin["drive_finish"]+")", func(d *Dials) {
				*d = Dials{Finish: fm.DriveFinish}
			})
			e.problem(false, "drive_finish is read as paces.%s.finish; f10 init --migrate rewrites it", name)
		}
	}

	if n := proseRounds(review.body); n != 0 {
		where := "the Review section"
		if review.file != "" {
			where += " in " + review.file
		}

		if e.Paces[name]["review"].Rounds != 0 {
			e.problem(false, "Rounds: %d in %s ignored for pace %s, since paces.%s.review sets rounds; delete the line",
				n, where, name, name)
		} else {
			put("review", "converted from Rounds: "+strconv.Itoa(n)+" ("+where+")", func(d *Dials) { d.Rounds = n })
			e.problem(false, "Rounds: %d in %s is read as paces.%s.review.rounds; f10 init --migrate rewrites it",
				n, where, name)
		}
	}

	return origins
}

// roundsRE is the Review section's `Rounds: 2` line, bold or a list item.
var roundsRE = regexp.MustCompile(`(?i)^rounds\s*:\s*([12])\b`)

// proseRounds is the number a Review section's Rounds line names, 0 for none.
func proseRounds(body string) int {
	for line := range strings.SplitSeq(body, "\n") {
		if item, ok := mdline.Item(line); ok {
			line = item
		}

		line = strings.TrimSpace(strings.ReplaceAll(line, "*", ""))
		if m := roundsRE.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])

			return n
		}
	}

	return 0
}

// resolveCeiling records the merged ceiling with each key's layer.
func (e *Effective) resolveCeiling(fm *layered) {
	if fm.Ceiling == nil {
		return
	}

	e.Ceiling = fm.Ceiling.Pace
	if e.Ceiling == nil {
		e.Ceiling = Pace{}
	}

	e.CeilingOrigin = map[string]string{}
	layers := map[string]bool{}

	var keys []string

	for _, key := range slices.Sorted(maps.Keys(e.Ceiling)) {
		o := "declared (" + fm.origin["ceiling."+key] + ", frontmatter)"
		e.CeilingOrigin[key] = o
		layers[o] = true
		keys = append(keys, key+" ("+e.Ceiling[key].Shown()+")")
	}

	value := strings.Join(keys, ", ")

	if e.CeilingExcept = fm.Ceiling.Except; e.CeilingExcept != nil {
		e.CeilingExceptOrigin = "declared (" + fm.origin["ceiling.except"] + ", frontmatter)"
		layers[e.CeilingExceptOrigin] = true
		value += "; except " + strings.Join(e.CeilingExcept, ", ")
	}

	e.Facts = append(e.Facts, Fact{
		Key:    "ceiling",
		Value:  value,
		Origin: strings.Join(slices.Sorted(maps.Keys(layers)), " + "),
	})
}
