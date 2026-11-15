package ledger

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Schema is the JSON Schema of every kind's payload, every version, generated
// from the payload messages' descriptors: docs/ledger.schema.json is its
// output.
func Schema() ([]byte, error) { return kinds.schema() }

func (r registry) schema() ([]byte, error) {
	defs := map[string]any{}

	for kind, vs := range r {
		refs := make([]any, 0, len(vs))

		for i, v := range vs {
			name := fmt.Sprintf("%s.v%d", kind, i+1)
			defs[name] = versionSchema(v.typ.Descriptor(), i+1, defs)
			refs = append(refs, defRef(name))
		}

		defs[kind] = map[string]any{"oneOf": refs}
	}

	out, err := json.MarshalIndent(map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://github.com/amberpixels/f10/docs/ledger.schema.json",
		"title":   "Ledger payloads",
		"description": "The payload of each events row, by kind: $defs/<kind> lists its versions, oldest first, " +
			"and $defs/<kind>.v<n> is version n. docs/ledger.md is the spec.",
		"$defs": defs,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	return append(out, '\n'), nil
}

// versionSchema is one version's object: its message's fields, and v, absent
// on rows written before versions existed, so required only from version 2.
// No other key is required, because rows written before payloads were
// messages leave empty keys out.
func versionSchema(md protoreflect.MessageDescriptor, n int, defs map[string]any) map[string]any {
	props := fieldSchemas(md, defs)
	props[versionKey] = map[string]any{"const": n}

	required := []string{}
	if n > 1 {
		required = append(required, versionKey)
	}

	return object(props, required)
}

func object(props map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// fieldSchemas is md's fields under their stored names, the proto names.
func fieldSchemas(md protoreflect.MessageDescriptor, defs map[string]any) map[string]any {
	props := map[string]any{}

	for i := range md.Fields().Len() {
		fd := md.Fields().Get(i)

		switch {
		case fd.IsMap():
			props[string(fd.Name())] = map[string]any{
				"type":                 []string{"object", "null"},
				"additionalProperties": valueSchema(fd.MapValue(), defs),
			}
		case fd.IsList():
			props[string(fd.Name())] = map[string]any{"type": []string{"array", "null"}, "items": valueSchema(fd, defs)}
		default:
			props[string(fd.Name())] = valueSchema(fd, defs)
		}
	}

	return props
}

// valueSchema is one value of fd; a nested message is a shared $defs entry
// under its full name.
func valueSchema(fd protoreflect.FieldDescriptor, defs map[string]any) map[string]any {
	switch fd.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return map[string]any{"type": "string"}
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}
	case protoreflect.EnumKind:
		// rows written before payloads were messages store an unset enum as ""
		names := []string{""}
		for i := range fd.Enum().Values().Len() {
			names = append(names, string(fd.Enum().Values().Get(i).Name()))
		}

		return map[string]any{"type": "string", "enum": names}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		md := fd.Message()
		if md.FullName() == "google.protobuf.Timestamp" {
			return map[string]any{"type": "string", "format": "date-time"}
		}

		name := string(md.FullName())
		if _, ok := defs[name]; !ok {
			defs[name] = nil // a recursive message refers to itself
			defs[name] = object(fieldSchemas(md, defs), []string{})
		}

		return defRef(name)
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "integer"}
	}
}

func defRef(name string) map[string]any { return map[string]any{"$ref": "#/$defs/" + name} }
