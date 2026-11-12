package main

import (
	"strings"
	"testing"
)

func TestBoardWindowArgs(t *testing.T) {
	for _, args := range [][]string{{"today", "yesterday"}, {"--since", "today", "yesterday"}} {
		err := runApp(t, append([]string{"board"}, args...)...).err
		if err == nil || !strings.Contains(err.Error(), "window") {
			t.Errorf("%v: err = %v, want the window refused", args, err)
		}
	}
}
