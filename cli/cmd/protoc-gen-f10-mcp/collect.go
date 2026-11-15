package main

import (
	"fmt"
	"strings"
	"unicode"

	"google.golang.org/protobuf/compiler/protogen"
)

// A tool is one unary method on its way to the generated file.
type tool struct {
	name        string // the method in snake case
	procedure   string // the Connect route, /<package>.<Service>/<Method>
	description string
	schema      string // the request message as a JSON Schema
	service     *protogen.Service
	method      *protogen.Method
	file        *protogen.File
}

// collect is every unary method of the files to generate. A method with no
// comment, or two methods with one tool name, fail the run: the comment is
// all a model reads to pick the tool.
func collect(files []*protogen.File) ([]tool, error) {
	var tools []tool

	seen := map[string]string{}

	for _, f := range files {
		if !f.Generate {
			continue
		}

		for _, svc := range f.Services {
			for _, m := range svc.Methods {
				if m.Desc.IsStreamingClient() || m.Desc.IsStreamingServer() {
					continue
				}

				full := string(m.Desc.FullName())

				desc := commentText(m.Comments.Leading)
				if desc == "" {
					return nil, fmt.Errorf("%s has no comment: an MCP tool's description is its rpc's comment", full)
				}

				name := snake(m.GoName)
				if other, ok := seen[name]; ok {
					return nil, fmt.Errorf("%s and %s would both be the MCP tool %s", other, full, name)
				}

				seen[name] = full

				schema, err := inputSchema(m.Input)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", full, err)
				}

				tools = append(tools, tool{
					name:        name,
					procedure:   fmt.Sprintf("/%s/%s", svc.Desc.FullName(), m.Desc.Name()),
					description: desc,
					schema:      schema,
					service:     svc,
					method:      m,
					file:        f,
				})
			}
		}
	}

	return tools, nil
}

// commentText is a proto comment as one paragraph.
func commentText(c protogen.Comments) string {
	return strings.Join(strings.Fields(string(c)), " ")
}

// snake is a Go method name in snake case, an acronym kept as one word:
// GetPRStatus is get_pr_status.
func snake(s string) string {
	r := []rune(s)

	var b strings.Builder

	for i, c := range r {
		if unicode.IsUpper(c) && i > 0 {
			prev := r[i-1]
			nextLower := i+1 < len(r) && unicode.IsLower(r[i+1])

			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}

		b.WriteRune(unicode.ToLower(c))
	}

	return b.String()
}
