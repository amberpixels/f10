package ledger

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Schema is the JSON Schema of every kind's payload, every version, generated
// from the registered structs: docs/ledger.schema.json is its output.
func Schema() ([]byte, error) { return kinds.schema() }

func (r registry) schema() ([]byte, error) {
	defs := map[string]any{}

	for kind, vs := range r {
		refs := make([]any, 0, len(vs))

		for i, v := range vs {
			def, err := structSchema(v.typ, i+1)
			if err != nil {
				return nil, fmt.Errorf("ledger: %s: %w", kind, err)
			}

			defs[v.typ.Name()] = def
			refs = append(refs, map[string]any{"$ref": "#/$defs/" + v.typ.Name()})
		}

		defs[kind] = map[string]any{"oneOf": refs}
	}

	out, err := json.MarshalIndent(map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://github.com/amberpixels/f10/docs/ledger.schema.json",
		"title":   "Ledger payloads",
		"description": "The payload of each events row, by kind: $defs/<kind> lists its versions, oldest first. " +
			"docs/ledger.md is the spec.",
		"$defs": defs,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	return append(out, '\n'), nil
}

// structSchema is one version's object: its fields, and v, absent on rows
// written before versions existed, so required only from version 2.
func structSchema(t reflect.Type, n int) (map[string]any, error) {
	props := map[string]any{versionKey: map[string]any{"const": n}}
	required := []string{}

	if n > 1 {
		required = append(required, versionKey)
	}

	if err := addFields(t, props, &required); err != nil {
		return nil, err
	}

	return object(props, required), nil
}

// objectSchema is a nested struct as encoding/json writes it.
func objectSchema(t reflect.Type) (map[string]any, error) {
	props := map[string]any{}
	required := []string{}

	if err := addFields(t, props, &required); err != nil {
		return nil, err
	}

	return object(props, required), nil
}

func object(props map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// addFields adds t's fields: an embedded struct's inline, and each one
// required unless omitempty or omitzero.
func addFields(t reflect.Type, props map[string]any, required *[]string) error {
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct {
			if err := addFields(f.Type, props, required); err != nil {
				return err
			}

			continue
		}

		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}

		typ, err := typeSchema(f.Type)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}

		props[name] = typ

		if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") {
			*required = append(*required, name)
		}
	}

	return nil
}

// typeSchema covers the field types payloads use; a nil list or map is
// stored as null.
func typeSchema(t reflect.Type) (map[string]any, error) {
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Slice:
		items, err := typeSchema(t.Elem())
		if err != nil {
			return nil, err
		}

		return map[string]any{"type": []string{"array", "null"}, "items": items}, nil
	case reflect.Struct:
		return objectSchema(t)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map key %s", t.Key())
		}

		values, err := typeSchema(t.Elem())
		if err != nil {
			return nil, err
		}

		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": values}, nil
	default:
		return nil, fmt.Errorf("no schema for %s", t)
	}
}
