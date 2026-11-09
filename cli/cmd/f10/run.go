package main

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

// A run's configuration is what facts merged (the built-ins, the user's
// files, the project's layers) with the run's own layers on top: its flags
// and the records on its branch. Every run command resolves it here, so a
// pace, a flow and a profile mean the same thing to start, drive, flow,
// agents and the status gates.

// runInput is what a run adds over the facts.
type runInput struct {
	pace    string // --pace; "" for none typed
	profile string // --agent or --model; "" for none typed
	main    string // the main checkout, where branch records live
	branch  string // the branch whose records the run reads; "" for none
}

// A resolvedRun is a run: its pace, and the flows and agents map it
// resolves to under that pace.
type resolvedRun struct {
	eff  *facts.Effective
	in   runInput
	pace runPace
}

// resolveRun picks the run's pace: the flag, else the branch's record,
// else the default a layer names, else medium. A pace no layer declares
// fails with the list.
func resolveRun(ctx context.Context, eff *facts.Effective, in runInput) (*resolvedRun, error) {
	pace, err := activePace(ctx, eff.PaceSet, in.pace, in.main, in.branch)
	if err != nil {
		return nil, err
	}

	return &resolvedRun{eff: eff, in: in, pace: pace}, nil
}

// flow is the named flow under the run's pace; "" is default, or the area's
// pick on a project declaring flow_by_area.
func (r *resolvedRun) flow(name string) (shipFlow, error) {
	return pacedFlow(r.eff, r.pace.name, name)
}

// entries is what the named declared flow runs under the run's pace,
// labels kept.
func (r *resolvedRun) entries(name string) (flow.Flow, error) {
	declared, ok := r.eff.Flow(name)
	if !ok {
		return nil, fmt.Errorf("no flow %s", name)
	}

	res, err := r.eff.PaceSet.ResolveFlow(name, declared, r.pace.name)
	if err != nil {
		return nil, err
	}

	return res.RunFlow(), nil
}

// flowAt is the named flow under another pace: a drive task's own.
func (r *resolvedRun) flowAt(pace, name string) (shipFlow, error) {
	return pacedFlow(r.eff, pace, name)
}

// agents is the merged agents map with the run's layers on: its profile
// flag, else the profile the branch records, then the pace's effort dials.
func (r *resolvedRun) agents(ctx context.Context) (agentMap, error) {
	m := agentMapOf(r.eff)

	if flag := strings.TrimSpace(r.in.profile); flag != "" {
		if _, err := agentctl.ParseProfile(flag); err != nil {
			return nil, fmt.Errorf("--agent %w", err)
		}

		m = m.with(flag, agentFlagOrigin)
	} else {
		var err error
		if m, err = m.withBranch(ctx, r.in.main, r.in.branch); err != nil {
			return nil, err
		}
	}

	return m.withPace(r.eff.PaceSet, r.pace), nil
}

// A runPace is the pace a run takes and where that came from; a zero one
// is no pace.
type runPace struct {
	name   string
	origin string
}

// paceOriginDefault marks medium taken because nothing named a pace; start
// and drive record only a typed --pace, so it never reaches a branch.
const paceOriginDefault = "default"

// activePace picks the run's pace: the flag, else what branch records,
// else the set's default, else medium. A name the set lacks fails with the
// list.
func activePace(ctx context.Context, s *facts.PaceSet, flag, main, branch string) (runPace, error) {
	rp := runPace{}

	switch flag = strings.ToLower(strings.TrimSpace(flag)); {
	case flag != "":
		rp = runPace{name: flag, origin: "--pace"}
	case branch != "":
		recorded, err := recordedPace(ctx, main, branch)
		if err != nil {
			return rp, err
		}

		if recorded != "" {
			rp = runPace{name: recorded, origin: "recorded on branch " + branch}
		}
	}

	if rp.name == "" && s.Default != "" {
		rp = runPace{name: s.Default, origin: s.DefaultOrigin.String()}
	}

	if rp.name == "" {
		return runPace{name: facts.DefaultPaceName, origin: paceOriginDefault}, nil
	}

	if _, err := s.Lookup(rp.name); err != nil {
		return runPace{}, fmt.Errorf("%s: %w", rp.label(), err)
	}

	return rp, nil
}

// recordedPace is the pace start or drive recorded on branch, "" for none.
func recordedPace(ctx context.Context, main, branch string) (string, error) {
	recorded, err := branchmeta.Get(ctx, main, branch, branchmeta.Pace)
	if err != nil {
		return "", fmt.Errorf("reading the recorded pace: %w", err)
	}

	return recorded, nil
}

func (rp runPace) label() string {
	if rp.origin == "--pace" {
		return "--pace " + rp.name
	}

	return "pace " + rp.name + " (" + rp.origin + ")"
}

func paceFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "pace",
		Usage: "run at the pace `NAME` (slow, medium, fast, ultra or one the paces declare); f10 config lists them",
	}
}
