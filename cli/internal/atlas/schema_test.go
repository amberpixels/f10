package atlas

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// docs/atlas.schema.json is checked by a small walker instead of a validator
// library. It knows only the keywords below, and the schema may use no other,
// so a keyword it would silently skip fails the test instead.
var keywords = []string{
	"$schema", "$id", "title", "description", "$ref", "$defs", "type", "properties", "required",
	"additionalProperties", "items", "const", "enum", "pattern", "format", "oneOf", "minimum",
}

type schema = map[string]any

func docsPath(name string) string {
	return filepath.Join("..", "..", "..", "docs", name)
}

func readSchema(t *testing.T) schema {
	t.Helper()

	raw, err := os.ReadFile(docsPath("atlas.schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	var s schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	return s
}

type walker struct {
	root schema
	errs []string
}

func (w *walker) fail(at, format string, args ...any) {
	w.errs = append(w.errs, at+": "+fmt.Sprintf(format, args...))
}

func (w *walker) resolve(s schema) schema {
	ref, ok := s["$ref"].(string)
	if !ok {
		return s
	}

	name, _ := strings.CutPrefix(ref, "#/$defs/")
	def, _ := w.root["$defs"].(map[string]any)[name].(map[string]any)

	return def
}

// oneOf branches are told apart by their kind const, as every oneOf in this
// schema is; matching by trial would hide which branch a document meant.
func (w *walker) pick(branches []any, v any, at string) schema {
	obj, _ := v.(map[string]any)
	for _, b := range branches {
		s := w.resolve(b.(map[string]any))
		props, _ := s["properties"].(map[string]any)
		kind, _ := props["kind"].(map[string]any)

		if kind != nil && obj != nil && kind["const"] == obj["kind"] {
			return s
		}
	}

	w.fail(at, "kind %v matches no oneOf branch", obj["kind"])

	return nil
}

func (w *walker) check(s schema, v any, at string) {
	s = w.resolve(s)
	if s == nil {
		w.fail(at, "unresolved $ref")

		return
	}

	if branches, ok := s["oneOf"].([]any); ok {
		if picked := w.pick(branches, v, at); picked != nil {
			w.check(picked, v, at)
		}

		return
	}

	if c, ok := s["const"]; ok && c != v {
		w.fail(at, "%v is not %v", v, c)
	}

	if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, v) {
		w.fail(at, "%v is not one of %v", v, enum)
	}

	if typ, ok := s["type"].(string); ok && !hasType(typ, v) {
		w.fail(at, "%v is not a %s", v, typ)

		return
	}

	switch v := v.(type) {
	case string:
		w.checkString(s, v, at)
	case float64:
		if minimum, ok := s["minimum"].(float64); ok && v < minimum {
			w.fail(at, "%v is below %v", v, minimum)
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, item := range v {
				w.check(items, item, fmt.Sprintf("%s[%d]", at, i))
			}
		}
	case map[string]any:
		w.checkObject(s, v, at)
	}
}

func (w *walker) checkString(s schema, v, at string) {
	if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(v) {
		w.fail(at, "%q does not match %s", v, p)
	}

	if s["format"] == "date-time" {
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			w.fail(at, "%q is not a date-time", v)
		}
	}
}

func (w *walker) checkObject(s schema, obj map[string]any, at string) {
	props, _ := s["properties"].(map[string]any)

	for key, val := range obj {
		if p, ok := props[key].(map[string]any); ok {
			w.check(p, val, at+"."+key)

			continue
		}

		switch extra := s["additionalProperties"].(type) {
		case map[string]any:
			w.check(extra, val, at+"."+key)
		case bool:
			if !extra {
				w.fail(at, "%s is not in the schema", key)
			}
		}
	}

	required, _ := s["required"].([]any)
	for _, r := range required {
		if _, ok := obj[r.(string)]; !ok {
			w.fail(at, "lacks %s", r)
		}
	}
}

func hasType(typ string, v any) bool {
	switch typ {
	case "object":
		_, ok := v.(map[string]any)

		return ok
	case "array":
		_, ok := v.([]any)

		return ok
	case "string":
		_, ok := v.(string)

		return ok
	case "boolean":
		_, ok := v.(bool)

		return ok
	case "integer":
		n, ok := v.(float64)

		return ok && n == math.Trunc(n)
	}

	return false
}

func validate(t *testing.T, s schema, def string, doc any) []string {
	t.Helper()

	w := &walker{root: s}
	w.check(schema{"$ref": "#/$defs/" + def}, doc, def)

	return w.errs
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	return doc
}

