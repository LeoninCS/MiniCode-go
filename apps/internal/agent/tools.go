package agent

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/tools"
)

const toolNameBash = "bash"

// executeTool 校验工具名称和参数,执行后返回模型需要的结果。
func executeTool(ctx context.Context, call provider.ToolCall, stdout io.Writer) (string, error) {
	switch call.Function.Name {
	case toolNameBash:
		var arguments map[string]any
		if err := call.DecodeArguments(&arguments); err != nil {
			return "", err
		}
		command, ok := arguments["command"].(string)
		if !ok {
			return "", errors.New("bash: command must be a string")
		}
		if len(arguments) != 1 {
			return "", errors.New("bash: only the command parameter is supported")
		}
		return tools.RunBash(ctx, command, stdout)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
}

// availableTools 返回当前发送给模型的工具声明。
func availableTools() []provider.Tool {
	bashTool := buildBashTool()
	return []provider.Tool{bashTool}
}

// buildBashTool 构造发送给模型的 bash 工具声明。
func buildBashTool() provider.Tool {
	bashParameters := provider.JSONSchema{
		"type": "object",
		"properties": map[string]provider.JSONSchema{
			"command": {
				"type":        "string",
				"description": "The shell command to run.",
			},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
	}
	bashTool := provider.Tool{
		Type: provider.ToolTypeFunction,
		Function: provider.FunctionDefinition{
			Name:        toolNameBash,
			Description: "Run a shell command in the current workspace.",
			Parameters:  bashParameters,
		},
	}
	return bashTool
}
