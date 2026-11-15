// Command protoc-gen-f10-mcp is the buf plugin that turns every unary method
// of the API into an MCP tool: its input schema from the request message, its
// description from the rpc's comment. It runs with strategy all, so one file
// holds every service and a new method needs no hand-written registration.
package main

import (
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

// outPath and outPackage name the one file the plugin writes, relative to
// the plugin's out dir (cli/gen/mcptools).
const (
	outPath    = "tools.gen.go"
	outPackage = protogen.GoImportPath("github.com/amberpixels/f10/cli/gen/mcptools")
)

func main() {
	protogen.Options{}.Run(func(gen *protogen.Plugin) error {
		gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

		tools, err := collect(gen.Files)
		if err != nil {
			return err
		}

		return render(gen.NewGeneratedFile(outPath, outPackage), tools)
	})
}
