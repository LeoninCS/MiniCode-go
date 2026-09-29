package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/tools"
)

// registryTestTool 记录被分发到的调用,以便断言注册表按名称路由到正确的工具。
type registryTestTool struct {
	name   string
	result string
	err    error
	calls  []provider.ToolCall
}

// newRegistryTestTool 包装成 tools.Tool,执行时把结果同时写入实时输出和返回值。
func newRegistryTestTool(tool *registryTestTool) tools.Tool {
	return tools.Tool{
		Definition: provider.Tool{
			Type:     provider.ToolTypeFunction,
			Function: provider.FunctionDefinition{Name: tool.name},
		},
		Execute: func(_ context.Context, call provider.ToolCall, stdout io.Writer) (string, error) {
			tool.calls = append(tool.calls, call)
			if _, err := io.WriteString(stdout, tool.result); err != nil {
				return "", err
			}
			return tool.result, tool.err
		},
	}
}

func registryCall(name, arguments string) provider.ToolCall {
	return provider.ToolCall{ID: "test-call", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: name, Arguments: arguments}}
}

func TestToolRegistry_RegisterLookupAndExecute(t *testing.T) {
	failure := errors.New("tool failed")
	first := &registryTestTool{name: "first", result: "partial output", err: failure}
	second := &registryTestTool{name: "second", result: "success"}
	registry, err := tools.NewToolRegistry(newRegistryTestTool(first))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(newRegistryTestTool(second)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(newRegistryTestTool(&registryTestTool{name: "first"})); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate registration: %v", err)
	}
	// 重复注册被拒绝后,原工具仍可按名称查到,声明不被覆盖。
	got, ok := registry.Lookup("first")
	if !ok || got.Definition.Function.Name != "first" {
		t.Fatalf("lookup after duplicate registration = %+v, %v", got.Definition, ok)
	}
	if _, ok := registry.Lookup("missing"); ok {
		t.Fatal("found unknown tool")
	}
	definitions := registry.Definitions()
	var names []string
	for _, definition := range definitions {
		names = append(names, definition.Function.Name)
	}
	if !reflect.DeepEqual(names, []string{"first", "second"}) {
		t.Fatalf("registration order = %v", names)
	}
	call := registryCall("first", `{"value":42}`)
	var stdout bytes.Buffer
	result, err := registry.Execute(context.Background(), call, &stdout)
	// 工具失败时仍返回已产生的输出,并把原始错误交给调用方。
	if result != first.result || stdout.String() != first.result || !errors.Is(err, failure) {
		t.Fatalf("execute = %q, %v, output %q", result, err, stdout.String())
	}
	if !reflect.DeepEqual(first.calls, []provider.ToolCall{call}) || len(second.calls) != 0 {
		t.Fatalf("wrong dispatch: first = %v, second = %v", first.calls, second.calls)
	}
	// stdout 为 nil 时丢弃实时输出,不影响结果与错误。
	if result, err := registry.Execute(context.Background(), registryCall("second", `{}`), nil); err != nil || result != "success" {
		t.Fatalf("execute with discarded output = %q, %v", result, err)
	}
}

func TestToolRegistry_RejectsInvalidRegistration(t *testing.T) {
	execute := func(context.Context, provider.ToolCall, io.Writer) (string, error) {
		return "", nil
	}
	withExecute := func(name string) tools.Tool {
		return tools.Tool{
			Definition: provider.Tool{Type: provider.ToolTypeFunction, Function: provider.FunctionDefinition{Name: name}},
			Execute:    execute,
		}
	}
	withoutExecute := func(name string) tools.Tool {
		return tools.Tool{
			Definition: provider.Tool{Type: provider.ToolTypeFunction, Function: provider.FunctionDefinition{Name: name}},
		}
	}
	wrongType := func(name string) tools.Tool {
		return tools.Tool{
			Definition: provider.Tool{Type: "unknown", Function: provider.FunctionDefinition{Name: name}},
			Execute:    execute,
		}
	}
	for _, tc := range []struct {
		name  string
		tools []tools.Tool
	}{
		{"missing execute function", []tools.Tool{withoutExecute("valid")}},
		{"empty name", []tools.Tool{withExecute("")}},
		{"blank name", []tools.Tool{withExecute(" ")}},
		{"unsupported type", []tools.Tool{wrongType("valid")}},
		{"duplicate name", []tools.Tool{withExecute("same"), withExecute("same")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tools.NewToolRegistry(tc.tools...); err == nil {
				t.Fatal("accepted invalid registration")
			}
		})
	}
}

func TestToolRegistry_UnknownToolAndCancellation(t *testing.T) {
	tool := &registryTestTool{name: "known"}
	registry, err := tools.NewToolRegistry(newRegistryTestTool(tool))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(context.Background(), registryCall("missing", `{}`), nil); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool: %v", err)
	}
	// ctx 已取消时直接返回,不再执行任何工具。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Execute(ctx, registryCall("known", `{}`), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dispatch: %v", err)
	}
	if len(tool.calls) != 0 {
		t.Fatal("unexpected execution")
	}
}

