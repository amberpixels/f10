package testkit

import (
	"time"
)

// The standup collectors are exercised against a scripted git and gh or
// glab: what each asks and what it reads back. Grouping is internal/standup's
// and tested there.

var StandupSince = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
