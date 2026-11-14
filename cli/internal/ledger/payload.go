package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Payload is a row's kind-specific detail as stored: the JSON object of its
// kind's payload struct, plus v. Only this package sees the keys; every
// other reader goes through Decode, so a key has one definition.
type Payload struct{ m map[string]any }

func (p Payload) MarshalJSON() ([]byte, error) {
	if p.m == nil {
		return []byte("{}"), nil
	}

	return json.Marshal(p.m)
}

func (p *Payload) UnmarshalJSON(raw []byte) error {
	p.m = nil

	return json.Unmarshal(raw, &p.m)
}

// ErrNewerVersion marks a row a newer f10 wrote: Decode returns what the
// current struct could read of it.
var ErrNewerVersion = errors.New("ledger: payload from a newer version")

// versionKey is the payload key the encoder sets; absent means version 1.
const versionKey = "v"

// A version is one shape of a kind's payload; up turns the previous
// version's value into this one, nil for version 1.
type version struct {
	typ reflect.Type
	up  func(any) any
}

// registry maps a kind to its versions, oldest first.
type registry map[string][]version

// kinds is every kind's payload: the contract writers and readers share.
var kinds = registry{}

// register gives kinds their version-1 struct T.
func register[T any](r registry, names ...string) {
	for _, k := range names {
		if _, ok := r[k]; ok {
			panic("ledger: kind registered twice: " + k)
		}

		r[k] = []version{{typ: reflect.TypeFor[T]()}}
	}
}

// upcast adds the next version of kind, New, read from rows of the
// current one, Old, through f. Old rows are never rewritten.
func upcast[Old, New any](r registry, kind string, f func(Old) New) {
	vs := r[kind]
	if len(vs) == 0 || vs[len(vs)-1].typ != reflect.TypeFor[Old]() {
		panic("ledger: upcast from a type that is not " + kind + "'s current one")
	}

	r[kind] = append(vs, version{typ: reflect.TypeFor[New](), up: func(v any) any {
		old, _ := v.(Old) // the registry holds Old as this kind's previous type

		return f(old)
	}})
}

// NewEvent is a row of kind carrying payload, which must be the kind's
// current struct; the caller fills the other columns.
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

	if t := reflect.TypeOf(payload); t != vs[len(vs)-1].typ {
		return Event{}, fmt.Errorf("ledger: %s takes %s, not %v", kind, vs[len(vs)-1].typ, t)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("ledger: %w", err)
	}

	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Event{}, fmt.Errorf("ledger: %w", err)
	}

	p.m[versionKey] = len(vs)

	return Event{Kind: kind, Payload: col(p)}, nil
}

// Decode reads e's payload as T, its kind's current struct, upcasting an
// older row. A row from a newer version decodes as far as T can read it and
// returns ErrNewerVersion with the value.
func Decode[T any](e Event) (T, error) { return decodeIn[T](kinds, e) }

func decodeIn[T any](r registry, e Event) (T, error) {
	v, err := r.decode(e, reflect.TypeFor[T]())

	t, _ := v.(T)

	return t, err
}

// decode reads e's payload into its kind's current struct; want, when not
// nil, must be that struct's type.
func (r registry) decode(e Event, want reflect.Type) (any, error) {
	vs, ok := r[e.Kind]
	if !ok {
		return nil, fmt.Errorf("ledger: unknown kind %q", e.Kind)
	}

	cur := vs[len(vs)-1].typ
	if want != nil && want != cur {
		return nil, fmt.Errorf("ledger: %s decodes to %s, not %s", e.Kind, cur, want)
	}

	raw, err := json.Marshal(e.Payload.Val)
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	n := e.Payload.Val.version()
	if n > len(vs) {
		ptr := reflect.New(cur)
		err := json.Unmarshal(raw, ptr.Interface())

		return ptr.Elem().Interface(), errors.Join(
			fmt.Errorf("%w: %s v%d, this f10 reads up to v%d", ErrNewerVersion, e.Kind, n, len(vs)), err)
	}

	ptr := reflect.New(vs[n-1].typ)
	err = json.Unmarshal(raw, ptr.Interface())

	v := ptr.Elem().Interface()
	for _, next := range vs[n:] {
		v = next.up(v)
	}

	if err != nil {
		err = fmt.Errorf("ledger: %s payload: %w", e.Kind, err)
	}

	return v, err
}

// version is the payload's v, 1 when absent or unreadable.
func (p Payload) version() int {
	var n int

	switch v := p.m[versionKey].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	}

	return max(n, 1)
}