func newBuiltinRegistry(t *testing.T) (*tools.ToolRegistry, string) {
	t.Helper()
	workspace := t.TempDir()
	files, err := tools.NewFileTools(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := files.Close(); err != nil {
			t.Error(err)
		}
	})
	registry, err := tools.NewBuiltinToolRegistry(files)
	if err != nil {
		t.Fatal(err)
	}
	return registry, workspace
}

func TestBuiltinTools_Definitions(t *testing.T) {
	registry, _ := newBuiltinRegistry(t)
	// 检查实际发送给模型的 JSON,包括参数类型、必填项和默认值。
	encoded, err := json.Marshal(registry.Definitions())
	if err != nil {
		t.Fatal(err)
	}
	var definitions []provider.Tool
	if err := json.Unmarshal(encoded, &definitions); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name       string
		properties map[string]string
		required   []any
	}{
		{"read", map[string]string{"path": "string", "offset": "integer", "limit": "integer"}, []any{"path"}},
		{"bash", map[string]string{"command": "string"}, []any{"command"}},
		{"edit", map[string]string{"path": "string", "old_text": "string", "new_text": "string", "replace_all": "boolean"}, []any{"path", "old_text", "new_text"}},
		{"write", map[string]string{"path": "string", "content": "string"}, []any{"path", "content"}},
	}
	if len(definitions) != len(want) {
		t.Fatalf("definitions = %s", encoded)
	}
	for i, expected := range want {
		definition := definitions[i]
		if definition.Type != provider.ToolTypeFunction || definition.Function.Name != expected.name || definition.Function.Description == "" || definition.Function.Strict != nil {
			t.Fatalf("invalid definition: %+v", definition)
		}
		parameters := definition.Function.Parameters
		if parameters["type"] != "object" || parameters["additionalProperties"] != false || !reflect.DeepEqual(parameters["required"], expected.required) {
			t.Fatalf("invalid parameters for %s: %v", expected.name, parameters)
		}
		properties, ok := parameters["properties"].(map[string]any)
		if !ok || len(properties) != len(expected.properties) {
			t.Fatalf("invalid properties for %s: %v", expected.name, parameters["properties"])
		}
		for name, kind := range expected.properties {
			property, ok := properties[name].(map[string]any)
			if !ok || property["type"] != kind {
				t.Fatalf("invalid property %s.%s: %v", expected.name, name, properties[name])
			}
			if name == "replace_all" && property["default"] != false {
				t.Fatalf("replace_all default = %v", property["default"])
			}
		}
	}
	if _, err := tools.NewBuiltinToolRegistry(nil); err == nil {
		t.Fatal("accepted nil file tools")
	}
}

func TestBuiltinTools_ReadAndEditShareVersions(t *testing.T) {
	registry, workspace := newBuiltinRegistry(t)
	content := "one old\r\ntwo old\r\n"
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := registry.Execute(ctx, registryCall("edit", `{"path":"input.txt","old_text":"old","new_text":"new"}`), nil); err == nil || !strings.Contains(err.Error(), "read this path") {
		t.Fatalf("edit without reading: %v", err)
	}
	if result, err := registry.Execute(ctx, registryCall("read", `{"path":"input.txt"}`), nil); err != nil || result != "1: one old\r\n2: two old\r\n" {
		t.Fatalf("read with defaults = %q, %v", result, err)
	}
	if result, err := registry.Execute(ctx, registryCall("read", `{"path":"input.txt","offset":2,"limit":1}`), nil); err != nil || result != "2: two old\r\n" {
		t.Fatalf("read range = %q, %v", result, err)
	}
	for _, arguments := range []string{
		`{"path":"input.txt","old_text":"old","new_text":"new"}`,
		`{"path":"input.txt","old_text":"old","new_text":"new","replace_all":false}`,
	} {
		if _, err := registry.Execute(ctx, registryCall("edit", arguments), nil); err == nil || !strings.Contains(err.Error(), "include surrounding code") {
			t.Fatalf("single edit should require context: %v", err)
		}
		assertFile(t, workspace, "input.txt", content)
	}
	if result, err := registry.Execute(ctx, registryCall("edit", `{"path":"input.txt","old_text":"old","new_text":"new","replace_all":true}`), nil); err != nil || !strings.Contains(result, "2 occurrences") {
		t.Fatalf("replace all = %q, %v", result, err)
	}
	assertFile(t, workspace, "input.txt", "one new\r\ntwo new\r\n")
	if _, err := registry.Execute(ctx, registryCall("edit", `{"path":"input.txt","old_text":"one new\ntwo new\n","new_text":""}`), nil); err != nil {
		t.Fatalf("delete after previous edit: %v", err)
	}
	assertFile(t, workspace, "input.txt", "")
}

