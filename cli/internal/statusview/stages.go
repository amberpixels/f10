package statusview

import (
	"maps"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// A Color is one palette color: its code in the terminal's 256 colors and
// the CSS variable the board's page defines for it.
type Color struct{ ANSI, CSS string }

var (
	green   = Color{"2", "green"}
	red     = Color{"1", "red"}
	yellow  = Color{"3", "yellow"}
	orange  = Color{"208", "orange"}
	blue    = Color{"4", "blue"}
	cyan    = Color{"6", "cyan"}
	magenta = Color{"5", "magenta"}
)

// Weight is how loud a stage is drawn: bold where it asks for you or for a
// next move, faint where it is past.
type Weight int

const (
	Plain Weight = iota
	Bold
	Faint
)

// A Look is a stage's color and weight, on the terminal and the page alike.
type Look struct {
	Color  Color
	Weight Weight
}

// stageLooks is the one palette on both stage vocabularies, status's and
// the board's, by how much a stage needs you: done green, asking, halted or
// blocked red, implementing yellow, reviewing orange, planning blue. The
// terminal and the board's page both draw it; a stage it lacks is faint.
var stageLooks = map[string]Look{
	StageDone:                        {green, Plain},
	StageShipped:                     {green, Faint},
	lookaround.GroupFinished:         {green, Plain},
	lookaround.StageReady:            {green, Bold},
	StageAsking:                      {red, Bold},
	StageHalted:                      {red, Bold},
	lookaround.StageAsksWaiting:      {red, Bold},
	lookaround.StageBlocked:          {red, Bold},
	lookaround.StageAbandoned:        {red, Plain},
	StageImplementing:                {yellow, Plain},
	lookaround.GroupUpdated:          {yellow, Bold},
	StageReviewing:                   {orange, Plain},
	StageResolving:                   {orange, Plain},
	lookaround.StageInReview:         {orange, Plain},
	lookaround.StagePROpen:           {orange, Plain},
	StagePlanning:                    {blue, Plain},
	StageJudging:                     {blue, Plain},
	StagePlanned:                     {blue, Faint},
	lookaround.GroupStarted:          {blue, Bold},
	lookaround.StageMergedUnfinished: {cyan, Bold},
	lookaround.GroupReviewed:         {magenta, Bold},
}

// StageLooks is the palette by stage, a copy, for the board's page.
func StageLooks() map[string]Look { return maps.Clone(stageLooks) }

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
