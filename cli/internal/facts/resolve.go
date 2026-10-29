package facts

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/mdline"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// Resolution gives every fact the binary acts on one typed value. The
// frontmatter wins; prose under the old headings is read for one release,
// with a warning naming the migration; the contract's default fills the
// rest. A fact both places declare differently is an error, since the
// binary would follow one and the model, reading the whole file, maybe the
// other.

// A Fact is one typed value and where it came from.
type Fact struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// A Problem is something wrong with the declaration. An error stops the
// commands that act on facts; a warning is shown and nothing else.
type Problem struct {
	Error   bool   `json:"error,omitempty"`
	Message string `json:"message"`
}

// migrateHint is what every prose-read warning points at.
const migrateHint = "f10 init --migrate moves them into the frontmatter"

// A source is one place a fact can be declared.
type source struct {
	set    bool
	shown  string // the value as config prints it, and as the two places are compared
	origin string
	file   string
}

// settle picks the frontmatter over prose over the default, records the
// fact, and reports a prose read or a disagreement. It returns which source
// won: "frontmatter", "prose" or "".
func (e *Effective) settle(key string, fm, prose source, def Fact) string {
	switch {
	case fm.set && prose.set && fm.shown != prose.shown:
		e.problem(
			true,
			"%s: the frontmatter says %q, the prose in %s says %q; keep one",
			key,
			fm.shown,
			prose.file,
			prose.shown,
		)
	case fm.set && prose.set:
		e.twice = append(e.twice, [2]string{prose.file, key})
	case prose.set:
		e.proseRead = append(e.proseRead, [2]string{prose.file, key})
	}

	switch {
	case fm.set:
		e.Facts = append(e.Facts, Fact{Key: key, Value: fm.shown, Origin: fm.origin})

		return "frontmatter"
	case prose.set:
		e.Facts = append(e.Facts, Fact{Key: key, Value: prose.shown, Origin: prose.origin})

		return "prose"
	default:
		if def.Value != "" {
			e.Facts = append(e.Facts, def)
		}

		return ""
	}
}

