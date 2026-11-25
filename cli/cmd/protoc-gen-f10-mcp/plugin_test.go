package main

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/pluginpb"
)

func field(
	name string,
	n int32,
	typ descriptorpb.FieldDescriptorProto_Type,
	typeName string,
) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{
		Name:     new(name),
		Number:   new(n),
		Type:     typ.Enum(),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		JsonName: new(name),
	}
	if typeName != "" {
		f.TypeName = new(typeName)
	}

	return f
}

func repeated(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()

	return f
}

func comment(path []int32, text string) *descriptorpb.SourceCodeInfo_Location {
	return &descriptorpb.SourceCodeInfo_Location{
		Path:            path,
		Span:            []int32{0, 0, 1},
		LeadingComments: new(" " + text + "\n"),
	}
}

const (
	tMessage = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	tString  = descriptorpb.FieldDescriptorProto_TYPE_STRING
)

// testFile is a proto file with every shape the schema maps; methodDoc is
// the Get rpc's comment, empty for none.
func testFile(methodDoc string, extra ...*descriptorpb.MethodDescriptorProto) *descriptorpb.FileDescriptorProto {
	choice := int32(0)

	a := field("a", 12, tString, "")
	a.OneofIndex = &choice
	b := field("b", 13, tString, "")
	b.OneofIndex = &choice

	req := &descriptorpb.DescriptorProto{
		Name: new("Req"),
		Field: []*descriptorpb.FieldDescriptorProto{
			field("n", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32, ""),
			field("big", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64, ""),
			field("ok", 3, descriptorpb.FieldDescriptorProto_TYPE_BOOL, ""),
			field("name", 4, tString, ""),
			field("data", 5, descriptorpb.FieldDescriptorProto_TYPE_BYTES, ""),
			field("color", 6, descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".t.v1.Color"),
			repeated(field("tags", 7, tString, "")),
			repeated(field("labels", 8, tMessage, ".t.v1.Req.LabelsEntry")),
			field("child", 9, tMessage, ".t.v1.Node"),
			field("at", 10, tMessage, ".google.protobuf.Timestamp"),
			field("every", 11, tMessage, ".google.protobuf.Duration"),
			field("caller", 20, tMessage, ".t.v1.CallerContext"),
			a, b,
		},
		NestedType: []*descriptorpb.DescriptorProto{{
			Name: new("LabelsEntry"),
			Field: []*descriptorpb.FieldDescriptorProto{
				field("key", 1, tString, ""),
				field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32, ""),
			},
			Options: &descriptorpb.MessageOptions{MapEntry: new(true)},
		}},
		OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: new("choice")}},
	}

	node := &descriptorpb.DescriptorProto{
		Name: new("Node"),
		Field: []*descriptorpb.FieldDescriptorProto{
			field("next", 1, tMessage, ".t.v1.Node"),
			field("value", 2, tString, ""),
		},
	}

	caller := &descriptorpb.DescriptorProto{
		Name:  new("CallerContext"),
		Field: []*descriptorpb.FieldDescriptorProto{field("cwd", 1, tString, "")},
	}

	stream := &descriptorpb.MethodDescriptorProto{
		Name:            new("Watch"),
		InputType:       new(".t.v1.Req"),
		OutputType:      new(".t.v1.Node"),
		ServerStreaming: new(true),
	}

	methods := append([]*descriptorpb.MethodDescriptorProto{{
		Name:       new("GetPRNode"),
		InputType:  new(".t.v1.Req"),
		OutputType: new(".t.v1.Node"),
	}, stream}, extra...)

	locs := []*descriptorpb.SourceCodeInfo_Location{
		comment([]int32{4, 0, 2, 0}, "n counts\n things."),
		comment([]int32{5, 0, 2, 1}, "warm"),
		comment([]int32{6, 0, 2, 1}, "Watch streams."),
	}
	if methodDoc != "" {
		locs = append(locs, comment([]int32{6, 0, 2, 0}, methodDoc))
	}

	return &descriptorpb.FileDescriptorProto{
		Name:        new("t/v1/t.proto"),
		Package:     new("t.v1"),
		Syntax:      new("proto3"),
		Dependency:  []string{"google/protobuf/timestamp.proto", "google/protobuf/duration.proto"},
		Options:     &descriptorpb.FileOptions{GoPackage: new("example.com/t/v1;tv1")},
		MessageType: []*descriptorpb.DescriptorProto{req, node, caller},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: new("Color"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: new("COLOR_UNSPECIFIED"), Number: new(int32(0))},
				{Name: new("COLOR_RED"), Number: new(int32(1))},
			},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:   new("NodeService"),
			Method: methods,
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: locs},
	}
}

func plugin(t *testing.T, f *descriptorpb.FileDescriptorProto) *protogen.Plugin {
	t.Helper()

	gen, err := protogen.Options{}.New(&pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{f.GetName()},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto),
			protodesc.ToFileDescriptorProto(durationpb.File_google_protobuf_duration_proto),
			f,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return gen
}

