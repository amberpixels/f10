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

// DriveUsage is start's flags for a flow start, read off DriveFlags.
func DriveUsage() string {
	run := slices.DeleteFunc(
		slices.Clone(drivectl.DriveFlags),
		func(f drivectl.DriveFlag) bool { return f.Internal },
	)

	return flagUsage(run)
}

// ResumeUsage is the flags a resume takes.
func ResumeUsage() string { return flagUsage(drivectl.ResumeTunes()) }
