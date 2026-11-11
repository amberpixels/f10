package facts

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// The frontmatter declares every typed fact, and the default fills the rest.

// A Fact is one typed value and where it came from.
type Fact struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Origin Origin `json:"origin"`
}

// A Problem is something wrong with the declaration. An error stops the
// commands that act on facts; a warning is shown and nothing else.
type Problem struct {
	Error   bool   `json:"error,omitempty"`
	Message string `json:"message"`
	// File is the user's file the problem is in, "" for the project's facts
	File string `json:"-"`
}

// A source is a fact as the frontmatter declares it.
type source struct {
	set    bool
	shown  string // the value as config prints it
	origin Origin
}

// settle records the frontmatter's fact, else the default, and reports
// whether the frontmatter declared it.
func (e *Effective) settle(key string, fm source, def Fact) bool {
	if fm.set {
		e.Facts = append(e.Facts, Fact{Key: key, Value: fm.shown, Origin: fm.origin})

		return true
	}

	if def.Value != "" {
		e.Facts = append(e.Facts, def)
	}

	return false
}

// scalar records a registry key's fact, the frontmatter's value else its
// default, and returns the value in force.
func (e *Effective) scalar(fm *layered, key string) string {
	k := scalarNamed(key)

	if value := *k.field(&fm.Declared); value != "" {
		e.Facts = append(e.Facts, Fact{Key: key, Value: value, Origin: DeclaredFrontmatter(fm.origin[key])})

		return value
	}

	if k.def != "" {
		e.Facts = append(e.Facts, Fact{Key: key, Value: k.def, Origin: OriginOf(OriginDefault)})
	}

	return k.def
}

func (e *Effective) problem(isError bool, format string, args ...any) {
	e.Problems = append(e.Problems, Problem{Error: isError, Message: fmt.Sprintf(format, args...)})
}

// ErrorProblems are the problems that stop a command.
func (e *Effective) ErrorProblems() []Problem {
	var out []Problem

	for _, p := range e.Problems {
		if p.Error {
			out = append(out, p)
		}
	}

	return out
}

// Errors are the problems that stop a command, joined for its message.
func (e *Effective) Errors() string {
	var msgs []string

	for _, p := range e.Problems {
		if p.Error {
			msgs = append(msgs, p.Message)
		}
	}

	return strings.Join(msgs, "\n")
}

// resolve settles every typed fact from the merged frontmatter, the layout
// and the probes; declared tells whether a Review section is written.
func (e *Effective) resolve(fm *layered, declared map[string]section, lay *layout.Layout, pb *probe.Probes) {
	fmSource := func(key, shown string) source {
		if _, ok := fm.origin[key]; !ok || shown == "" {
			return source{}
		}

		return source{set: true, shown: shown, origin: DeclaredFrontmatter(fm.origin[key])}
	}

	e.Visibility = e.scalar(fm, "visibility")

	// out-of-tree declares itself by location, so finding it there is
	// detection, not the registry's default
	storageDefault := Fact{Key: "storage", Value: scalarNamed("storage").def, Origin: OriginOf(OriginDefault)}
	if outOfTree(lay) {
		storageDefault = Fact{Key: "storage", Value: "out-of-tree", Origin: OriginOf(OriginDetected)}
		e.Storage = "out-of-tree"
	}

	if e.settle("storage", fmSource("storage", fm.Storage), storageDefault) {
		e.Storage = fm.Storage
	}

	// layering is layout's to read, since it decides which files load
	if lay.LayeringDeclared {
		e.Facts = append(e.Facts, Fact{Key: "layering", Value: lay.Layering, Origin: DeclaredFrontmatter("worktree")})
	}

	e.resolveTracker(fm, fmSource, lay, pb)
	e.resolveHosting(fm)

	e.ReportBack = e.scalar(fm, "report_back") != "off"
	e.Watch = e.scalar(fm, "watch")
	e.WatchCatchup = e.scalar(fm, "watch_catchup") == "on"

	if e.UpkeepPaths = fm.Standup.Upkeep; e.UpkeepPaths != nil {
		e.Facts = append(e.Facts, Fact{
			Key:    "standup.upkeep",
			Value:  strings.Join(e.UpkeepPaths, ", "),
			Origin: DeclaredFrontmatter(fm.origin["standup"]),
		})
	}

	// frontmatter only: the prose heading stays the commands of a project
	// that scopes none, and the rules of one that does
	if e.Verify = fm.Verify; e.Verify != nil {
		e.VerifyOrigin = DeclaredIn(fm.origin["verify"])
		e.Facts = append(e.Facts, Fact{
			Key:    "verify",
			Value:  verifyShown(e.Verify),
			Origin: DeclaredFrontmatter(fm.origin["verify"]),
		})
	}

	_, reviewDeclared := declared["Review"]
	e.resolveReview(fm, fmSource, reviewDeclared, pb)

	// after review: a CI review in a flow asks whether a remote reviewer is
	// declared
	e.resolveFlows(fm)
	e.resolveAgents(fm)
	e.resolvePaces(fm)
}

