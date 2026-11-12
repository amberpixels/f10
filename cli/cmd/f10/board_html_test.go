package main

import (
	"strings"
	"testing"
)

func TestBoardFormatFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"json and html", []string{"--json", "--html"}, "exclusive"},
		{"out alone", []string{"--out", "board.html"}, "--out writes"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := runApp(t, append([]string{"board"}, c.args...)...).err
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
}