func TestBuiltinTools_WriteAndEditShareVersions(t *testing.T) {
	registry, workspace := newBuiltinRegistry(t)
	ctx := context.Background()
	if _, err := registry.Execute(ctx, registryCall("write", `{"path":"nested/input.txt","content":"old"}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(ctx, registryCall("edit", `{"path":"nested/input.txt","old_text":"old","new_text":"new"}`), nil); err != nil {
		t.Fatalf("edit after write: %v", err)
	}
	assertFile(t, workspace, "nested/input.txt", "new")
	if _, err := registry.Execute(ctx, registryCall("write", `{"path":"nested/input.txt","content":""}`), nil); err != nil {
		t.Fatalf("write empty content: %v", err)
	}
	assertFile(t, workspace, "nested/input.txt", "")
}

func TestBuiltinTools_BashOutputAndErrors(t *testing.T) {
	registry, _ := newBuiltinRegistry(t)
	var output bytes.Buffer
	result, err := registry.Execute(context.Background(), registryCall("bash", `{"command":"printf partial; exit 7"}`), &output)
	var exitError *exec.ExitError
	if result != "partial" || output.String() != "partial" || !errors.As(err, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("bash = %q, %v, output %q", result, err, output.String())
	}
	if result, err := registry.Execute(context.Background(), registryCall("bash", `{"command":"printf success"}`), nil); err != nil || result != "success" {
		t.Fatalf("bash without live output = %q, %v", result, err)
	}
}

func TestBuiltinTools_RejectInvalidArgumentsBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tool      string
		arguments string
		wantErr   string
	}{
		{"empty arguments", "read", "", "empty arguments"},
		{"null object", "write", "null", "JSON object"},
		{"array", "edit", "[]", "JSON object"},
		{"malformed JSON", "bash", "{", "JSON object"},
		{"trailing JSON", "write", `{"path":"input.txt","content":"new"} {}`, "JSON object"},
		{"missing command", "bash", `{}`, "command must be a string"},
		{"null command", "bash", `{"command":null}`, "command must be a string"},
		{"wrong command type", "bash", `{"command":42}`, "command must be a string"},
		{"empty command", "bash", `{"command":" "}`, "must not be empty"},
		{"extra bash parameter", "bash", `{"command":"printf should-not-run","extra":true}`, "only the command"},
		{"missing path", "read", `{}`, "path is required"},
		{"null path", "read", `{"path":null}`, "path must not be null"},
		{"wrong path type", "read", `{"path":42}`, "decode arguments"},
		{"empty path", "read", `{"path":""}`, "must not be empty"},
		{"extra read parameter", "read", `{"path":"input.txt","extra":1}`, "unknown parameter"},
		{"case-sensitive optional field", "read", `{"path":"input.txt","Limit":1}`, "unknown parameter"},
		{"null offset", "read", `{"path":"input.txt","offset":null}`, "offset must not be null"},
		{"fractional offset", "read", `{"path":"input.txt","offset":1.5}`, "decode arguments"},
		{"overflowing limit", "read", `{"path":"input.txt","limit":1e100}`, "decode arguments"},
		{"negative limit", "read", `{"path":"input.txt","limit":-1}`, "must not be negative"},
		{"missing content", "write", `{"path":"input.txt"}`, "content is required"},
		{"null content", "write", `{"path":"input.txt","content":null}`, "content must not be null"},
		{"wrong content type", "write", `{"path":"input.txt","content":false}`, "decode arguments"},
		{"extra write parameter", "write", `{"path":"input.txt","content":"new","extra":1}`, "unknown parameter"},
		{"missing old text", "edit", `{"path":"input.txt","new_text":"new"}`, "old_text is required"},
		{"missing new text", "edit", `{"path":"input.txt","old_text":"old"}`, "new_text is required"},
		{"null new text", "edit", `{"path":"input.txt","old_text":"old","new_text":null}`, "new_text must not be null"},
		{"empty old text", "edit", `{"path":"input.txt","old_text":"","new_text":"new"}`, "old_text must not be empty"},
		{"null replace all", "edit", `{"path":"input.txt","old_text":"old","new_text":"new","replace_all":null}`, "replace_all must not be null"},
		{"string replace all", "edit", `{"path":"input.txt","old_text":"old","new_text":"new","replace_all":"true"}`, "decode arguments"},
		{"extra edit parameter", "edit", `{"path":"input.txt","old_text":"old","new_text":"new","extra":1}`, "unknown parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, workspace := newBuiltinRegistry(t)
			if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := registry.Execute(ctx, registryCall("read", `{"path":"input.txt"}`), nil); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			result, err := registry.Execute(ctx, registryCall(tc.tool, tc.arguments), &output)
			// 参数非法时必须在执行前失败,不产生输出也不改动文件。
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || result != "" || output.Len() != 0 {
				t.Fatalf("execute = %q, %v, output %q; want error containing %q", result, err, output.String(), tc.wantErr)
			}
			assertFile(t, workspace, "input.txt", "old")
		})
	}
}
