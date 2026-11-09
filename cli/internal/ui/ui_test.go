package ui

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

type fakeTerm struct{ bytes.Buffer }

func (fakeTerm) Terminal() bool { return true }

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, map[string]int{"a": 1}); err != nil || b.String() != "{\n  \"a\": 1\n}\n" {
		t.Errorf("JSON = %q, %v", b.String(), err)
	}

	if err := JSON(&b, math.Inf(1)); err == nil || !strings.HasPrefix(err.Error(), "encoding JSON: ") {
		t.Errorf("JSON error = %v", err)
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is a terminal")
	}

	if !IsTerminal(&fakeTerm{}) {
		t.Error("a writer that says it is a terminal is not one")
	}

	if Width(&bytes.Buffer{}) != 0 {
		t.Error("a buffer has a width")
	}
}

func TestPlainWidth(t *testing.T) {
	t.Setenv("COLUMNS", "")

	if PlainWidth() != 100 {
		t.Errorf("PlainWidth = %d, want 100", PlainWidth())
	}

	t.Setenv("COLUMNS", "72")

	if PlainWidth() != 72 {
		t.Errorf("PlainWidth = %d, want 72", PlainWidth())
	}
}

func TestExit(t *testing.T) {
	err := Exit(ExitDeclined, "%s is here", "FT-1")

	var ec cli.ExitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != 3 || err.Error() != "f10: FT-1 is here" {
		t.Errorf("Exit = %v", err)
	}

	var b bytes.Buffer

	Warnf(&b, "already %s", "set")

	if b.String() != "f10: already set\n" {
		t.Errorf("Warnf = %q", b.String())
	}
}