func (e *Effective) problem(isError bool, format string, args ...any) {
	e.Problems = append(e.Problems, Problem{Error: isError, Message: fmt.Sprintf(format, args...)})
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

// resolve settles every typed fact from the merged frontmatter, the
// declared prose sections, the layout and the probes.
func (e *Effective) resolve(fm *layered, declared map[string]section, lay *layout.Layout, pb *probe.Probes) {
	fmSource := func(key, shown string) source {
		if _, ok := fm.origin[key]; !ok || shown == "" {
			return source{}
		}

		return source{set: true, shown: shown, origin: "declared (" + fm.origin[key] + ", frontmatter)"}
	}

	proseSource := func(name, shown string) source {
		sec, ok := declared[name]
		if !ok || shown == "" {
			return source{}
		}

		return source{set: true, shown: shown, origin: "declared (" + sec.layer + ")", file: sec.file}
	}

	body := func(name string) string { return declared[name].body }

	// visibility and storage: a section's word, as absorb used to read it
	if e.settle("visibility", fmSource("visibility", fm.Visibility),
		proseSource("Visibility", proseWord(body("Visibility"), "public", "stealth")),
		Fact{Key: "visibility", Value: "stealth", Origin: OriginDefault}) == "frontmatter" {
		e.Visibility = fm.Visibility
	} else if w := proseWord(body("Visibility"), "public", "stealth"); w != "" {
		e.Visibility = w
	}

	storageDefault := Fact{Key: "storage", Value: "in-repo", Origin: OriginDefault}
	if outOfTree(lay) {
		storageDefault = Fact{Key: "storage", Value: "out-of-tree", Origin: OriginDetected}
		e.Storage = "out-of-tree"
	}

	switch e.settle("storage", fmSource("storage", fm.Storage),
		proseSource("Storage", proseWord(body("Storage"), "out-of-tree", "in-repo")), storageDefault) {
	case "frontmatter":
		e.Storage = fm.Storage
	case "prose":
		e.Storage = proseWord(body("Storage"), "out-of-tree", "in-repo")
	}

	// layering is layout's to read, since it decides which files load
	if lay.LayeringDeclared {
		e.Facts = append(
			e.Facts,
			Fact{Key: "layering", Value: lay.Layering, Origin: "declared (worktree, " + lay.LayeringFrom + ")"},
		)
		if lay.LayeringFrom == "prose" {
			e.problem(false, "layering read from the prose of the worktree's project.md; %s", migrateHint)
		}
	}

	e.resolveTracker(fm, fmSource, proseSource, body, lay, pb)
	e.resolveHosting(fm, fmSource, proseSource, body)

	e.ReportBack = fm.ReportBack != "off"
	if fm.ReportBack != "" {
		e.Facts = append(e.Facts, Fact{
			Key:    "report_back",
			Value:  fm.ReportBack,
			Origin: "declared (" + fm.origin["report_back"] + ", frontmatter)",
		})
	}

	e.Watch = cmp.Or(fm.Watch, "finish")
	if fm.Watch != "" {
		e.Facts = append(e.Facts, Fact{
			Key:    "watch",
			Value:  fm.Watch,
			Origin: "declared (" + fm.origin["watch"] + ", frontmatter)",
		})
	}

	// frontmatter only, off unless declared: an unattended catchup pushed
	// with a lease is the riskiest thing the watcher does
	e.settle("watch_catchup", fmSource("watch_catchup", fm.WatchCatchup), source{},
		Fact{Key: "watch_catchup", Value: "off", Origin: OriginDefault})
	e.WatchCatchup = fm.WatchCatchup == "on"

	// frontmatter only: no prose ever declared it
	if e.UpkeepPaths = fm.Standup.Upkeep; e.UpkeepPaths != nil {
		e.Facts = append(e.Facts, Fact{
			Key:    "standup.upkeep",
			Value:  strings.Join(e.UpkeepPaths, ", "),
			Origin: "declared (" + fm.origin["standup"] + ", frontmatter)",
		})
	}

	_, reviewDeclared := declared["Review"]
	e.resolveReview(fm, fmSource, proseSource, body, reviewDeclared, pb)

	// after review: converting the old drive chain asks whether a remote
	// reviewer is declared
	e.resolveFlows(fm, declared)
	e.resolveAgents(fm)

	// one warning per file and kind, naming every fact it concerns
	for file, keys := range groupByFile(e.proseRead) {
		e.problem(false, "%s read from the prose in %s; %s", keys, file, migrateHint)
	}

	for file, keys := range groupByFile(e.twice) {
		e.problem(false, "%s declared in both the frontmatter and the prose of %s; delete the prose", keys, file)
	}
}

// groupByFile yields each file with its keys joined, files in order.
func groupByFile(pairs [][2]string) func(yield func(string, string) bool) {
	byFile := map[string][]string{}
	for _, p := range pairs {
		byFile[p[0]] = append(byFile[p[0]], p[1])
	}

	return func(yield func(string, string) bool) {
		for _, file := range slices.Sorted(maps.Keys(byFile)) {
			if !yield(file, strings.Join(byFile[file], ", ")) {
				return
			}
		}
	}
}

func (e *Effective) resolveTracker(
	fm *layered,
	fmSource func(key, shown string) source,
	proseSource func(name, shown string) source,
	body func(string) string,
	lay *layout.Layout,
	pb *probe.Probes,
) {
	fmKind := trackerKinds[fm.Tracker.Kind]
	proseKind := proseTrackerKind(strings.ToLower(body("Tracker")))

	switch e.settle("tracker.kind", fmSource("tracker", fmKind), proseSource("Tracker", proseKind), Fact{}) {
	case "frontmatter":
		e.TrackerKind = fmKind
	case "prose":
		e.TrackerKind = proseKind
	default:
		e.TrackerKind = cliTrackerKind(pb)
	}

	fmPrefix, prosePrefix := idPrefix(fm.Tracker.ID), idPrefix(body("Tracker"))

	switch e.settle("tracker.id", fmSource("tracker", fmPrefix), proseSource("Tracker", prosePrefix), Fact{}) {
	case "frontmatter":
		e.IDPrefix, e.IDPrefixOrigin = fmPrefix, "declared ("+fm.origin["tracker"]+", frontmatter)"
	case "prose":
		e.IDPrefix, e.IDPrefixOrigin = prosePrefix, proseSource("Tracker", prosePrefix).origin
	default:
		// no declared id format: the project's own name settles the prefix,
		// until f10 init writes it down
		if e.IDPrefix = ProjectPrefix(lay.Project); e.IDPrefix != "" {
			e.IDPrefixOrigin = "derived from project name " + lay.Project
			e.IDPrefixDerived = true
			e.Facts = append(e.Facts, Fact{Key: "tracker.id", Value: e.IDPrefix, Origin: e.IDPrefixOrigin})
		}
	}

	// frontmatter only: no prose ever declared it, and nothing detects it
	if fm.Tracker.Priority != nil {
		e.PriorityLabels = priorityIndex(fm.Tracker.Priority)
		e.Facts = append(e.Facts, Fact{
			Key:    "tracker.priority",
			Value:  priorityShown(fm.Tracker.Priority),
			Origin: "declared (" + fm.origin["tracker"] + ", frontmatter)",
		})
	}

	// a template check() refuses stays unset, so nothing writes through it
	if t, err := tasktitle.Parse(fm.Tracker.Title); fm.Tracker.Title != "" && err == nil {
		e.TitleTemplate = t
		e.Facts = append(e.Facts, Fact{
			Key:    "tracker.title",
			Value:  fm.Tracker.Title,
			Origin: "declared (" + fm.origin["tracker"] + ", frontmatter)",
		})
	}

	if fm.Tracker.Status != nil {
		e.TrackerStatus = fm.Tracker.Status
		e.Facts = append(e.Facts, Fact{
			Key:    "tracker.status",
			Value:  mapShown(fm.Tracker.Status),
			Origin: "declared (" + fm.origin["tracker"] + ", frontmatter)",
		})
	}
}

// mapShown is a string map as `key: value` pairs in key order.
func mapShown(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		pairs = append(pairs, k+": "+m[k])
	}

	return strings.Join(pairs, ", ")
}

