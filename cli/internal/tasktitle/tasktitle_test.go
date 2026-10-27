package tasktitle

import "testing"

func TestParse(t *testing.T) {
	for _, c := range []struct {
		raw string
		ok  bool
	}{
		{"{id}: {title}", true},
		{"[{n}] {title}", true},
		{"something--{id}--{title}", true},
		{"{title}", true},
		{"{id}", false},
		{"{title} {title}", false},
		{"{id}: {name}", false},
	} {
		if _, err := Parse(c.raw); (err == nil) != c.ok {
			t.Errorf("Parse(%q) error = %v, want ok %v", c.raw, err, c.ok)
		}
	}
}

func TestRender(t *testing.T) {
	for _, c := range []struct {
		tmpl Template
		want string
	}{
		{"", "Add {id} support"},
		{"{id}: {title}", "FT-132: Add {id} support"},
		{"#{n} {title}", "#132 Add {id} support"},
	} {
		if got := c.tmpl.Render("FT-132", "132", "Add {id} support"); got != c.want {
			t.Errorf("%q.Render = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

func TestStrip(t *testing.T) {
	for _, c := range []struct {
		name  string
		tmpl  Template
		title string
		want  string
	}{
		{"no template strips nothing", "", "FT-12: Drive", "FT-12: Drive"},
		{"id prefix", "{id}: {title}", "FT-12: Drive halts", "Drive halts"},
		{"id prefix in another case", "{id}: {title}", "ft-12 - Drive halts", "Drive halts"},
		{"bare title stays", "{id}: {title}", "Drive halts", "Drive halts"},
		{"a longer id is not this one", "{id}: {title}", "FT-123: Drive", "FT-123: Drive"},
		{"the id alone stays whole", "{id}: {title}", "FT-12:", "FT-12:"},
		{"a bracket opens the title", "{id}: {title}", "FT-12: (wip) Drive", "(wip) Drive"},
		{"number prefix", "{n}. {title}", "12. Drive", "Drive"},
		{"hash number prefix", "#{n} {title}", "#12 Drive", "Drive"},
		{"a non-prefix template is left as is", "x--{id}--{title}", "x--FT-12--Drive", "x--FT-12--Drive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tmpl.Strip(c.title, "FT-12", "12"); got != c.want {
				t.Errorf("Strip(%q) = %q, want %q", c.title, got, c.want)
			}
		})
	}
}

func TestApplied(t *testing.T) {
	tmpl := Template("{id}: {title}")

	for title, want := range map[string]bool{
		"FT-12: Drive": true,
		"Drive":        false,
		"FT-12 Drive":  false,
	} {
		if got := tmpl.Applied(title, "FT-12", "12"); got != want {
			t.Errorf("Applied(%q) = %v, want %v", title, got, want)
		}
	}

	if !Template("").Applied("anything", "FT-12", "12") {
		t.Error("no template should count as applied")
	}
}
