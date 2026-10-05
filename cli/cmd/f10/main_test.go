package main

import (
	"runtime/debug"
	"testing"
)

func TestVersion(t *testing.T) {
	stamped := func(version string, settings ...debug.BuildSetting) *debug.BuildInfo {
		bi := &debug.BuildInfo{Settings: settings}
		bi.Main.Version = version

		return bi
	}

	cases := []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "unknown"},
		{"release install", stamped("v0.25.0"), true, "0.25.0"},
		{
			"pseudo-version past the tag",
			stamped("v0.25.1-0.20261005190000-0123456789ab"),
			true,
			"0.25.1-0.20261005190000-0123456789ab",
		},
		{
			"devel with vcs",
			stamped("(devel)",
				debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
				debug.BuildSetting{Key: "vcs.time", Value: "2026-10-05T19:00:00Z"},
				debug.BuildSetting{Key: "vcs.modified", Value: "true"},
			),
			true,
			"devel 0123456789ab 2026-10-05T19:00:00Z dirty",
		},
		{"devel without vcs", stamped("(devel)"), true, "devel"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := version(c.bi, c.ok); got != c.want {
				t.Errorf("version = %q, want %q", got, c.want)
			}
		})
	}
}
