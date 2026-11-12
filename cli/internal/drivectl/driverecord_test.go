package drivectl_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
)

func TestParseDriveSubcommands(t *testing.T) {
	cases := []struct {
		args []string
		want drivectl.DriveArgs
		err  string
	}{
		{args: []string{"list"}, want: drivectl.DriveArgs{List: true}},
		{args: []string{"list", "--all"}, want: drivectl.DriveArgs{List: true, All: true}},
		{args: []string{"list", "GH-1"}, err: "only --all"},
		{args: []string{"cancel", "3"}, want: drivectl.DriveArgs{Cancel: 3}},
		{args: []string{"cancel"}, err: "one drive number"},
		{args: []string{"cancel", "x"}, err: "not a drive number"},
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
		case got.List != c.want.List || got.All != c.want.All || got.Cancel != c.want.Cancel ||
			got.Resume != c.want.Resume || got.Parallel != c.want.Parallel || !slices.Equal(got.Answers, c.want.Answers):
			t.Errorf("%q = %+v, want %+v", c.args, got, c.want)
		}
	}
}
