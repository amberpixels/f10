package config_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/facts"
)

// The config view copies Effective field by field, so a field added there
// fails here until the view shows it or this list says the view folds it
// into its own shape.
func TestConfigViewCoversEffective(t *testing.T) {
	folded := []string{
		"agentsOrigin",        // each agents entry carries its own origin
		"pacesOrigin",         // each paces entry carries its own origin
		"paceOrigin",          // the pace entry carries its origin
		"ceilingOrigin",       // each ceiling entry carries its own origin
		"ceilingExcept",       // the ceiling's last entry
		"ceilingExceptOrigin", // the ceiling's last entry's origin
	}

	shown := jsonKeys(reflect.TypeFor[configcmd.View]())

	for _, key := range jsonKeys(reflect.TypeFor[facts.Effective]()) {
		if !slices.Contains(shown, key) && !slices.Contains(folded, key) {
			t.Errorf("f10 config --json lacks Effective's %q", key)
		}
	}
}

func jsonKeys(typ reflect.Type) []string {
	var keys []string

	for f := range typ.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			keys = append(keys, name)
		}
	}

	return keys
}
