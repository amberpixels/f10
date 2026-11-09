package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/projects"
)

// A view is the one model both outputs render - the human table and --json
// marshal the same struct, so they cannot disagree.
type view struct {
	CheckoutRoot    string              `json:"checkoutRoot"`
	StorageRoot     string              `json:"storageRoot"`
	LegacyStorage   bool                `json:"legacyStorage,omitempty"` // a worktree keeping its own .f10
	Instructions    string              `json:"instructions"`            // source description, f10-load-project.sh wording
	NestedIgnored   string              `json:"nestedIgnored,omitempty"`
	Remote          string              `json:"remote,omitempty"`
	PlansDir        string              `json:"plansDir"`
	Plans           []string            `json:"plans,omitempty"`
	Fields          []facts.Field       `json:"fields"`
	Unrecognized    []facts.Field       `json:"unrecognized,omitempty"`
	Layering        string              `json:"layering"`
	Visibility      string              `json:"visibility"`
	Storage         string              `json:"storage"`
	TrackerKind     string              `json:"trackerKind,omitempty"`
	IDPrefix        string              `json:"idPrefix,omitempty"`
	IDPrefixOrigin  string              `json:"idPrefixOrigin,omitempty"`
	IDPrefixDerived bool                `json:"idPrefixDerived,omitempty"`
	PriorityLabels  map[string]string   `json:"priorityLabels,omitempty"` // host label to priority level, from tracker.priority
	TrackerStatus   map[string]string   `json:"trackerStatus,omitempty"`  // started and shipped to the tracker's words, from tracker.status
	TitleTemplate   string              `json:"titleTemplate,omitempty"`  // tracker.title, the template task titles are written through
	Review          facts.Review        `json:"review,omitzero"`          // the remote review's four facts, zero when none is declared
	MergeMethod     string              `json:"mergeMethod,omitempty"`
	Catchup         string              `json:"catchup"`
	CatchupStacked  string              `json:"catchupStacked"`
	Traces          bool                `json:"traces"`
	Lore            string              `json:"lore"`
	LoreGate        string              `json:"loreGate"`
	StaleAfter      string              `json:"staleAfter,omitempty"`
	Flows           map[string][]string `json:"flows"`
	FlowByArea      map[string]string   `json:"flowByArea,omitempty"`
	DriveFinish     bool                `json:"driveFinish"`
	ReportBack      bool                `json:"reportBack"`
	Agents          []agentEntry        `json:"agents,omitempty"`  // the merged agents map, the user's file under the project's, each key with its origin
	Paces           []paceEntry         `json:"paces,omitempty"`   // every pace's keys, built-ins under the user's under the project's, each with its origin
	Pace            *paceEntry          `json:"pace,omitempty"`    // the default pace, when any layer names one
	Ceiling         []paceEntry         `json:"ceiling,omitempty"` // the project's cap on every pace, a key per entry, except last
	Facts           []facts.Fact        `json:"facts"`             // every typed value with its origin
	Problems        []facts.Problem     `json:"problems,omitempty"`
	Roots           []string            `json:"roots,omitempty"` // F10_ROOTS; empty means -C takes paths only
}

// withAgents puts the merged agents map on the view: one row per key in
// the facts table, in place of the project's single agents fact, so each
// value shows its own origin, the user's file included. A user file the
// vocabulary refuses is a problem, shown like a project.md one.
func (v *view) withAgents(eff *facts.Effective) {
	m, err := agentLayers(eff, "")
	if err != nil {
		v.Problems = append(v.Problems, facts.Problem{Error: true, Message: err.Error()})

		return
	}

	if len(m) == 0 {
		return
	}

	v.Facts = slices.DeleteFunc(slices.Clone(v.Facts), func(f facts.Fact) bool { return f.Key == "agents" })

	for _, key := range slices.Sorted(maps.Keys(m)) {
		e := m[key]
		v.Agents = append(v.Agents, e)
		v.Facts = append(v.Facts, facts.Fact{Key: "agents." + key, Value: e.Value, Origin: e.Origin})
	}
}

// A paceEntry is one pace key with its dials and the layer that set it.
type paceEntry struct {
	Key    string `json:"key"` // <pace>.<step>, or pace for the default
	Value  string `json:"value"`
	Origin string `json:"origin"`
}

