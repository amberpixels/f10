package statusview

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

// StageStyle is the one palette on both stage vocabularies, status's and
// the board's, by how much a stage needs you: done green, asking, halted or
// blocked red, implementing yellow, reviewing orange, planning blue, idle dim.
func StageStyle(stage string) lipgloss.Style {
	s := lipgloss.NewStyle()

	switch stage {
	case StageDone, lookaround.GroupFinished:
		return s.Foreground(lipgloss.Color("2"))
	case StageShipped:
		return s.Faint(true).Foreground(lipgloss.Color("2"))
	case lookaround.StageReady:
		return s.Bold(true).Foreground(lipgloss.Color("2"))
	case StageAsking, StageHalted, lookaround.StageAsksWaiting, lookaround.StageBlocked:
		return s.Bold(true).Foreground(lipgloss.Color("1"))
	case "abandoned":
		return s.Foreground(lipgloss.Color("1"))
	case StageImplementing:
		return s.Foreground(lipgloss.Color("3"))
	case StageReviewing, StageResolving, lookaround.StageInReview, lookaround.StagePROpen:
		return s.Foreground(lipgloss.Color("208"))
	case StagePlanning, StageJudging:
		return s.Foreground(lipgloss.Color("4"))
	case StagePlanned:
		return s.Faint(true).Foreground(lipgloss.Color("4"))
	case lookaround.StageMergedUnfinished:
		return s.Bold(true).Foreground(lipgloss.Color("6"))
	case lookaround.GroupStarted:
		return s.Bold(true).Foreground(lipgloss.Color("4"))
	case lookaround.GroupUpdated:
		return s.Bold(true).Foreground(lipgloss.Color("3"))
	case lookaround.GroupReviewed:
		return s.Bold(true).Foreground(lipgloss.Color("5"))
	default:
		return s.Faint(true)
	}
}
