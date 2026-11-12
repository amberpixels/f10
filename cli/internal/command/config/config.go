// Package config holds the commands that report and settle a project's setup:
// f10 config, f10 init, f10 flow, f10 agents and f10 doctor.
package config

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A View is the one model both outputs render - the human table and --json
// marshal the same struct, so they cannot disagree.
type View struct {
	CheckoutRoot    string               `json:"checkoutRoot"`
	StorageRoot     string               `json:"storageRoot"`
	Instructions    string               `json:"instructions"` // source description, f10-load-project.sh wording
	NestedIgnored   string               `json:"nestedIgnored,omitempty"`
	Remote          string               `json:"remote,omitempty"`
	PlansDir        string               `json:"plansDir"`
	Plans           []string             `json:"plans,omitempty"`
	Fields          []facts.Field        `json:"fields"`
	Unrecognized    []facts.Field        `json:"unrecognized,omitempty"`
	Layering        string               `json:"layering"`
	Visibility      string               `json:"visibility"`
	Storage         string               `json:"storage"`
	TrackerKind     facts.TrackerKind    `json:"trackerKind,omitempty"`
	IDPrefix        string               `json:"idPrefix,omitempty"`
	IDPrefixOrigin  facts.Origin         `json:"idPrefixOrigin,omitzero"`
	IDPrefixDerived bool                 `json:"idPrefixDerived,omitempty"`
	PriorityLabels  map[string]string    `json:"priorityLabels,omitempty"` // host label to priority level, from tracker.priority
	TrackerStatus   map[string]string    `json:"trackerStatus,omitempty"`  // started and shipped to the tracker's words, from tracker.status
	TitleTemplate   string               `json:"titleTemplate,omitempty"`  // tracker.title, the template task titles are written through
	Review          facts.Review         `json:"review,omitzero"`          // the remote review's four facts, zero when none is declared
	MergeMethod     string               `json:"mergeMethod,omitempty"`
	Catchup         string               `json:"catchup"`
	CatchupStacked  string               `json:"catchupStacked"`
	Traces          bool                 `json:"traces"`
	Lore            string               `json:"lore"`
	LoreGate        string               `json:"loreGate"`
	StaleAfter      facts.StaleAfter     `json:"staleAfter,omitzero"`
	DriverTimeout   string               `json:"driverTimeout"` // driver_timeout, as a Go duration
	Flows           map[string]flow.Flow `json:"flows"`
	FlowByArea      map[string]string    `json:"flowByArea,omitempty"`
	DriveEnv        []string             `json:"driveEnv,omitempty"`
	Watch           string               `json:"watch"`
	WatchCatchup    bool                 `json:"watchCatchup"`
	ReportBack      bool                 `json:"reportBack"`
	UpkeepPaths     []string             `json:"upkeepPaths,omitempty"`
	Verify          []facts.VerifyDecl   `json:"verify,omitempty"`
	VerifyOrigin    facts.Origin         `json:"verifyOrigin,omitzero"`
	Agents          []runplan.AgentEntry `json:"agents,omitempty"`  // the merged agents map, the user's file under the project's, each key with its origin
	Paces           []paceEntry          `json:"paces,omitempty"`   // every pace's keys, built-ins under the user's under the project's, each with its origin
	Pace            *paceEntry           `json:"pace,omitempty"`    // the default pace, when any layer names one
	Ceiling         []paceEntry          `json:"ceiling,omitempty"` // the project's cap on every pace, a key per entry, except last
	Facts           []facts.Fact         `json:"facts"`             // every typed value with its origin
	Problems        []facts.Problem      `json:"problems,omitempty"`
	Roots           []string             `json:"roots,omitempty"` // F10_ROOTS; empty means -C takes paths only
}

// withAgents and withPaces list the merged agents map and every pace key,
// the user's file and the built-ins included; facts already holds each as a
// fact and each problem.
func (v *View) withAgents(eff *facts.Effective) {
	for _, key := range slices.Sorted(maps.Keys(eff.Agents)) {
		o := eff.AgentsOrigin[key]
		v.Agents = append(v.Agents, runplan.AgentEntry{Key: key, Value: eff.Agents[key], Origin: o.String()})
	}
}

