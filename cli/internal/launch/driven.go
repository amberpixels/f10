package launch

import (
	"slices"
	"strings"
)

// DrivenMarker tells a skill the run is driven from another session: no
// interactive question, every ask through run state (modes/driven.md).
const DrivenMarker = "--driven"

// Driven marks an f10 command as sent from outside; plain text (an answer, a
// steering note) goes as it is, since the agent it reaches already knows.
func Driven(text string) string {
	if !strings.HasPrefix(text, "/f10:") || slices.Contains(strings.Fields(text), DrivenMarker) {
		return text
	}

	return text + " " + DrivenMarker
}
