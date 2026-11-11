package statusview

import (
	"maps"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// stageColors is the one palette on both stage vocabularies, status's and
// the board's, by how much a stage needs you: done green, asking, halted or
// blocked red, implementing yellow, reviewing orange, planning blue. The
// terminal and the board's page both draw it; a stage it lacks is faint.
var stageColors = map[string]string{
	StageDone:                        "green",
	StageShipped:                     "green",
	lookaround.GroupFinished:         "green",
	lookaround.StageReady:            "green",
	StageAsking:                      "red",
	StageHalted:                      "red",
	lookaround.StageAsksWaiting:      "red",
	lookaround.StageBlocked:          "red",
	"abandoned":                      "red",
	StageImplementing:                "yellow",
	lookaround.GroupUpdated:          "yellow",
	StageReviewing:                   "orange",
	StageResolving:                   "orange",
	lookaround.StageInReview:         "orange",
	lookaround.StagePROpen:           "orange",
	StagePlanning:                    "blue",
	StageJudging:                     "blue",
	StagePlanned:                     "blue",
	lookaround.GroupStarted:          "blue",
	lookaround.StageMergedUnfinished: "cyan",
	lookaround.GroupReviewed:         "magenta",
}

// ansiColor is each palette color in the terminal's 256 colors.
var ansiColor = map[string]string{
	"green": "2", "red": "1", "yellow": "3", "orange": "208", "blue": "4", "cyan": "6", "magenta": "5",
}

// StageColors is the palette by stage, a copy, for the board's page.
func StageColors() map[string]string { return maps.Clone(stageColors) }

// StageStyle is the stage's color in the terminal, bold where it asks for
// you or for a next move, faint where it is past.
func StageStyle(stage string) lipgloss.Style {
	s := lipgloss.NewStyle()

	color, ok := stageColors[stage]
	if !ok {
		return s.Faint(true)
	}

	s = s.Foreground(lipgloss.Color(ansiColor[color]))

	switch stage {
	case StageShipped, StagePlanned:
		return s.Faint(true)
	case lookaround.StageReady, StageAsking, StageHalted, lookaround.StageAsksWaiting, lookaround.StageBlocked,
		lookaround.StageMergedUnfinished, lookaround.GroupStarted, lookaround.GroupUpdated, lookaround.GroupReviewed:
		return s.Bold(true)
	}

	return s
}
