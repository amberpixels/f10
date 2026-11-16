package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// A schema is one JSON Schema node. encoding/json sorts map keys, so the
// generated literal is stable from run to run.
type schema = map[string]any

// inputSchema is a request message as the JSON Schema of the arguments
// protojson reads into it, with proto field names. A nested message is a
// $defs entry, so a recursive one terminates.
func inputSchema(m *protogen.Message) (string, error) {
	b := builder{defs: map[string]schema{}}

	root := b.object(m)
	if len(b.defs) > 0 {
		root["$defs"] = b.defs
	}

	raw, err := json.Marshal(root)
	if err != nil {
		return "", err
	}

	return string(raw), nil
}

type builder struct {
	defs map[string]schema
}

// object is a message's own shape: its fields, nothing unknown accepted, so a
// misspelled field fails the call instead of being dropped.
func (b *builder) object(m *protogen.Message) schema {
	props := schema{}

	for _, f := range m.Fields {
		if localOnly(f) {
			continue
		}

		props[string(f.Desc.Name())] = b.field(f)
	}

	out := schema{"type": "object", "properties": props, "additionalProperties": false}
	if d := commentText(m.Comments.Leading); d != "" {
		out["description"] = d
	}

	return out
}

// callerContext is the message the daemon honors only from the unix socket:
// where a local command runs, which a model has no way to know.
const callerContext = "CallerContext"

// localOnly is a field a tool leaves out: the caller's context, which
// additionalProperties then refuses if a model passes it anyway.
func localOnly(f *protogen.Field) bool {
	return f.Message != nil && f.Message.Desc.Name() == callerContext
}

func (b *builder) field(f *protogen.Field) schema {
	var s schema

	switch {
	case f.Desc.IsMap():
		s = schema{"type": "object", "additionalProperties": b.single(f.Message.Fields[1])}
	case f.Desc.IsList():
		s = schema{"type": "array", "items": b.single(f)}
	default:
		s = b.single(f)
	}

	desc := commentText(f.Comments.Leading)

	if o := f.Oneof; o != nil && !o.Desc.IsSynthetic() {
		names := make([]string, len(o.Fields))
		for i, of := range o.Fields {
			names[i] = string(of.Desc.Name())
		}

		desc = strings.TrimSpace(desc + " Set at most one of " + strings.Join(names, ", ") + ".")
	}

	return describe(s, desc)
}

// single is one value of f, ignoring whether f repeats.
func (b *builder) single(f *protogen.Field) schema {
	switch f.Desc.Kind() {
	case protoreflect.BoolKind:
		return schema{"type": "boolean"}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return schema{"type": "integer"}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		// protojson writes 64-bit integers as strings and reads either
		return schema{"type": []string{"integer", "string"}}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return schema{"type": "number"}
	case protoreflect.StringKind:
		return schema{"type": "string"}
	case protoreflect.BytesKind:
		return schema{"type": "string", "contentEncoding": "base64"}
	case protoreflect.EnumKind:
		return enum(f.Enum)
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return b.message(f.Message)
	}

	return schema{}
}

// message is a reference to m's $defs entry, or the JSON shape protojson
// gives a well-known type.
func (b *builder) message(m *protogen.Message) schema {
	full := string(m.Desc.FullName())

	if s, ok := wellKnown(full); ok {
		return s
	}

	if _, ok := b.defs[full]; !ok {
		b.defs[full] = schema{} // claimed first, so a recursive field stops here
		b.defs[full] = b.object(m)
	}

	return schema{"$ref": "#/$defs/" + full}
}

func wellKnown(full string) (schema, bool) {
	switch full {
	case "google.protobuf.Timestamp":
		return schema{"type": "string", "format": "date-time"}, true
	case "google.protobuf.Duration":
		return schema{"type": "string", "description": "a duration in seconds, such as 90s or 1.5s"}, true
	case "google.protobuf.Struct", "google.protobuf.Empty", "google.protobuf.Any":
		return schema{"type": "object"}, true
	case "google.protobuf.Value":
		return schema{}, true
	case "google.protobuf.ListValue":
		return schema{"type": "array"}, true
	case "google.protobuf.FieldMask":
		return schema{"type": "string", "description": "comma-separated field paths"}, true
	case "google.protobuf.BoolValue":
		return schema{"type": "boolean"}, true
	case "google.protobuf.StringValue":
		return schema{"type": "string"}, true
	case "google.protobuf.BytesValue":
		return schema{"type": "string", "contentEncoding": "base64"}, true
	case "google.protobuf.Int32Value", "google.protobuf.UInt32Value":
		return schema{"type": "integer"}, true
	case "google.protobuf.Int64Value", "google.protobuf.UInt64Value":
		return schema{"type": []string{"integer", "string"}}, true
	case "google.protobuf.FloatValue", "google.protobuf.DoubleValue":
		return schema{"type": "number"}, true
	}

	return nil, false
}

// enum is the value names, and those with a comment described, so a model
// can tell them apart.
func enum(e *protogen.Enum) schema {
	names := make([]string, len(e.Values))

	var notes []string

	for i, v := range e.Values {
		names[i] = string(v.Desc.Name())
		if c := commentText(v.Comments.Leading); c != "" {
			notes = append(notes, fmt.Sprintf("%s: %s", v.Desc.Name(), c))
		}
	}

	return describe(schema{"type": "string", "enum": names}, strings.Join(notes, "; "))
}

// describe adds desc to s, after any description s already has.
func describe(s schema, desc string) schema {
	if desc == "" {
		return s
	}

	if _, ref := s["$ref"]; ref {
		// a $ref's siblings are ignored by older drafts, so wrap it
		return schema{"allOf": []any{s}, "description": desc}
	}

	out := maps.Clone(s)

	if prev, ok := out["description"].(string); ok && prev != "" {
		desc = desc + " (" + prev + ")"
	}

	out["description"] = desc

	return out
}
