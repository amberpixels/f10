package frontmatter

import "testing"

func TestSplit(t *testing.T) {
	cases := []struct {
		name, in, block, body string
		ok                    bool
	}{
		{"a block and a body", "---\nstorage: in-repo\n---\n# Title\n", "storage: in-repo\n", "# Title\n", true},
		{"an empty block", "---\n---\nbody", "", "body", true},
		{"CRLF fences", "---\r\nvisibility: public\r\n---\r\nbody", "visibility: public\r\n", "body", true},
		{"no block", "# Title\n---\nnot: frontmatter\n---\n", "", "# Title\n---\nnot: frontmatter\n---\n", false},
		{"never closed", "---\nstorage: in-repo\n", "", "---\nstorage: in-repo\n", false},
		{"empty input", "", "", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block, body, ok := Split([]byte(c.in))
			if string(block) != c.block || string(body) != c.body || ok != c.ok {
				t.Errorf("Split = %q, %q, %v; want %q, %q, %v", block, body, ok, c.block, c.body, c.ok)
			}
		})
	}
}