// Every schema object uses only the keywords the walker checks, and every
// object closes itself.
func TestSchemaUsesKnownKeywords(t *testing.T) {
	t.Parallel()

	var walk func(s schema, at string)
	walk = func(s schema, at string) {
		for key, val := range s {
			if !slices.Contains(keywords, key) {
				t.Errorf("%s uses %s, which the walker does not check", at, key)
			}

			switch key {
			case "properties", "$defs":
				for name, sub := range val.(map[string]any) {
					walk(sub.(map[string]any), at+"."+name)
				}
			case "items", "additionalProperties":
				if sub, ok := val.(map[string]any); ok {
					walk(sub, at+"."+key)
				}
			case "oneOf":
				for i, sub := range val.([]any) {
					walk(sub.(map[string]any), fmt.Sprintf("%s.oneOf[%d]", at, i))
				}
			}
		}

		if s["type"] == "object" {
			if _, ok := s["additionalProperties"]; !ok {
				t.Errorf("%s does not set additionalProperties", at)
			}
		}
	}

	walk(readSchema(t), "schema")
}

func TestSpecExampleFitsSchema(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(docsPath("atlas.md"))
	if err != nil {
		t.Fatal(err)
	}

	_, rest, ok := strings.Cut(string(raw), "```json\n")
	example, _, closed := strings.Cut(rest, "\n```")

	if !ok || !closed {
		t.Fatal("docs/atlas.md has no json example")
	}

	for _, e := range validate(t, readSchema(t), "Atlas", decode(t, []byte(example))) {
		t.Error(e)
	}
}

func TestFixturesFitSchema(t *testing.T) {
	t.Parallel()

	s := readSchema(t)
	paths, _ := filepath.Glob("testdata/*.atlas.json")

	if len(paths) == 0 {
		t.Fatal("no atlas fixtures")
	}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		for _, e := range validate(t, s, "Atlas", decode(t, raw)) {
			t.Errorf("%s: %s", filepath.Base(path), e)
		}
	}
}

// The declared file's frontmatter, read as YAML and seen as JSON, fits
// $defs/Declared.
func TestDeclaredFitsSchema(t *testing.T) {
	t.Parallel()

	s := readSchema(t)
	paths, _ := filepath.Glob("testdata/*.declared.md")

	if len(paths) == 0 {
		t.Fatal("no declared fixtures")
	}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, rest, _ := strings.Cut(string(raw), "---\n")
		front, _, ok := strings.Cut(rest, "\n---")

		if !ok {
			t.Fatalf("%s has no frontmatter", path)
		}

		var doc any
		if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
			t.Fatal(err)
		}

		asJSON, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}

		for _, e := range validate(t, s, "Declared", decode(t, asJSON)) {
			t.Errorf("%s: %s", filepath.Base(path), e)
		}
	}
}

// A walker that accepts everything would pass the tests above, so each of
// these must fail; the anonymous node's closed shape is the privacy rule.
func TestSchemaRejects(t *testing.T) {
	t.Parallel()

	s := readSchema(t)
	doc := func(node, edge string) string {
		return `{"version": 1, "generated_at": "2026-11-11T12:00:00Z", "nodes": [` + node + `], "edges": [` + edge + `]}`
	}

	for name, raw := range map[string]string{
		"anonymous with a name":  doc(`{"id": "anon/x", "kind": "anonymous", "alias": "x", "name": "secret"}`, ""),
		"anonymous with a date":  doc(`{"id": "anon/x", "kind": "anonymous", "alias": "x", "first_commit": "2026-01-01T00:00:00Z"}`, ""),
		"anonymous id not alias": doc(`{"id": "github.com/acme/secret", "kind": "anonymous", "alias": "x"}`, ""),
		"unknown status":         doc(`{"id": "local/w", "kind": "public", "name": "w", "status": "dormant"}`, ""),
		"unknown node kind":      doc(`{"id": "local/w", "kind": "private", "name": "w"}`, ""),
		"link without relation":  doc("", `{"from": "local/a", "to": "local/b", "kind": "link", "source": "declared"}`),
		"depends from declared":  doc("", `{"from": "local/a", "to": "local/b", "kind": "depends", "source": "declared", "manifest": "go.mod"}`),
		"bad month":              doc(`{"id": "local/w", "kind": "public", "name": "w", "commits_per_month": [{"month": "2026-13", "commits": 1}]}`, ""),
		"wrong version":          strings.Replace(doc("", ""), `"version": 1`, `"version": 2`, 1),
	} {
		if errs := validate(t, s, "Atlas", decode(t, []byte(raw))); len(errs) == 0 {
			t.Errorf("%s: the schema accepts it", name)
		}
	}
}
