package config_test

import (
	"strings"
	"testing"

	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
)

func TestCompletingFlow(t *testing.T) {
	for args, want := range map[string]bool{
		"f10 start --flow --generate-shell-completion":       true,
		"f10 drive GH-1 --flow --generate-shell-completion":  true,
		"f10 start --local --generate-shell-completion":      false,
		"f10 start --flow local --generate-shell-completion": false,
	} {
		if got := configcmd.CompletingFlow(strings.Fields(args)); got != want {
			t.Errorf("%q = %v, want %v", args, got, want)
		}
	}
}