func (v *View) withPaces(eff *facts.Effective) {
	set := eff.PaceSet

	for _, name := range set.Names() {
		p := set.Paces[name]
		if p.Empty() {
			v.Paces = append(v.Paces, paceEntry{
				Key: "paces." + name, Value: "(no dials: flows run as declared)", Origin: set.Origins[name],
			})

			continue
		}

		for _, key := range p.Keys() {
			v.Paces = append(v.Paces, paceEntry{
				Key: "paces." + name + "." + key, Value: p.Shown(key), Origin: set.Origins[name+"."+key],
			})
		}
	}

	if set.Default != "" {
		v.Pace = &paceEntry{Key: "pace", Value: set.Default, Origin: set.DefaultOrigin}
	}

	for _, key := range set.Ceiling.Keys() {
		v.Ceiling = append(v.Ceiling, paceEntry{
			Key: "ceiling." + key, Value: set.Ceiling.Shown(key), Origin: set.CeilingOrigins[key],
		})
	}

	if len(set.Except) > 0 {
		v.Ceiling = append(v.Ceiling, paceEntry{
			Key: "ceiling.except", Value: strings.Join(set.Except, ", "), Origin: set.ExceptOrigin,
		})
	}
}

// A paceEntry is one pace key with its dials and the layer that set it.
type paceEntry struct {
	Key    string       `json:"key"` // <pace>.<step>, or pace for the default
	Value  string       `json:"value"`
	Origin facts.Origin `json:"origin"`
}

func Command() *cli.Command {
	return &cli.Command{
		Name:      "config",
		Usage:     "print the effective configuration, with the origin of every value",
		ArgsUsage: "[verify]",
		Flags: []cli.Flag{
			ui.JSONFlag("emit the same view as JSON"),
		},
		Action: runConfig,
	}
}

func runConfig(ctx context.Context, cmd *cli.Command) error {
	// through the shared target, so -C answers here too: which project's
	// configuration you see is what this command is for
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	lay, pb, eff := t.Layout, t.Probes, t.Effective

	// the project load prints this one line, so a section selector stays
	// limited to it until another has a reader
	if cmd.Args().Len() > 0 {
		if cmd.Args().Len() > 1 || cmd.Args().First() != "verify" {
			return fmt.Errorf(
				"config: %q is not a section it prints alone (only verify)",
				strings.Join(cmd.Args().Slice(), " "),
			)
		}

		field := facts.VerifyFieldIn(eff.Fields)

		line := VerifyLine(field)
		if eff.Verify != nil {
			base, changed, err := VerifyDiff(ctx, lay)
			line = ScopedVerifyLine(eff, field, base, changed, err)
		}

		_, err := io.WriteString(cmd.Writer, line)

		return err
	}

	v := &View{
		CheckoutRoot:    lay.CheckoutRoot,
		StorageRoot:     lay.StorageRoot.Path(),
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
		DriverTimeout:   eff.DriverTimeout.String(),
		Flows:           eff.Flows,
		FlowByArea:      eff.FlowByArea,
		DriveEnv:        eff.DriveEnv,
		Watch:           eff.Watch,
		WatchCatchup:    eff.WatchCatchup,
		ReportBack:      eff.ReportBack,
		UpkeepPaths:     eff.UpkeepPaths,
		Verify:          eff.Verify,
		VerifyOrigin:    eff.VerifyOrigin,
		Facts:           eff.Facts,
		Problems:        eff.Problems,
		Roots:           projects.Roots(),
	}

	v.withAgents(eff)
	v.withPaces(eff)

	if cmd.Bool("json") {
		return ui.JSON(cmd.Writer, v)
	}

	RenderHuman(cmd.Writer, v, ui.Width(cmd.Writer))

	return nil
}