func (e *Effective) resolveTracker(
	fm *layered,
	fmSource func(key, shown string) source,
	lay *layout.Layout,
	pb *probe.Probes,
) {
	fmKind := TrackerKind(fm.Tracker.Kind)
	if !slices.Contains(TrackerKinds, fmKind) {
		fmKind = "" // check() reports it
	}

	if e.settle("tracker.kind", fmSource("tracker", fmKind.String()), Fact{}) {
		e.TrackerKind = fmKind
	} else {
		e.TrackerKind = cliTrackerKind(pb)
	}

	fmPrefix := idPrefix(fm.Tracker.ID)
	if e.settle("tracker.id", fmSource("tracker", fmPrefix), Fact{}) {
		e.IDPrefix, e.IDPrefixOrigin = fmPrefix, DeclaredFrontmatter(fm.origin["tracker"])
	} else {
		// no declared id format: the project's own name settles the prefix,
		// until f10 init writes it down
		if e.IDPrefix = ProjectPrefix(lay.Project); e.IDPrefix != "" {
			e.IDPrefixOrigin = DerivedFrom(lay.Project)
			e.IDPrefixDerived = true
			e.Facts = append(e.Facts, Fact{Key: "tracker.id", Value: e.IDPrefix, Origin: e.IDPrefixOrigin})
		}
	}

	// nothing detects it
	if fm.Tracker.Priority != nil {
		e.PriorityLabels = priorityIndex(fm.Tracker.Priority)
		e.Facts = append(e.Facts, Fact{
			Key:    "tracker.priority",
			Value:  priorityShown(fm.Tracker.Priority),
			Origin: DeclaredFrontmatter(fm.origin["tracker"]),
		})
	}

	// a template check() refuses stays unset, so nothing writes through it
	if t, err := tasktitle.Parse(fm.Tracker.Title); fm.Tracker.Title != "" && err == nil {
		e.TitleTemplate = t
		e.Facts = append(e.Facts, Fact{
			Key:    "tracker.title",
			Value:  fm.Tracker.Title,
			Origin: DeclaredFrontmatter(fm.origin["tracker"]),
		})
	}

	if fm.Tracker.Status != nil {
		e.TrackerStatus = fm.Tracker.Status
		e.Facts = append(e.Facts, Fact{
			Key:    "tracker.status",
			Value:  mapShown(fm.Tracker.Status),
			Origin: DeclaredFrontmatter(fm.origin["tracker"]),
		})
	}
}

func mapShown(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		pairs = append(pairs, k+": "+m[k])
	}

	return strings.Join(pairs, ", ")
}

// priorityIndex inverts the declared map, each label lower-cased. Levels go
// in vocabulary order and the first keeps a label, so a label check() reports
// under two levels still indexes the same way on every run.
func priorityIndex(declared map[string]Labels) map[string]string {
	index := map[string]string{}

	for _, level := range driver.Priorities {
		for _, name := range declared[level] {
			if key := strings.ToLower(name); index[key] == "" {
				index[key] = level
			}
		}
	}

	return index
}

// priorityShown is the map as config prints it, levels in vocabulary order.
func priorityShown(declared map[string]Labels) string {
	var shown []string

	for _, level := range driver.Priorities {
		if names := declared[level]; len(names) > 0 {
			shown = append(shown, level+": "+strings.Join(names, ", "))
		}
	}

	if len(shown) == 0 {
		return "none"
	}

	return strings.Join(shown, "; ")
}