func TestCollectSkipsStreamingAndNamesInSnakeCase(t *testing.T) {
	tools, err := collect(plugin(t, testFile("GetPRNode reads a node.")).Files)
	if err != nil {
		t.Fatal(err)
	}

	if len(tools) != 1 {
		t.Fatalf("want only the unary method, got %d tools", len(tools))
	}

	got := tools[0]
	if got.name != "get_pr_node" || got.procedure != "/t.v1.NodeService/GetPRNode" ||
		got.description != "GetPRNode reads a node." {
		t.Fatalf("got %q %q %q", got.name, got.procedure, got.description)
	}
}

func TestCollectRefusesAMethodWithNoComment(t *testing.T) {
	_, err := collect(plugin(t, testFile("")).Files)
	if err == nil || !strings.Contains(err.Error(), "t.v1.NodeService.GetPRNode has no comment") {
		t.Fatalf("want a missing-comment error, got %v", err)
	}
}

func TestCollectRefusesTwoMethodsWithOneName(t *testing.T) {
	twin := &descriptorpb.MethodDescriptorProto{
		Name:       new("Get_PRNode"),
		InputType:  new(".t.v1.Req"),
		OutputType: new(".t.v1.Node"),
	}
	f := testFile("GetPRNode reads a node.", twin)
	f.SourceCodeInfo.Location = append(f.SourceCodeInfo.Location, comment([]int32{6, 0, 2, 2}, "A twin."))

	_, err := collect(plugin(t, f).Files)
	if err == nil || !strings.Contains(err.Error(), "would both be the MCP tool get_pr_node") {
		t.Fatalf("want a name collision, got %v", err)
	}
}

func TestInputSchemaMapsEveryShape(t *testing.T) {
	tools, err := collect(plugin(t, testFile("GetPRNode reads a node.")).Files)
	if err != nil {
		t.Fatal(err)
	}

	var s map[string]any
	if err := json.Unmarshal([]byte(tools[0].schema), &s); err != nil {
		t.Fatal(err)
	}

	if s["additionalProperties"] != false {
		t.Fatalf("unknown fields must be refused: %v", s)
	}

	props := s["properties"].(map[string]any)
	want := map[string]string{
		"n":      `{"description":"n counts things.","type":"integer"}`,
		"big":    `{"type":["integer","string"]}`,
		"ok":     `{"type":"boolean"}`,
		"name":   `{"type":"string"}`,
		"data":   `{"contentEncoding":"base64","type":"string"}`,
		"color":  `{"description":"COLOR_RED: warm","enum":["COLOR_UNSPECIFIED","COLOR_RED"],"type":"string"}`,
		"tags":   `{"items":{"type":"string"},"type":"array"}`,
		"labels": `{"additionalProperties":{"type":"integer"},"type":"object"}`,
		"child":  `{"$ref":"#/$defs/t.v1.Node"}`,
		"at":     `{"format":"date-time","type":"string"}`,
		"every":  `{"description":"a duration in seconds, such as 90s or 1.5s","type":"string"}`,
		"a":      `{"description":"Set at most one of a, b.","type":"string"}`,
	}

	for name, js := range want {
		raw, _ := json.Marshal(props[name])
		if string(raw) != js {
			t.Errorf("%s:\n got %s\nwant %s", name, raw, js)
		}
	}

	// the caller's context is the local CLI's, which a model cannot know
	if _, ok := props["caller"]; ok {
		t.Errorf("a CallerContext field must be left out of the tool: %v", props["caller"])
	}

	if _, ok := s["$defs"].(map[string]any)["t.v1.CallerContext"]; ok {
		t.Error("a CallerContext must not reach $defs")
	}

	node, _ := json.Marshal(s["$defs"].(map[string]any)["t.v1.Node"])
	if !strings.Contains(string(node), `"next":{"$ref":"#/$defs/t.v1.Node"}`) {
		t.Errorf("a recursive message must refer to its own $defs entry: %s", node)
	}
}

// A deprecated method is an old name kept for older clients: it is no tool,
// and a deprecated field is no argument of one.
func TestCollectLeavesOutDeprecatedNames(t *testing.T) {
	old := &descriptorpb.MethodDescriptorProto{
		Name:       new("OldGet"),
		InputType:  new(".t.v1.Req"),
		OutputType: new(".t.v1.Node"),
		Options:    &descriptorpb.MethodOptions{Deprecated: new(true)},
	}
	f := testFile("GetPRNode reads a node.", old)

	gone := field("old_name", 99, tString, "")
	gone.Options = &descriptorpb.FieldOptions{Deprecated: new(true)}
	f.MessageType[0].Field = append(f.MessageType[0].Field, gone)

	tools, err := collect(plugin(t, f).Files)
	if err != nil {
		t.Fatal(err)
	}

	if len(tools) != 1 || tools[0].name != "get_pr_node" {
		t.Fatalf("tools = %+v, want only the method still current", tools)
	}

	if strings.Contains(tools[0].schema, "old_name") {
		t.Errorf("the schema keeps a deprecated field: %s", tools[0].schema)
	}
}

// A method whose comment says it is not a tool stays an rpc and is no tool.
func TestCollectLeavesOutAMethodMarkedNotATool(t *testing.T) {
	f := testFile("GetPRNode reads a node. Not a tool: only a local client has what it takes.")

	tools, err := collect(plugin(t, f).Files)
	if err != nil || len(tools) != 0 {
		t.Errorf("tools = %+v, %v; want none", tools, err)
	}
}
