package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Payload is a row's kind-specific detail as stored: the protojson object of
// its kind's payload message, plus v. Only this package sees the keys; every
// other reader goes through Decode, so a key has one definition. It keeps
// the bytes as stored, so a read decodes them once, into the message.
type Payload struct{ raw json.RawMessage }

func (p Payload) MarshalJSON() ([]byte, error) {
	if len(p.raw) == 0 {
		return []byte("{}"), nil
	}

	return p.raw, nil
}

func (p *Payload) UnmarshalJSON(raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("ledger: payload is not JSON")
	}

	p.raw = append(json.RawMessage(nil), raw...) // the decoder reuses its buffer

	return nil
}

// ErrNewerVersion marks a row a newer f10 wrote: Decode returns what the
// current message could read of it.
var ErrNewerVersion = errors.New("ledger: payload from a newer version")

// versionKey is the payload key the encoder sets; absent means version 1.
const versionKey = "v"

// Payloads are stored as protojson under the proto field names with zero
// values written, so json_extract reads 0 or "" rather than NULL; reads skip
// v and any field a newer f10 added.
var (
	storeJSON = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}
	readJSON  = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// A version is one shape of a kind's payload; up turns the previous
// version's message into this one, nil when the two share a message.
type version struct {
	typ protoreflect.MessageType
	up  func(proto.Message) proto.Message
}

// registry maps a kind to its versions, oldest first.
type registry map[string][]version

// kinds is every kind's payload: the contract writers and readers share.
var kinds = registry{}

// register gives kinds their version-1 message T.
func register[T proto.Message](r registry, names ...string) {
	var zero T

	for _, k := range names {
		if _, ok := r[k]; ok {
			panic("ledger: kind registered twice: " + k)
		}

		r[k] = []version{{typ: zero.ProtoReflect().Type()}}
	}
}

// additive raises kind's current version to n with no new message: versions
// from before proto payloads that only added keys read with one message,
// and rows keep their number for the older f10s that still read it.
func additive(r registry, kind string, n int) {
	vs := r[kind]
	if len(vs) == 0 || n <= len(vs) {
		panic("ledger: additive version for " + kind + " is not ahead of its current one")
	}

	for len(vs) < n {
		vs = append(vs, version{typ: vs[len(vs)-1].typ})
	}

	r[kind] = vs
}

// upcast adds the next version of kind, New, a message of its own read from
// rows of the current one, Old, through f. Old rows are never rewritten.
func upcast[Old, New proto.Message](r registry, kind string, f func(Old) New) {
	var oldZero Old

	var newZero New

	vs := r[kind]
	if len(vs) == 0 || vs[len(vs)-1].typ != oldZero.ProtoReflect().Type() {
		panic("ledger: upcast from a type that is not " + kind + "'s current one")
	}

	r[kind] = append(vs, version{typ: newZero.ProtoReflect().Type(), up: func(m proto.Message) proto.Message {
		old, _ := m.(Old) // the registry holds Old as this kind's previous type

		return f(old)
	}})
}

// NewEvent is a row of kind carrying payload, which must be the kind's
// current message; the caller fills the other columns.
func NewEvent(kind string, payload any) (Event, error) { return kinds.event(kind, payload) }

// MustEvent is NewEvent for a payload known to match, as in a test.
func MustEvent(kind string, payload any) Event {
	e, err := NewEvent(kind, payload)
	if err != nil {
		panic(err)
	}

	return e
}

func (r registry) event(kind string, payload any) (Event, error) {
	vs, ok := r[kind]
	if !ok {
		return Event{}, fmt.Errorf("ledger: unknown kind %q", kind)
	}

	cur := vs[len(vs)-1].typ

	m, ok := payload.(proto.Message)
	if !ok || m.ProtoReflect().Type() != cur {
		return Event{}, fmt.Errorf("ledger: %s takes *%s, not %T", kind, cur.Descriptor().FullName(), payload)
	}

	raw, err := storeJSON.Marshal(m)
	if err != nil {
		// protojson refuses invalid UTF-8; losing the bad bytes beats losing the row
		raw, err = storeJSON.Marshal(validUTF8(m))
	}

	if err != nil {
		return Event{}, fmt.Errorf("ledger: %w", err)
	}

	// protojson varies its whitespace on purpose; stored bytes stay compact
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return Event{}, fmt.Errorf("ledger: %w", err)
	}

	raw = buf.Bytes()

	// a message marshals to an object: v goes in as its first key
	v := fmt.Sprintf(`{%q:%d`, versionKey, len(vs))
	if string(raw) != "{}" {
		v += ","
	}

	return Event{Kind: kind, Payload: col(Payload{raw: append([]byte(v), raw[1:]...)})}, nil
}

