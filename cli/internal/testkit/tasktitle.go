package testkit

import (
	"github.com/amberpixels/f10/cli/internal/hostops"
)

// TitledHost is a gh host whose project declares tracker.title.
func TitledHost() hostops.Host {
	h := hostops.NewHost("gh", "")
	h.Titles, h.Prefix = "{id}: {title}", "FT"

	return h
}