// priorityIndex inverts the declared map: each label, lower-cased, to its
// level. Levels go in vocabulary order and the first level keeps a label, so
// a label check() reports under two levels still indexes the same way on
// every run, which config shows even while the error stops other commands.
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

// priorityShown is the map as config prints it: `urgent: P0; high: P1,
// priority:high`, levels in vocabulary order, levels with no labels left out.
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

func (e *Effective) resolveHosting(
	fm *layered,
	fmSource func(key, shown string) source,
	proseSource func(name, shown string) source,
	body func(string) string,
) {
	hosting := mdline.Unmark(body("Hosting & PR"))

	proseMethod := firstMatch(mergeMethodRe, hosting)
	switch e.settle(
		"merge_method",
		fmSource("merge_method", fm.MergeMethod),
		proseSource("Hosting & PR", proseMethod),
		Fact{},
	) {
	case "frontmatter":
		e.MergeMethod = fm.MergeMethod
	case "prose":
		e.MergeMethod = proseMethod
	}

	proseCatchup := firstMatch(catchupRe, hosting)
	switch e.settle("catchup", fmSource("catchup", fm.Catchup), proseSource("Hosting & PR", proseCatchup),
		Fact{Key: "catchup", Value: "rebase", Origin: OriginDefault}) {
	case "frontmatter":
		e.Catchup = fm.Catchup
	case "prose":
		e.Catchup = proseCatchup
	default:
		e.Catchup = "rebase"
	}

	// frontmatter only: a stacked base is merged unless the project takes the replay a rebase risks
	if e.settle("catchup_stacked", fmSource("catchup_stacked", fm.CatchupStacked), source{},
		Fact{Key: "catchup_stacked", Value: "merge", Origin: OriginDefault}) == "frontmatter" {
		e.CatchupStacked = fm.CatchupStacked
	} else {
		e.CatchupStacked = "merge"
	}

	// frontmatter only: no prose ever declared it
	e.settle("traces", fmSource("traces", fm.Traces), source{},
		Fact{Key: "traces", Value: "off", Origin: OriginDefault})
	e.Traces = fm.Traces == "on"

	e.settle("lore", fmSource("lore", fm.Lore), source{},
		Fact{Key: "lore", Value: "local", Origin: OriginDefault})

	e.Lore = cmp.Or(fm.Lore, "local")

	e.settle("lore_gate", fmSource("lore_gate", fm.LoreGate), source{},
		Fact{Key: "lore_gate", Value: "warn", Origin: OriginDefault})

	e.LoreGate = cmp.Or(fm.LoreGate, "warn")

	e.settle("stale_after", fmSource("stale_after", fm.StaleAfter), source{},
		Fact{Key: "stale_after", Value: "off", Origin: OriginDefault})
	e.StaleAfter = fm.StaleAfter

	e.settle("driver_timeout", fmSource("driver_timeout", fm.DriverTimeout), source{},
		Fact{Key: "driver_timeout", Value: driver.DefaultTimeout.String(), Origin: OriginDefault})

	// a bad value is already a problem that stops the command; the default
	// stands in until then
	e.DriverTimeout = driver.DefaultTimeout
	if d, err := ParseDriverTimeout(fm.DriverTimeout); err == nil {
		e.DriverTimeout = d
	}

	if len(fm.DriveEnv) > 0 {
		e.Facts = append(e.Facts, Fact{
			Key:    "drive_env",
			Value:  strings.Join(fm.DriveEnv, ", "),
			Origin: "declared (" + fm.origin["drive_env"] + ", frontmatter)",
		})
	}

	e.DriveEnv = fm.DriveEnv
}

