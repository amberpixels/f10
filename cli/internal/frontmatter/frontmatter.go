// Package frontmatter splits a markdown file's leading YAML block from its
// body. project.md keeps the facts the binary acts on there, typed, and the
// prose the model reads below it.
package frontmatter

import (
	"bytes"
	"strings"
)

const fence = "---"

// Split returns the YAML between a leading `---` line and the next `---`
// line, and the body after it. Without a leading block, ok is false and the
// body is the whole input; an opened block that never closes is not a block.
func Split(data []byte) ([]byte, []byte, bool) {
	first, rest, found := bytes.Cut(data, []byte("\n"))
	if !found || strings.TrimRight(string(first), " \t\r") != fence {
		return nil, data, false
	}

	var block []byte

	for len(rest) > 0 {
		line, after, _ := bytes.Cut(rest, []byte("\n"))
		if strings.TrimRight(string(line), " \t\r") == fence {
			return block, after, true
		}

		block = append(append(block, line...), '\n')
		rest = after
	}

	return nil, data, false
}
