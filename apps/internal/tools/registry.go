package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
)

const (
	toolNameBash  = "bash"
	toolNameRead  = "read"
	toolNameWrite = "write"
	toolNameEdit  = "edit"
)

// Tool 将工具声明和执行函数放在一起;执行函数负责解析、校验自己的参数。
// Execute 返回工具结果和执行错误,stdout 仅用于实时输出。
type Tool struct {
	Definition provider.Tool
	Execute    func(ctx context.Context, call provider.ToolCall, stdout io.Writer) (string, error)
}

// ToolRegistry 按名称保存工具,按注册顺序提供声明;同一实例应顺序调用。
type ToolRegistry struct {
	tools map[string]Tool
	names []string
}

// NewToolRegistry 创建注册表,拒绝无效工具和重复名称。
func NewToolRegistry(registered ...Tool) (*ToolRegistry, error) {
	registry := &ToolRegistry{}
	for _, tool := range registered {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Register 注册一个工具,已有同名工具时不覆盖。
func (r *ToolRegistry) Register(tool Tool) error {
	if tool.Execute == nil {
		return errors.New("tool registry: execute function must not be nil")
	}
	definition := tool.Definition
	name := definition.Function.Name
	if strings.TrimSpace(name) == "" {
		return errors.New("tool registry: tool name must not be empty")
	}
	if definition.Type != provider.ToolTypeFunction {
		return fmt.Errorf("tool registry: unsupported tool type %q", definition.Type)
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool registry: duplicate tool %q", name)
	}
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[name] = tool
	r.names = append(r.names, name)
	return nil
}

// Lookup 按名称查找工具。
func (r *ToolRegistry) Lookup(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Definitions 按注册顺序返回可发送给模型的工具声明。
func (r *ToolRegistry) Definitions() []provider.Tool {
	definitions := make([]provider.Tool, 0, len(r.names))
	for _, name := range r.names {
		definitions = append(definitions, r.tools[name].Definition)
	}
	return definitions
}

// Execute 按工具名称分发调用,保留失败时已产生的输出和原始错误。
func (r *ToolRegistry) Execute(ctx context.Context, call provider.ToolCall, stdout io.Writer) (string, error) {
	tool, ok := r.Lookup(call.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	return tool.Execute(ctx, call, stdout)
}

// NewBuiltinToolRegistry 注册四个内置工具,文件工具共享 files 的工作区和版本记录。
// files 由调用方创建并关闭;bash 沿用进程的当前工作目录。
func NewBuiltinToolRegistry(files *FileTools) (*ToolRegistry, error) {
	if files == nil {
		return nil, errors.New("tool registry: file tools must not be nil")
	}
	return NewToolRegistry(
		Tool{
			Definition: buildReadTool(),
			Execute: func(ctx context.Context, call provider.ToolCall, _ io.Writer) (string, error) {
				return executeReadTool(ctx, call, files)
			},
		},
		Tool{Definition: buildBashTool(), Execute: executeBashTool},
		Tool{
			Definition: buildEditTool(),
			Execute: func(ctx context.Context, call provider.ToolCall, _ io.Writer) (string, error) {
				return executeEditTool(ctx, call, files)
			},
		},
		Tool{
			Definition: buildWriteTool(),
			Execute: func(ctx context.Context, call provider.ToolCall, _ io.Writer) (string, error) {
				return executeWriteTool(ctx, call, files)
			},
		},
	)
}

// decodeToolArguments 复用协议层的 JSON 对象校验,检查必填、未知和 null 字段后解码类型。
// 字段名区分大小写;可选字段省略时保留 dst 中的零值。
func decodeToolArguments(call provider.ToolCall, dst any, required []string, optional ...string) error {
	var fields map[string]json.RawMessage
	if err := call.DecodeArguments(&fields); err != nil {
		return err
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("%s: %s is required", call.Function.Name, name)
		}
	}
	for name, value := range fields {
		if !slices.Contains(required, name) && !slices.Contains(optional, name) {
			return fmt.Errorf("%s: unknown parameter %q", call.Function.Name, name)
		}
		if strings.TrimSpace(string(value)) == "null" {
			return fmt.Errorf("%s: %s must not be null", call.Function.Name, name)
		}
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), dst); err != nil {
		return fmt.Errorf("%s: decode arguments: %w", call.Function.Name, err)
	}
	return nil
}
