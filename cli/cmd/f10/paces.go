package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A run's pace comes from, first to last: --pace, the branch's record, the
// project's pace:, the user's pace:. None of them leaves every flow as
// declared.

// cfgPace is the pace a started task runs, under `branch.<name>.` beside
// f10-flow, so a rerun of start or drive keeps it.
const cfgPace = "f10-pace"

// pacesConfig is the frontmatter of ~/.f10/paces.md: the user's paces,
// laid over the built-ins, and the pace every project takes unless it
// names one.
type pacesConfig struct {
	Paces   map[string]facts.Pace `yaml:"paces"`
	Pace    string                `yaml:"pace"`
	Ceiling any                   `yaml:"ceiling"` // refused: a cap is the project's to set
}

// readPacesConfig refuses a pace the vocabulary refuses, naming the file,
// since a run must not go on a half-read pace.
func readPacesConfig() (pacesConfig, string, error) {
	var cfg pacesConfig

	path, err := readUserConfig(pacesConfigFile, &cfg)
	if err != nil {
		return cfg, path, err
	}

	var bad []string
	if cfg.Ceiling != nil {
		bad = append(bad, "ceiling: a ceiling is the project's, declared in project.md; here it would just be a pace")
	}

	for _, name := range slices.Sorted(maps.Keys(cfg.Paces)) {
		bad = append(bad, facts.CheckPace(name, cfg.Paces[name])...)
	}

	if len(bad) > 0 {
		return cfg, path, fmt.Errorf("%s: %s", path, strings.Join(bad, "; "))
	}

	return cfg, path, nil
}

// paceSet is the built-ins, the user's paces, then the project's, key by
// key, with the default pace the last layer naming one gives.
func paceSet(eff *facts.Effective) (*facts.PaceSet, error) {
	s := facts.NewPaceSet()

	user, path, err := readPacesConfig()
	if err != nil {
		return nil, err
	}

	userOrigin := "declared (user, " + path + ")"
	s.Over(user.Paces, func(string, string) string { return userOrigin })

	if user.Pace != "" {
		s.Default, s.DefaultOrigin = user.Pace, userOrigin
	}

	if eff != nil {
		s.Remote = eff.Review.Remote()
		s.Over(eff.Paces, func(name, key string) string { return eff.PacesOrigin[name+"."+key] })

		if eff.Pace != "" {
			s.Default, s.DefaultOrigin = eff.Pace, eff.PaceOrigin
		}

		s.Ceiling, s.CeilingOrigins = eff.Ceiling, eff.CeilingOrigin
		s.Except, s.ExceptOrigin = eff.CeilingExcept, eff.CeilingExceptOrigin

		for _, name := range s.Except {
			if _, err := s.Lookup(name); err != nil {
				return nil, fmt.Errorf("ceiling.except (%s): %w", s.ExceptOrigin, err)
			}
		}
	}

	return s, nil
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
		recorded, err := gitx.Config(ctx, main, "branch."+branch+"."+cfgPace)
		if err != nil {
			return rp, fmt.Errorf("reading the recorded pace: %w", err)
		}

		if recorded != "" {
			rp = runPace{name: recorded, origin: "recorded on branch " + branch}
		}
	}

	if rp.name == "" && s.Default != "" {
		rp = runPace{name: s.Default, origin: s.DefaultOrigin}
	}

	if rp.name == "" {
		return runPace{name: facts.DefaultPaceName, origin: paceOriginDefault}, nil
	}

	if _, err := s.Lookup(rp.name); err != nil {
		return runPace{}, fmt.Errorf("%s: %w", rp.label(), err)
	}

	return rp, nil
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