// Decode reads e's payload as T, its kind's current message, upcasting an
// older row. A row from a newer version decodes as far as T can read it and
// returns ErrNewerVersion with the value.
func Decode[T proto.Message](e Event) (T, error) { return decodeIn[T](kinds, e) }

// DecodeMessage is Decode for a reader that takes any kind, such as the API.
func DecodeMessage(e Event) (proto.Message, error) { return kinds.decode(e, nil) }

func decodeIn[T proto.Message](r registry, e Event) (T, error) {
	var zero T

	m, err := r.decode(e, zero.ProtoReflect().Type())

	// never nil, so a reader that ignores err reads zero values
	t, ok := m.(T)
	if !ok {
		t, _ = zero.ProtoReflect().Type().New().Interface().(T)
	}

	return t, err
}

// decode reads e's payload into its kind's current message; want, when not
// nil, must be that message's type.
func (r registry) decode(e Event, want protoreflect.MessageType) (proto.Message, error) {
	vs, ok := r[e.Kind]
	if !ok {
		return nil, fmt.Errorf("ledger: unknown kind %q", e.Kind)
	}

	cur := vs[len(vs)-1].typ
	if want != nil && want != cur {
		return nil, fmt.Errorf(
			"ledger: %s decodes to %s, not %s", e.Kind, cur.Descriptor().FullName(), want.Descriptor().FullName())
	}

	raw, _ := e.Payload.Val.MarshalJSON()
	n := e.Payload.Val.version()

	if n > len(vs) {
		m, err := unmarshal(raw, cur)

		return m, errors.Join(
			fmt.Errorf("%w: %s v%d, this f10 reads up to v%d", ErrNewerVersion, e.Kind, n, len(vs)), err)
	}

	m, err := unmarshal(raw, vs[n-1].typ)

	for _, next := range vs[n:] {
		if next.up != nil {
			m = next.up(m)
		}
	}

	if err != nil {
		err = fmt.Errorf("ledger: %s payload: %w", e.Kind, err)
	}

	return m, err
}

// unmarshal reads raw as a typ message. DiscardUnknown also reads an enum
// name this f10 does not know as unspecified: a newer f10's value, and the
// "" rows from before payloads were messages store for an unset enum.
func unmarshal(raw []byte, typ protoreflect.MessageType) (proto.Message, error) {
	m := typ.New().Interface()

	return m, readJSON.Unmarshal(raw, m)
}

// validUTF8 is a copy of m with each invalid UTF-8 sequence in its strings,
// nested ones and map keys included, replaced by U+FFFD.
func validUTF8(m proto.Message) proto.Message {
	c := proto.Clone(m)
	fixMessage(c.ProtoReflect())

	return c
}

func fixMessage(m protoreflect.Message) {
	type field struct {
		fd protoreflect.FieldDescriptor
		v  protoreflect.Value
	}

	var fields []field

	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		fields = append(fields, field{fd, v})

		return true
	})

	for _, f := range fields {
		switch {
		case f.fd.IsList():
			l := f.v.List()
			for i := range l.Len() {
				l.Set(i, fixValue(f.fd, l.Get(i)))
			}
		case f.fd.IsMap():
			fixMap(f.v.Map(), f.fd)
		default:
			m.Set(f.fd, fixValue(f.fd, f.v))
		}
	}
}

func fixMap(mp protoreflect.Map, fd protoreflect.FieldDescriptor) {
	type entry struct {
		k protoreflect.MapKey
		v protoreflect.Value
	}

	var entries []entry

	mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		entries = append(entries, entry{k, v})

		return true
	})

	for _, e := range entries {
		k := e.k
		if fd.MapKey().Kind() == protoreflect.StringKind {
			if s := e.k.String(); !utf8.ValidString(s) {
				mp.Clear(e.k)
				k = protoreflect.ValueOfString(strings.ToValidUTF8(s, "\uFFFD")).MapKey()
			}
		}

		mp.Set(k, fixValue(fd.MapValue(), e.v))
	}
}

func fixValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) protoreflect.Value {
	if fd.Kind() == protoreflect.StringKind {
		return protoreflect.ValueOfString(strings.ToValidUTF8(v.String(), "\uFFFD"))
	}

	if fd.Message() != nil {
		fixMessage(v.Message())
	}

	return v
}

// version is the payload's v, 1 when absent or unreadable.
func (p Payload) version() int {
	var head struct {
		V int `json:"v"`
	}

	_ = json.Unmarshal(p.raw, &head)

	return max(head.V, 1)
}
