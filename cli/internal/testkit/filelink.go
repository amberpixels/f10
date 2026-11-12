package testkit

import (
	"io"
	"testing"

	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/seam"
)

func WithTerminal(t *testing.T, on bool) {
	t.Helper()

	seam.Swap(t, &render.LinkTerminal, func(io.Writer) bool { return on })
}
