package testkit

import (
	"github.com/amberpixels/f10/cli/internal/hostops"
)

var PickGH = &hostops.Pickup{
	Number: "30",
	URL:    "https://github.com/o/r/pull/30",
	Branch: "alice/fix-login",
	View:   "f10 pr view --comments 30",
	Diff:   "f10 pr diff 30",
}

var PickMerged = &hostops.Pickup{
	Number: "30",
	URL:    PickGH.URL,
	Branch: PickGH.Branch,
	View:   PickGH.View,
	Diff:   PickGH.Diff,
	SHA:    "abc1234abc",
	State:  "merged by alice on 2026-10-12",
}