// withPaces puts every pace key on the view, the user's file and the
// built-ins included, in place of the project's paces and pace facts, and
// each pace that breaks a flow as a problem.
func (v *view) withPaces(eff *facts.Effective) {
	set, err := paceSet(eff)
	if err != nil {
		v.Problems = append(v.Problems, facts.Problem{Error: true, Message: err.Error()})

		return
	}

	v.Facts = slices.DeleteFunc(slices.Clone(v.Facts), func(f facts.Fact) bool {
		return f.Key == "paces" || f.Key == "pace" || f.Key == "ceiling"
	})

	for _, name := range set.Names() {
		p := set.Paces[name]
		if len(p) == 0 {
			e := paceEntry{
				Key:    "paces." + name,
				Value:  "(no dials: flows run as declared)",
				Origin: set.Origins[name],
			}
			v.Paces = append(v.Paces, e)
			v.Facts = append(v.Facts, facts.Fact{Key: e.Key, Value: e.Value, Origin: e.Origin})

			continue
		}

		for _, key := range slices.Sorted(maps.Keys(p)) {
			e := paceEntry{Key: "paces." + name + "." + key, Value: p[key].Shown(), Origin: set.Origins[name+"."+key]}
			v.Paces = append(v.Paces, e)
			v.Facts = append(v.Facts, facts.Fact{Key: e.Key, Value: e.Value, Origin: e.Origin})
		}
	}

	if set.Default != "" {
		v.Pace = &paceEntry{Key: "pace", Value: set.Default, Origin: set.DefaultOrigin}
		v.Facts = append(v.Facts, facts.Fact{Key: "pace", Value: set.Default, Origin: set.DefaultOrigin})

		if _, err := set.Lookup(set.Default); err != nil {
			v.Problems = append(v.Problems, facts.Problem{Error: true, Message: "pace: " + err.Error()})
		}
	}

	for _, key := range slices.Sorted(maps.Keys(set.Ceiling)) {
		v.Ceiling = append(v.Ceiling, paceEntry{
			Key: "ceiling." + key, Value: set.Ceiling[key].Shown(), Origin: set.CeilingOrigins[key],
		})
	}

	if len(set.Except) > 0 {
		v.Ceiling = append(v.Ceiling, paceEntry{
			Key: "ceiling.except", Value: strings.Join(set.Except, ", "), Origin: set.ExceptOrigin,
		})
	}

	for _, e := range v.Ceiling {
		v.Facts = append(v.Facts, facts.Fact{Key: e.Key, Value: e.Value, Origin: e.Origin})
	}

	known := map[string]bool{}
	for _, p := range v.Problems {
		known[p.Message] = true
	}

	for _, bad := range set.PaceProblems(eff.Flows) {
		if !known[bad] {
			v.Problems = append(v.Problems, facts.Problem{Message: bad})
		}
	}
}

func configCommand() *cli.Command {
	return &cli.Command{
		Name:      "config",
		Usage:     "print the effective configuration, with the origin of every value",
		ArgsUsage: "[verify]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit the same view as JSON"},
		},
		Action: runConfig,
	}
}

func runConfig(ctx context.Context, cmd *cli.Command) error {
	// through the shared target, so -C answers here too: which project's
	// configuration you see is what this command is for
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	lay, pb, eff := t.lay, t.pb, t.eff

	// the project load prints this one line, so a section selector stays
	// limited to it until another has a reader
	if cmd.Args().Len() > 0 {
		if cmd.Args().Len() > 1 || cmd.Args().First() != "verify" {
			return fmt.Errorf(
				"config: %q is not a section it prints alone (only verify)",
				strings.Join(cmd.Args().Slice(), " "),
			)
		}

		field := verifyFieldIn(eff.Fields)

		line := verifyLine(field)
		if eff.Verify != nil {
			base, changed, err := verifyDiff(ctx, lay)
			line = scopedVerifyLine(eff, field, base, changed, err)
		}

		_, err := io.WriteString(cmd.Writer, line)

		return err
	}

	v := &view{
		CheckoutRoot:    lay.CheckoutRoot,
		StorageRoot:     lay.StorageRoot,
		LegacyStorage:   lay.LegacyStorage,
		Instructions:    lay.Source,
		NestedIgnored:   lay.NestedIgnored,
		Remote:          pb.RemoteURL,
		PlansDir:        pb.PlansDir,
		Plans:           pb.Plans,
		Fields:          eff.Fields,
		Unrecognized:    eff.Unrecognized,
		Layering:        eff.Layering,
		Visibility:      eff.Visibility,
		Storage:         eff.Storage,
		TrackerKind:     eff.TrackerKind,
		IDPrefix:        eff.IDPrefix,
		IDPrefixOrigin:  eff.IDPrefixOrigin,
		IDPrefixDerived: eff.IDPrefixDerived,
		PriorityLabels:  eff.PriorityLabels,
		TrackerStatus:   eff.TrackerStatus,
		TitleTemplate:   string(eff.TitleTemplate),
		Review:          eff.Review,
		MergeMethod:     eff.MergeMethod,
		Catchup:         eff.Catchup,
		CatchupStacked:  eff.CatchupStacked,
		Traces:          eff.Traces,
		Lore:            eff.Lore,
		LoreGate:        eff.LoreGate,
		StaleAfter:      eff.StaleAfter,
		Flows:           eff.Flows,
		FlowByArea:      eff.FlowByArea,
		DriveFinish:     eff.DriveFinish,
		ReportBack:      eff.ReportBack,
		Facts:           eff.Facts,
		Problems:        eff.Problems,
		Roots:           projects.Roots(),
	}

	v.withAgents(eff)
	v.withPaces(eff)

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("encoding view: %w", err)
		}

		return nil
	}

	renderHuman(cmd.Writer, v, termWidth(cmd.Writer))

	return nil
}
