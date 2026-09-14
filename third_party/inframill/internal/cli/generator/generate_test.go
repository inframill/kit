package generator

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
)

func request() *pluginpb.CodeGeneratorRequest {
	var files []*descriptorpb.FileDescriptorProto
	seen := map[string]bool{}
	var collect func(protoreflect.FileDescriptor)
	collect = func(file protoreflect.FileDescriptor) {
		if seen[file.Path()] {
			return
		}
		seen[file.Path()] = true
		for i := range file.Imports().Len() {
			collect(file.Imports().Get(i).FileDescriptor)
		}
		files = append(files, protodesc.ToFileDescriptorProto(file))
	}
	collect(pb.File_inframill_v1_inframill_proto)
	return &pluginpb.CodeGeneratorRequest{ProtoFile: files, FileToGenerate: []string{pb.File_inframill_v1_inframill_proto.Path()}}
}

func generate(t *testing.T, req *pluginpb.CodeGeneratorRequest) *pluginpb.CodeGeneratorResponse {
	t.Helper()
	plugin, err := (protogen.Options{}).New(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := Generate(plugin); err != nil {
		t.Fatal(err)
	}
	result := plugin.Response()
	if result.GetError() != "" {
		t.Fatal(result.GetError())
	}
	return result
}

// SPEC-0007 REQ-003: reserve driver list for native Engine composition.
func TestREQ003DriverListIsNative(t *testing.T) {
	result := generate(t, request())
	for _, file := range result.GetFile() {
		if strings.Contains(file.GetContent(), "client.ListDrivers(") || strings.Contains(file.GetContent(), `runner.Add(root, "driver"`) {
			t.Fatal("generated a remote Driver health command")
		}
	}
}

// SPEC-0002 REQ-005/009 / AC-004/007: unsupported descriptor changes fail at generation.
func TestREQ005UnsupportedAndDeterministic(t *testing.T) {
	first, second := generate(t, request()), generate(t, request())
	if !proto.Equal(first, second) {
		t.Fatal("nondeterministic generation")
	}
	for _, tc := range []struct {
		name   string
		change func(*descriptorpb.FileDescriptorProto)
	}{
		{"stream", func(f *descriptorpb.FileDescriptorProto) {
			f.GetService()[0].GetMethod()[0].ServerStreaming = new(true)
		}},
		{"repeated", func(f *descriptorpb.FileDescriptorProto) {
			for _, m := range f.GetMessageType() {
				if m.GetName() == fetchRequestName {
					m.GetField()[0].Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
				}
			}
		}},
		{"bytes", func(f *descriptorpb.FileDescriptorProto) {
			for _, m := range f.GetMessageType() {
				if m.GetName() == fetchRequestName {
					m.GetField()[0].Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
				}
			}
		}},
		{"reserved flag", func(f *descriptorpb.FileDescriptorProto) {
			for _, m := range f.GetMessageType() {
				if m.GetName() == fetchRequestName {
					m.GetField()[0].Name = new("endpoint")
				}
			}
		}},
		{"method convention", func(f *descriptorpb.FileDescriptorProto) { f.GetService()[0].GetMethod()[0].Name = new("Fetch") }},
		{"command collision", func(f *descriptorpb.FileDescriptorProto) {
			f.GetService()[0].Method = append(f.GetService()[0].Method, &descriptorpb.MethodDescriptorProto{Name: new("ExecInstance"), InputType: new(".inframill.v1.FetchBlueprintRequest"), OutputType: new(".inframill.v1.FetchBlueprintResponse")})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request()
			tc.change(req.GetProtoFile()[len(req.GetProtoFile())-1])
			plugin, err := (protogen.Options{}).New(req)
			if err == nil {
				err = Generate(plugin)
			}
			if err == nil {
				t.Fatal("unsupported schema accepted")
			}
		})
	}
}

// SPEC-0002 AC-004/007: regenerate transport and CLI in a clean disposable source
// tree, then compile and exercise a new RPC against a fake typed client.
func TestREQ005GeneratedExtensionCompilesAndRuns(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum"} {
		data, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		write(t, filepath.Join(fixture, name), data)
	}
	for _, directory := range []string{"internal/cli/runtime", "internal/cli/config", "internal/cli/output", "internal/cli/generator"} {
		walkErr := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			write(t, filepath.Join(fixture, relative), data)
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	req := request()
	file := req.GetProtoFile()[len(req.GetProtoFile())-1]
	file.MessageType = append(file.MessageType,
		&descriptorpb.DescriptorProto{Name: new("InspectWidgetRequest"), Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("widget_name"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("enabled"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum()},
			{Name: new("count"), Number: proto.Int32(3), Type: descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()},
		}},
		&descriptorpb.DescriptorProto{Name: new("InspectWidgetResponse"), Field: []*descriptorpb.FieldDescriptorProto{
			{Name: new("description"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: new("count"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()},
		}},
	)
	file.GetService()[0].Method = append(file.GetService()[0].Method, &descriptorpb.MethodDescriptorProto{Name: new("InspectWidget"), InputType: new(".inframill.v1.InspectWidgetRequest"), OutputType: new(".inframill.v1.InspectWidgetResponse")})
	// Build the plugin before there is any gen directory, proving bootstrap independence.
	pluginPath := filepath.Join(fixture, "cli-plugin")
	command(t, fixture, nil, "go", "build", "-o", pluginPath, "./internal/cli/generator/cmd")
	input, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	generated := command(t, fixture, input, pluginPath)
	var result pluginpb.CodeGeneratorResponse
	if decodeErr := proto.Unmarshal(generated, &result); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if result.GetError() != "" {
		t.Fatal(result.GetError())
	}
	for _, file := range result.GetFile() {
		write(t, filepath.Join(fixture, "internal/cli/gen", file.GetName()), []byte(file.GetContent()))
	}
	req.Parameter = new("paths=source_relative")
	input, err = proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"protoc-gen-go", "protoc-gen-connect-go"} {
		output := command(t, fixture, input, "go", "tool", tool)
		var transport pluginpb.CodeGeneratorResponse
		if err := proto.Unmarshal(output, &transport); err != nil {
			t.Fatal(err)
		}
		if transport.GetError() != "" {
			t.Fatal(transport.GetError())
		}
		for _, file := range transport.GetFile() {
			write(t, filepath.Join(fixture, "internal/gen", file.GetName()), []byte(file.GetContent()))
		}
	}
	write(t, filepath.Join(fixture, "internal/cli/gen/extension_test.go"), []byte(extensionTest))
	command(t, fixture, nil, "go", "test", "-vet=off", "./internal/cli/gen")
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func command(t *testing.T, directory string, input []byte, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = directory
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s\n%s", name, args, err, output, &stderr)
	}
	return output
}

const extensionTest = `package gen
import (
 "bytes"
 "context"
 "strings"
 "testing"
 "connectrpc.com/connect"
 "github.com/spf13/cobra"
 pb "github.com/sir-hassan/inframill/internal/gen/inframill/v1"
 "github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
 cliruntime "github.com/sir-hassan/inframill/internal/cli/runtime"
)
type fake struct {inframillv1connect.EngineServiceClient; called bool}
func(f *fake)InspectWidget(_ context.Context,r *connect.Request[pb.InspectWidgetRequest])(*connect.Response[pb.InspectWidgetResponse],error){
 if r.Msg.GetWidgetName()!="sample"||!r.Msg.GetEnabled()||r.Msg.GetCount()!=17{panic("wrong generated request")};f.called=true
 return connect.NewResponse(&pb.InspectWidgetResponse{Description:"complete fallback",Count:17}),nil
}
func TestExtension(t *testing.T){
 t.Chdir(t.TempDir());var out bytes.Buffer;client:=&fake{}
 root:=&cobra.Command{Use:"inframill"};root.AddGroup(&cobra.Group{ID:"remote",Title:"Remote"})
 Register(root,&cliruntime.Runtime{Stdout:&out,Client:func(string)inframillv1connect.EngineServiceClient{return client}})
 root.SetArgs([]string{"widget","inspect","--widget-name=sample","--enabled","--count=17"})
 if err:=root.ExecuteContext(t.Context());err!=nil{t.Fatal(err)}
 if !client.called||!strings.Contains(out.String(),"complete fallback")||!strings.Contains(out.String(),"17"){t.Fatal(out.String())}
}
`

const (
	fetchRequestName = "FetchBlueprintRequest"
)
