package ledger

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// The API streams ledger rows as f10.v1.Event, and the ledger will store
// those messages as protojson, so each kind's current struct and its
// payload message must agree key for key.
func TestProtoPayloadParity(t *testing.T) {
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
		checkParity(t, kind, vs[len(vs)-1].typ, fd.Message())
	}

	// the ask events are the asks inbox's, which the ledger does not record yet
	for i := range payload.Fields().Len() {
		name := payload.Fields().Get(i).Name()
		if !covered[name] && name != "ask_raised" && name != "ask_answered" {
			t.Errorf("payload case %q names no ledger kind", name)
		}
	}
}

// checkParity fails where the struct's JSON keys, embedded structs
// flattened, and the message's fields differ in name or in shape.
func checkParity(t *testing.T, at string, typ reflect.Type, msg protoreflect.MessageDescriptor) {
	t.Helper()

	keys := map[string]reflect.Type{}
	jsonKeys(typ, keys)

	for key, ft := range keys {
		fd := msg.Fields().ByName(protoreflect.Name(key))
		if fd == nil {
			t.Errorf("%s: %s has no field %q", at, msg.FullName(), key)

			continue
		}

		checkField(t, at+"."+key, ft, fd)
	}

	for i := range msg.Fields().Len() {
		if name := string(msg.Fields().Get(i).Name()); keys[name] == nil {
			t.Errorf("%s: %s.%s has no key in the stored payload", at, msg.FullName(), name)
		}
	}
}

func checkField(t *testing.T, at string, ft reflect.Type, fd protoreflect.FieldDescriptor) {
	t.Helper()

	switch {
	case fd.IsMap():
		if ft.Kind() != reflect.Map {
			t.Errorf("%s: a map in proto, %s in Go", at, ft)

			return
		}

		checkField(t, at+"[]", ft.Elem(), fd.MapValue())
	case fd.IsList():
		if ft.Kind() != reflect.Slice {
			t.Errorf("%s: repeated in proto, %s in Go", at, ft)

			return
		}

		checkScalar(t, at, ft.Elem(), fd)
	default:
		checkScalar(t, at, ft, fd)
	}
}

// checkScalar matches one value's kind; a message recurses. Counts must be
// int32, since protojson writes int64 as a string the ledger never stored.
func checkScalar(t *testing.T, at string, ft reflect.Type, fd protoreflect.FieldDescriptor) {
	t.Helper()

	want := map[reflect.Kind]protoreflect.Kind{
		reflect.String: protoreflect.StringKind,
		reflect.Bool:   protoreflect.BoolKind,
		reflect.Int:    protoreflect.Int32Kind,
		reflect.Struct: protoreflect.MessageKind,
	}

	k, ok := want[ft.Kind()]
	if !ok || fd.Kind() != k {
		t.Errorf("%s: %s in proto, %s in Go", at, fd.Kind(), ft)

		return
	}

	if k == protoreflect.MessageKind {
		checkParity(t, at, ft, fd.Message())
	}
}

// jsonKeys collects t's JSON keys, an anonymous struct's keys flattened
// into its parent's as encoding/json does.
func jsonKeys(t reflect.Type, into map[string]reflect.Type) {
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}

		if f.Anonymous && name == "" {
			jsonKeys(f.Type, into)

			continue
		}

		if name == "" {
			name = f.Name
		}

		into[name] = f.Type
	}
}
