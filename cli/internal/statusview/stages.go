package statusview

import (
	"maps"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// A color is one palette color: its code in the terminal's 256 colors and
// the CSS variable the board's page defines for it.
type color struct{ ANSI, CSS string }

var (
	green   = color{"2", "green"}
	red     = color{"1", "red"}
	yellow  = color{"3", "yellow"}
	orange  = color{"208", "orange"}
	blue    = color{"4", "blue"}
	cyan    = color{"6", "cyan"}
	magenta = color{"5", "magenta"}
)

// Weight is how loud a stage is drawn: bold where it asks for you or for a
// next move, faint where it is past.
type Weight int

const (
	Plain Weight = iota
	Bold
	Faint
)

// A look is a stage's color and weight, on the terminal and the page alike.
type look struct {
	Color  color
	Weight Weight
}

// stageLooks is the one palette on both stage vocabularies, status's and
// the board's, by how much a stage needs you: done green, asking, halted or
// blocked red, implementing yellow, reviewing orange, planning blue. The
// terminal and the board's page both draw it; a stage it lacks is faint.
var stageLooks = map[string]look{
	StageDone:                        {green, Plain},
	stageShipped:                     {green, Faint},
	lookaround.GroupFinished:         {green, Plain},
	lookaround.StageReady:            {green, Bold},
	stageAsking:                      {red, Bold},
	stageHalted:                      {red, Bold},
	lookaround.StageAsksWaiting:      {red, Bold},
	lookaround.StageBlocked:          {red, Bold},
	lookaround.StageAbandoned:        {red, Plain},
	stageImplementing:                {yellow, Plain},
	lookaround.GroupUpdated:          {yellow, Bold},
	stageReviewing:                   {orange, Plain},
	stageResolving:                   {orange, Plain},
	lookaround.StageInReview:         {orange, Plain},
	lookaround.StagePROpen:           {orange, Plain},
	stagePlanning:                    {blue, Plain},
	stageJudging:                     {blue, Plain},
	stagePlanned:                     {blue, Faint},
	lookaround.GroupStarted:          {blue, Bold},
	lookaround.StageMergedUnfinished: {cyan, Bold},
	lookaround.GroupReviewed:         {magenta, Bold},
}

// StageLooks is the palette by stage, a copy, for the board's page.
func StageLooks() map[string]look { return maps.Clone(stageLooks) }

// StageStyle is the stage's look in the terminal.
func StageStyle(stage string) lipgloss.Style {
	s := lipgloss.NewStyle()

	look, ok := stageLooks[stage]
	if !ok {
		return s.Faint(true)
	}

	s = s.Foreground(lipgloss.Color(look.Color.ANSI))

	switch look.Weight {
	case Bold:
		return s.Bold(true)
	case Faint:
		return s.Faint(true)
	case Plain:
	}

	return s
}
