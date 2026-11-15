package ledger

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// The API streams ledger rows as f10.v1.Event, so each kind's current
// message must be the payload case named after it, and each case a kind's.
func TestEveryKindIsAnEventCase(t *testing.T) {
	t.Parallel()

	payload := (&f10v1.Event{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	if payload == nil {
		t.Fatal("f10.v1.Event has no payload oneof")
	}

	covered := map[protoreflect.Name]bool{}

	for _, kind := range slices.Sorted(maps.Keys(kinds)) {
		name := protoreflect.Name(kind)
		if strings.HasPrefix(kind, "state_") {
			name = "claim" // every state_* kind shares the Claim payload
		}

		fd := payload.Fields().ByName(name)
		if fd == nil {
			t.Errorf("%s: f10.v1.Event has no payload case %q", kind, name)

			continue
		}

		covered[name] = true

		vs := kinds[kind]
		if cur := vs[len(vs)-1].typ.Descriptor(); cur != fd.Message() {
			t.Errorf("%s: stored as %s, streamed as %s", kind, cur.FullName(), fd.Message().FullName())
		}
	}

	for i := range payload.Fields().Len() {
		name := payload.Fields().Get(i).Name()
		if !covered[name] {
			t.Errorf("payload case %q names no ledger kind", name)
		}
	}
}
