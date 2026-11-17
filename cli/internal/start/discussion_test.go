package start

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// Several ids go to one agent only in a discussion, which takes no base
// and no PR; each refusal comes before any task is looked up.
func TestDiscussionRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		req  Request
		pick *hostops.Pickup
		want string
	}{
		{"a flow start", Request{Also: []string{"GH-2"}}, nil, "only with --discuss"},
		{"a base", Request{Mode: launch.ModeDiscuss, Also: []string{"GH-2"}, After: "GH-9"}, nil, "has none"},
		{"a PR", Request{Mode: launch.ModeDiscuss, Also: []string{"GH-2"}}, &hostops.Pickup{}, "picked up alone"},
	} {
		_, err := settleDiscussion(t.Context(), nil, ref.Ref{ID: "GH-1"}, c.pick, c.req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
