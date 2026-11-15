package pain

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

var since = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func ev(task, kind string, p any) ledger.Event {
	e := ledger.MustEvent(kind, p)
	e.At, e.Session, e.Task = since.Add(24*time.Hour), "s-"+task, task

	return e
}

func rounds(task string, n int) []ledger.Event {
	var out []ledger.Event
	for r := 1; r <= n; r++ {
		out = append(out, ev(task, ledger.ReviewRoundOpened, &ledger.ReviewRoundOpenedPayload{Round: strconv.Itoa(r)}))
	}

	return out
}

func verdict(task, finding, cat, word string) ledger.Event {
	return ev(task, ledger.FindingVerdict,
		&ledger.FindingVerdictPayload{Round: "1", Finding: finding, Category: cat, Verdict: word})
}

func halt(task, reason string) ledger.Event {
	return ev(task, ledger.TaskHalted, &ledger.TaskHaltedPayload{Skill: "ship", Reason: reason})
}

// fixture is the task's: a/ had three tasks of 3+ rounds and two halts,
// b/ more churn but one round each.
func fixture() Input {
	var events []ledger.Event
	for _, id := range []string{"FT-1", "FT-2", "FT-3"} {
		events = append(events, rounds(id, 3)...)
		events = append(events, verdict(id, "1", "tests and docs", "fix"), verdict(id, "2", "naming", "skip"))
	}

	events = append(events, halt("FT-1", "catchup conflict"), halt("FT-2", "no progress at review"))

	b := []string{"FT-4", "FT-5", "FT-6", "FT-7", "FT-8"}
	for _, id := range b {
		events = append(events, rounds(id, 1)...)
		events = append(events, verdict(id, "1", "naming", "skip"))
	}

	return Input{
		Areas:  []AreaTasks{{Dir: "a", Tasks: []string{"FT-1", "FT-2", "FT-3"}}, {Dir: "b", Tasks: b}},
		Events: events,
		Since:  since,
	}
}

func TestPainListsTheCostlyAreaNotTheBusyOne(t *testing.T) {
	m := Build(fixture())

	if len(m.Areas) != 1 || m.Areas[0].Dir != "a" {
		t.Fatalf("areas = %+v, want a alone", m.Areas)
	}

	line := m.Areas[0].Line
	for _, want := range []string{
		"a: 3.0 review rounds vs 1.0 (max 3)",
		"2 halts (FT-1: catchup conflict; FT-2: no progress at review)",
		`"tests and docs" fixed in 3 (FT-1, FT-2, FT-3)`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}

	if strings.Contains(line, "naming") {
		t.Errorf("a category only skipped recurs: %q", line)
	}
}

// Two tasks of high rounds are an anecdote, not a median.
func TestSmallAreaGetsNoMedian(t *testing.T) {
	in := fixture()
	in.Areas = append(in.Areas, AreaTasks{Dir: "c", Tasks: []string{"FT-9", "FT-10"}})
	in.Events = append(in.Events, rounds("FT-9", 6)...)
	in.Events = append(in.Events, rounds("FT-10", 6)...)

	for _, a := range Build(in).Areas {
		if a.Dir == "c" {
			t.Errorf("c listed on two tasks: %q", a.Line)
		}
	}
}

func TestRegressionLineage(t *testing.T) {
	in := fixture()
	in.Events = append(in.Events, ev("FT-30", ledger.RegressionFound,
		&ledger.RegressionFoundPayload{OriginTask: "FT-4", Class: "missed", Lines: []string{"b/x.go:1-2"}}))

	m := Build(in)

	if len(m.Lineages) != 1 || m.Lineages[0].Origin != "FT-4" || !slices.Equal(m.Lineages[0].Bugs, []string{"FT-30"}) {
		t.Errorf("lineages = %+v, want FT-4 to FT-30", m.Lineages)
	}

	var b *Area
	for i := range m.Areas {
		if m.Areas[i].Dir == "b" {
			b = &m.Areas[i]
		}
	}

	if b == nil || !strings.Contains(b.Line, "1 regression: FT-30 from FT-4 (missed)") {
		t.Errorf("b's line = %+v, want its regression", b)
	}
}

func TestNoLedgerNoPain(t *testing.T) {
	m := Build(Input{Areas: []AreaTasks{{Dir: "a", Tasks: []string{"FT-1"}}}, Since: since})
	if got := m.Lines(); !slices.Equal(got, []string{NoPain}) {
		t.Errorf("lines = %q", got)
	}
}

// A halt with no reason keeps its place, so later reasons stay on their halts.
func TestHaltWithoutReasonKeepsItsPlace(t *testing.T) {
	in := fixture()
	in.Events = append(in.Events, halt("FT-3", ""), halt("FT-3", "red verify"))

	line := Build(in).Areas[0].Line
	if !strings.Contains(line, "FT-3: no reason recorded; FT-3: red verify") {
		t.Errorf("halts out of order: %q", line)
	}
}
