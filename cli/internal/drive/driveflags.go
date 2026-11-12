package drive

import (
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drivectl"
)

// flagUsage is the usage of flags, one bracket each, a flag and its alt
// as one choice.
func flagUsage(flags []drivectl.DriveFlag) string {
	one := func(f drivectl.DriveFlag) string {
		s := f.Name
		if f.Value != "" {
			s += " " + f.Value
		}

		if f.Many {
			s += " ..."
		}

		return s
	}

	var parts []string

	for _, f := range flags {
		if f.Alt != "" && slices.ContainsFunc(flags, func(g drivectl.DriveFlag) bool { return g.Name == f.Alt }) {
			continue
		}

		choice := []string{one(f)}

		for _, g := range flags {
			if g.Alt == f.Name {
				choice = append(choice, one(g))
			}
		}

		parts = append(parts, "["+strings.Join(choice, " | ")+"]")
	}

	return strings.Join(parts, " ")
}

// DriveUsage is drive's ArgsUsage, read off DriveFlags.
func DriveUsage() string {
	run := slices.DeleteFunc(
		slices.Clone(drivectl.DriveFlags),
		func(f drivectl.DriveFlag) bool { return f.Name == "--resume" },
	)

	return "<id>[:profile]... | <id> -- <id>  " + flagUsage(run) + "\n" +
		"   f10 drive --resume N " + flagUsage(drivectl.ResumeTunes()) + " | list [--all] | cancel N"
}
