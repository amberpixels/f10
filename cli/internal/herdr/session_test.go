package herdr

import (
	"context"
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// Two clients calling one backend at once each reach Herdr in their own
// session, whichever the daemon registered last.
func TestSessionOnTheContextWinsOverTheBackends(t *testing.T) {
	b := Backend{Env: []string{"HERDR_ENV=1", "HERDR_SOCKET=newest"}}
	one := []string{"HERDR_ENV=1", "HERDR_SOCKET=one"}
	two := []string{"HERDR_ENV=1", "HERDR_SOCKET=two"}

	for _, c := range []struct {
		ctx  context.Context
		want []string
	}{
		{WithSession(t.Context(), one), one},
		{WithSession(t.Context(), two), two},
		{t.Context(), b.Env},
		{WithSession(t.Context(), nil), b.Env},
	} {
		if got := shell.EnvOf(b.scope(c.ctx)); !slices.Equal(got, c.want) {
			t.Errorf("scope = %v, want %v", got, c.want)
		}
	}
}
