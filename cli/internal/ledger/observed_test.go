package ledger

import (
	"reflect"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/observe"
)

func jsonTags(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}

	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous || name == "" || name == "-" {
			continue
		}

		out[name] = f.Type
	}

	return out
}

// An observed kind's payload carries every field of its observation but
// Meta, by the same key and type: a field the hook adds reaches the ledger.
func TestObservedPayloadsMatchTheirObservations(t *testing.T) {
	for obs, kind := range map[reflect.Type]string{
		reflect.TypeFor[observe.SkillStarted]():   RunStarted,
		reflect.TypeFor[observe.StepStarted]():    StepEntered,
		reflect.TypeFor[observe.Finished]():       TaskFinished,
		reflect.TypeFor[observe.SessionStopped](): SessionStopped,
		reflect.TypeFor[observe.IssueCreated]():   TaskCaptured,
		reflect.TypeFor[observe.PRCreated]():      PROpened,
		reflect.TypeFor[observe.Committed]():      Committed,
		reflect.TypeFor[observe.Pushed]():         Pushed,
		reflect.TypeFor[observe.WaitStarted]():    WaitStarted,
		reflect.TypeFor[observe.WaitEnded]():      WaitEnded,
		reflect.TypeFor[observe.PlanWritten]():    PlanSaved,
		reflect.TypeFor[observe.PlanArchived]():   PlanSuperseded,
	} {
		vs := kinds[kind]
		payload := jsonTags(vs[len(vs)-1].typ)

		for name, typ := range jsonTags(obs) {
			if payload[name] != typ {
				t.Errorf("%s.%s (%s) is not in %s's payload", obs.Name(), name, typ, kind)
			}
		}
	}
}
