package drivectl_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
)

func TestParseDriveResumeAndBase(t *testing.T) {
	cases := []struct {
		args []string
		want drivectl.DriveArgs
		err  string
	}{
		{args: []string{"list"}, want: drivectl.DriveArgs{Tasks: []string{"list"}, Every: drivectl.DefaultEvery}},
		{args: []string{"GH-1", "--plan"}, want: drivectl.DriveArgs{
			Tasks: []string{"GH-1"}, PlanOnly: true, Every: drivectl.DefaultEvery,
		}},
		{args: []string{"GH-1", "--after", "GH-2"}, want: drivectl.DriveArgs{
			Tasks: []string{"GH-1"}, After: "GH-2", Every: drivectl.DefaultEvery,
		}},
		{args: []string{"GH-1", "--base=release"}, want: drivectl.DriveArgs{
			Tasks: []string{"GH-1"}, Base: "release", Every: drivectl.DefaultEvery,
		}},
		{args: []string{"GH-1", "--after", "GH-2", "--base", "main"}, err: "exclusive"},
		{args: []string{"GH-1", "--plan", "--local"}, err: "exclusive"},
		{args: []string{"GH-1", "GH-3", "--after", "GH-2"}, err: "start that task alone"},
		{args: []string{"GH-1", "--", "GH-3", "--base", "main"}, err: "start that task alone"},
		{args: []string{"--resume", "2", "--plan"}, err: "takes only"},
		{args: []string{"--resume", "2", "--answer", "1. yes"}, want: drivectl.DriveArgs{
			Resume: 2, Answers: []string{"1. yes"}, Every: drivectl.DefaultEvery,
		}},
		{args: []string{"--resume=2", "GH-1"}, err: "takes only --answer, --every and --parallel"},
		{
			args: []string{"--resume", "2", "--parallel=3"},
			want: drivectl.DriveArgs{Resume: 2, Parallel: 3, Every: drivectl.DefaultEvery},
		},
	}

	for _, c := range cases {
		got, err := drivectl.ParseDriveArgs(c.args)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.args, err, c.err)
			}
		case err != nil:
			t.Errorf("%q: %v", c.args, err)
		case !slices.Equal(got.Tasks, c.want.Tasks) || got.PlanOnly != c.want.PlanOnly ||
			got.After != c.want.After || got.Base != c.want.Base ||
			got.Resume != c.want.Resume || got.Parallel != c.want.Parallel || !slices.Equal(got.Answers, c.want.Answers):
			t.Errorf("%q = %+v, want %+v", c.args, got, c.want)
		}
	}
}
