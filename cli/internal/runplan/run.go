package runplan

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
)

// RunInput is what a run adds over the facts.
type RunInput struct {
	Pace    string // --pace; "" for none typed
	Profile string // --agent or --model; "" for none typed
	Main    string // the main checkout, where branch records live
	Branch  string // the branch whose records the run reads; "" for none
}

// A resolvedRun is a run: its pace, and the flows and agents map it
// resolves to under that pace.
type resolvedRun struct {
	eff  *facts.Effective
	in   RunInput
	Pace runPace
}

// Flow is the named flow under the run's pace; "" is default, or the area's
// pick on a project declaring flow_by_area.
func (r *resolvedRun) Flow(name string) (ShipFlow, error) {
	return PacedFlow(r.eff, r.Pace.Name, name)
}

// Entries is what the named declared flow runs under the run's pace,
// labels kept.
func (r *resolvedRun) Entries(name string) (flow.Flow, error) {
	declared, ok := r.eff.Flow(name)
	if !ok {
		return nil, fmt.Errorf("no flow %s", name)
	}

	res, err := r.eff.PaceSet.ResolveFlow(name, declared, r.Pace.Name)
	if err != nil {
		return nil, err
	}

	return res.RunFlow(), nil
}

// FlowAt is the named flow under another pace: a drive task's own.
func (r *resolvedRun) FlowAt(pace, name string) (ShipFlow, error) {
	return PacedFlow(r.eff, pace, name)
}

// Agents is the merged agents map with the run's layers on: its profile
// flag, else the profile the branch records, then the pace's effort dials.
func (r *resolvedRun) Agents(ctx context.Context) (AgentMap, error) {
	m := AgentMapOf(r.eff)

	if flag := strings.TrimSpace(r.in.Profile); flag != "" {
		if _, err := agentctl.ParseProfile(flag); err != nil {
			return nil, fmt.Errorf("--agent %w", err)
		}

		m = m.With(flag, AgentFlagOrigin)
	} else {
		var err error
		if m, err = m.WithBranch(ctx, r.in.Main, r.in.Branch); err != nil {
			return nil, err
		}
	}

	return m.withPace(r.eff.PaceSet, r.Pace), nil
}

// ResolveRun picks the run's pace: the flag, else the branch's record,
// else the default a layer names, else medium. A pace no layer declares
// fails with the list.
func ResolveRun(ctx context.Context, eff *facts.Effective, in RunInput) (*resolvedRun, error) {
	pace, err := activePace(ctx, eff.PaceSet, in.Pace, in.Main, in.Branch)
	if err != nil {
		return nil, err
	}

	return &resolvedRun{eff: eff, in: in, Pace: pace}, nil
}

// A runPace is the pace a run takes and where that came from; a zero one
// is no pace.
type runPace struct {
	Name   string
	Origin string
}

func (rp runPace) label() string {
	if rp.Origin == "--pace" {
		return "--pace " + rp.Name
	}

	return "pace " + rp.Name + " (" + rp.Origin + ")"
}

// PaceOriginDefault marks medium taken because nothing named a pace; start
// and drive record only a typed --pace, so it never reaches a branch.
const PaceOriginDefault = "default"

// activePace picks the run's pace: the flag, else what branch records,
// else the set's default, else medium. A name the set lacks fails with the
// list.
func activePace(ctx context.Context, s *facts.PaceSet, flag, main, branch string) (runPace, error) {
	rp := runPace{}

	switch flag = strings.ToLower(strings.TrimSpace(flag)); {
	case flag != "":
		rp = runPace{Name: flag, Origin: "--pace"}
	case branch != "":
		recorded, err := RecordedPace(ctx, main, branch)
		if err != nil {
			return rp, err
		}

		if recorded != "" {
			rp = runPace{Name: recorded, Origin: "recorded on branch " + branch}
		}
	}

	if rp.Name == "" && s.Default != "" {
		rp = runPace{Name: s.Default, Origin: s.DefaultOrigin.String()}
	}

	if rp.Name == "" {
		return runPace{Name: facts.DefaultPaceName, Origin: PaceOriginDefault}, nil
	}

	if _, err := s.Lookup(rp.Name); err != nil {
		return runPace{}, fmt.Errorf("%s: %w", rp.label(), err)
	}

	return rp, nil
}

// RecordedPace is the pace start or drive recorded on branch, "" for none.
func RecordedPace(ctx context.Context, main, branch string) (string, error) {
	recorded, err := branchmeta.Get(ctx, main, branch, branchmeta.Pace)
	if err != nil {
		return "", fmt.Errorf("reading the recorded pace: %w", err)
	}

	return recorded, nil
}

func PaceFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "pace",
		Usage: "run at the pace `NAME` (slow, medium, fast, ultra or one the paces declare); f10 config lists them",
	}
}
