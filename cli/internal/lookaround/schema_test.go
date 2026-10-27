package lookaround

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// docs/board.schema.json and the Go types agree both ways: every field a
// type marshals is a property of its $defs entry, and every property is a
// field. No validator library: the walk checks names, the fixtures the
// values.
type schemaDoc struct {
	Defs map[string]struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties bool                       `json:"additionalProperties"`
	} `json:"$defs"`
}

func readSchema(t *testing.T) schemaDoc {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "board.schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	var s schemaDoc
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	return s
}

func jsonFields(typ reflect.Type) []string {
	var names []string

	for f := range typ.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			continue
		}

		names = append(names, tag)
	}

	return names
}

func TestSchemaMatchesTypes(t *testing.T) {
	s := readSchema(t)

	for name, typ := range map[string]reflect.Type{
		"Board": reflect.TypeFor[Board](), "Project": reflect.TypeFor[Project](), "Window": reflect.TypeFor[Window](),
		"Source": reflect.TypeFor[Source](), "Row": reflect.TypeFor[Row](), "Task": reflect.TypeFor[Task](),
		"Who": reflect.TypeFor[Who](), "Where": reflect.TypeFor[Where](), "PRRef": reflect.TypeFor[PRRef](),
		"ReviewFacts": reflect.TypeFor[ReviewFacts](), "Next": reflect.TypeFor[Next](),
		"Feed": reflect.TypeFor[Feed](), "FeedRow": reflect.TypeFor[FeedRow](), "Quiet": reflect.TypeFor[Quiet](),
		"RelatedPR": reflect.TypeFor[RelatedPR](),
	} {
		def, ok := s.Defs[name]
		if !ok {
			t.Errorf("schema has no $defs/%s", name)

			continue
		}

		if def.AdditionalProperties {
			t.Errorf("%s allows additional properties", name)
		}

		fields := jsonFields(typ)

		for _, f := range fields {
			if _, ok := def.Properties[f]; !ok {
				t.Errorf("%s.%s is marshalled but not in the schema", name, f)
			}
		}

		for p := range def.Properties {
			if !slices.Contains(fields, p) {
				t.Errorf("schema %s.%s is no field of the type", name, p)
			}
		}

		for _, r := range def.Required {
			if _, ok := def.Properties[r]; !ok {
				t.Errorf("%s requires %s, which it does not declare", name, r)
			}
		}
	}
}

// Every derived board uses only declared keys and carries every required
// one, at every level.
func TestFixturesFitSchema(t *testing.T) {
	s := readSchema(t)

	paths, _ := filepath.Glob("testdata/derive/*.board.json")
	feeds, _ := filepath.Glob("testdata/feed/*.board.json")
	paths = append(paths, feeds...)

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}

		checkObject(t, s, "Board", doc, filepath.Base(path))
	}
}

// nested names the $defs entry a property's object is; inFeed overrides
// it inside the feed, whose rows are feed rows.
var (
	nested = map[string]string{
		"project": "Project", "window": "Window", "sources": "Source", "rows": "Row", "task": "Task",
		"who": "Who", "where": "Where", "pr": "PRRef", "review": "ReviewFacts", "next": "Next",
		"feed": "Feed", "quiet": "Quiet",
	}
	inFeed = map[string]string{"Feed.rows": "FeedRow"}
)

func checkObject(t *testing.T, s schemaDoc, def string, obj map[string]any, at string) {
	t.Helper()

	d := s.Defs[def]

	for key, v := range obj {
		if _, ok := d.Properties[key]; !ok {
			t.Errorf("%s: %s.%s is not in the schema", at, def, key)
		}

		child, ok := inFeed[def+"."+key]
		if !ok {
			child, ok = nested[key]
		}

		if !ok {
			continue
		}

		switch v := v.(type) {
		case map[string]any:
			checkObject(t, s, child, v, at+"."+key)
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					checkObject(t, s, child, m, at+"."+key+"[]")
				}
			}
		}
	}

	for _, r := range d.Required {
		if _, ok := obj[r]; !ok {
			t.Errorf("%s: %s lacks %s", at, def, r)
		}
	}
}