func (e *Effective) resolveHosting(fm *layered) {
	e.MergeMethod = e.scalar(fm, "merge_method")
	e.Catchup = e.scalar(fm, "catchup")
	e.CatchupStacked = e.scalar(fm, "catchup_stacked")
	e.Traces = e.scalar(fm, "traces") == "on"
	e.Lore = e.scalar(fm, "lore")
	e.LoreGate = e.scalar(fm, "lore_gate")

	// the default, off, does not parse, and leaves stale_after off
	if s, err := NewStaleAfter(e.scalar(fm, "stale_after")); err == nil {
		e.StaleAfter = s
	}

	// a bad value is already a problem that stops the command; the default
	// stands in until then
	e.DriverTimeout = driver.DefaultTimeout
	if d, err := ParseDriverTimeout(e.scalar(fm, "driver_timeout")); err == nil {
		e.DriverTimeout = d
	}

	if len(fm.DriveEnv) > 0 {
		e.Facts = append(e.Facts, Fact{
			Key:    "drive_env",
			Value:  strings.Join(fm.DriveEnv, ", "),
			Origin: DeclaredFrontmatter(fm.origin["drive_env"]),
		})
	}

	e.DriveEnv = fm.DriveEnv
}

func (e *Effective) resolveReview(
	fm *layered,
	fmSource func(key, shown string) source,
	declared bool,
	pb *probe.Probes,
) {
	fmReview := reviewFrom(fm.Review)

	switch {
	case e.settle("review", fmSource("review", reviewShown(fmReview)), Fact{}):
		e.Review = fmReview
	default:
		// a CI workflow running the Claude Code action is a remote review
		// detection can name; a declared Review section, even a local-only
		// one, has already said there is none
		if detected := reviewBody(pb); detected != "" && !declared {
			e.Review = parseReview(detected)
			e.Facts = append(
				e.Facts,
				Fact{Key: "review", Value: reviewShown(e.Review), Origin: OriginOf(OriginDetected)},
			)
		}
	}
}

func outOfTree(lay *layout.Layout) bool {
	for _, l := range lay.Layers {
		if l.Label == "out-of-tree" {
			return true
		}
	}

	return false
}

// cliTrackerKind is what the CLI routing implies when nothing declares one.
func cliTrackerKind(pb *probe.Probes) TrackerKind {
	switch pb.CLI.Value {
	case "gh":
		return TrackerGitHub
	case "glab":
		return TrackerGitLab
	}

	return ""
}

// reviewFrom reads the frontmatter's strings with the prose items'
// sub-parsers, so both forms mean the same.
func reviewFrom(d ReviewDecl) Review {
	var (
		kind, reviewer, arrives, trigger, how string
		done, doneText, handled, handledText  string
	)

	if d.Reviewer != "" {
		kind, reviewer = parseReviewer(d.Reviewer)
	}

	if d.Arrives != "" {
		arrives = parseArrives(d.Arrives)
	}

	if d.Trigger != "" {
		trigger, how = parseTrigger(d.Trigger)
	}

	if d.Done != "" {
		doneHalf, handledHalf := splitDoneHandled(d.Done)
		done, doneText = parseDone(doneHalf)
		handled, handledText = parseHandled(handledHalf)
	}

	settled, settledText := parseSettled(d.Settled)

	return Review{
		Reviewer:     reviewer,
		ReviewerKind: kind,
		Arrives:      arrives,
		Trigger:      trigger,
		TriggerHow:   how,
		ManualHow:    manualPart(d.Trigger),
		Done:         done,
		DoneText:     doneText,
		Handled:      handled,
		HandledText:  handledText,
		Settled:      settled,
		SettledText:  settledText,
		settledAs:    d.Settled,
	}
}

func reviewShown(r Review) string {
	if r == (Review{}) {
		return ""
	}

	join := func(parts ...string) string {
		return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " ")
	}

	shown := []string{
		join(r.ReviewerKind, r.Reviewer), r.Arrives, r.Trigger,
		join(r.Done, r.DoneText), join(r.Handled, r.HandledText),
	}

	if r.Settled != "" {
		shown = append(shown, "settled "+join(r.Settled, r.SettledText))
	}

	return strings.Join(shown, ", ")
}