func (e *Effective) resolveReview(
	fm *layered,
	fmSource func(key, shown string) source,
	proseSource func(name, shown string) source,
	body func(string) string,
	declared bool,
	pb *probe.Probes,
) {
	fmReview := reviewFrom(fm.Review)
	proseReview := parseReview(body("Review"))

	switch e.settle(
		"review",
		fmSource("review", reviewShown(fmReview)),
		proseSource("Review", reviewShown(proseReview)),
		Fact{},
	) {
	case "frontmatter":
		e.Review = fmReview
	case "prose":
		e.Review = proseReview
	default:
		// a CI workflow running the Claude Code action is a remote review
		// detection can name; a declared Review section, even a local-only
		// one, has already said there is none
		if detected := reviewBody(pb); detected != "" && !declared {
			e.Review = parseReview(detected)
			e.Facts = append(e.Facts, Fact{Key: "review", Value: reviewShown(e.Review), Origin: OriginDetected})
		}
	}
}

// The legacy prose readers: what the binary found in each section before
// the frontmatter existed, read through mdline so markup does not hide a
// fact. They go when the prose fallback does.
var (
	mergeMethodRe = regexp.MustCompile(`(?i)\bmerge(?:\s+method)?\s*:\s*(squash|merge|rebase)\b`)
	catchupRe     = regexp.MustCompile(`(?i)\bcatchup\s*:\s*(merge|rebase|off)\b`)
)

func firstMatch(re *regexp.Regexp, text string) string {
	if m := re.FindStringSubmatch(text); m != nil {
		return strings.ToLower(m[1])
	}

	return ""
}

// proseWord is the marked word when the section holds it, the other one
// when it holds anything at all, "" for no section.
func proseWord(body, marked, otherwise string) string {
	switch {
	case strings.TrimSpace(body) == "":
		return ""
	case strings.Contains(strings.ToLower(body), marked):
		return marked
	default:
		return otherwise
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

// proseTrackerKind is the kind a Tracker section names, or "". Only a
// whole word of prose counts: a name in code, a url or a compound word
// (`create-notion-task`, api.github.com) names a skill or a host instead.
func proseTrackerKind(lower string) string {
	words := map[string]bool{}

	for _, w := range strings.FieldsFunc(mdline.Prose(lower), isWordBreak) {
		w = strings.Trim(w, "_'‘’.")
		words[strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "’s")] = true
	}

	for _, k := range []struct{ needle, kind string }{
		{needle: "notion", kind: "Notion"},
		{needle: "github", kind: "GitHub Issues"},
		{needle: "gitlab", kind: "GitLab work items"},
		{needle: "jira", kind: "Jira"},
	} {
		if words[k.needle] {
			return k.kind
		}
	}

	return ""
}

// isWordBreak splits prose into words, keeping the - . / _ inside a
// compound word, a host or a url so that it stays one word.
func isWordBreak(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`[](){}<>,;:!?"*|`, r)
}

// cliTrackerKind is what the CLI routing implies when nothing declares one.
func cliTrackerKind(pb *probe.Probes) string {
	switch pb.CLI.Value {
	case "gh":
		return "GitHub Issues"
	case "glab":
		return "GitLab work items"
	}

	return ""
}

// prosePipelines reads `- local: implement → commit` lines, names in lower
// case. A lone arrow line is the default pipeline, and `(planless)` in a
// name or its steps marks the pipeline planless.
func prosePipelines(body string) (map[string][]string, []string) {
	pipes := map[string][]string{}

	var planless []string

	for line := range strings.SplitSeq(body, "\n") {
		item, _ := mdline.Item(line)
		if item == "" {
			continue
		}

		name, steps := "", item
		if key, value, ok := mdline.Cut(item, ":"); ok && !strings.ContainsAny(key, "→>") {
			name, steps = strings.ToLower(key), value
		} else if !strings.Contains(item, "→") && !strings.Contains(item, "->") {
			continue
		}

		if strings.Contains(name+steps, "(planless)") {
			name = mdline.Bare(strings.ReplaceAll(name, "(planless)", ""))
			steps = strings.ReplaceAll(steps, "(planless)", "")
			planless = append(planless, cmpOr(name, "default"))
		}

		if list := splitSteps(steps); len(list) > 0 {
			pipes[cmpOr(name, "default")] = list
		}
	}

	if len(pipes) == 0 {
		return nil, nil
	}

	return pipes, planless
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}

	return s
}

// proseChain tokenizes Drive chain prose the way drive always has: arrows,
// commas and spaces separate, marks drop.
func proseChain(text string) []string {
	r := strings.NewReplacer("→", " ", "->", " ", ",", " ")

	var chain []string

	for tok := range strings.FieldsSeq(r.Replace(text)) {
		if tok = strings.ToLower(strings.Trim(tok, "`*_")); tok != "" && tok != "-" {
			chain = append(chain, tok)
		}
	}

	return chain
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
