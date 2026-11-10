package driverec

import (
	"time"
)

const // defaultEvery is the fallback look: a run-state write wakes a drive
// sooner (drivewait.go)
DefaultEvery = 30 * time.Second
