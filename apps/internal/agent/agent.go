// Package agent 管理模型调用、消息历史与工具执行循环。
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/tools"
)

const (
	maxTurns               = 500
	turnLimitSummaryPrompt = "已达到工具执行轮数上限。请停止调用工具，仅根据已有对话和工具结果给出最终回复。回答用户的问题，并说明已完成的工作、尚未完成的部分及原因；不要声称未验证的结果。"
)

// Output 由调用方实现,负责展示 Agent 的运行过程。
// Write 接收工具的实时输出;模型文本与工具结果均保留原文。
type Output interface {
	io.Writer
	Message(content string)
	ToolCall(call provider.ToolCall)
	// ToolResult 在工具结束后交付完整结果,其内容已通过 Write 实时输出。
	ToolResult(result string)
	ToolError(err error)
}

// Run 执行一次任务,直到模型给出最终回复、达到轮数上限或发生不可恢复错误。
// 调用方负责设置 ctx 的超时和取消,并提供 Output 实现。
// 工作区取进程当前工作目录,系统 Prompt 会向模型声明该路径;任务结束时释放工作区句柄。
func Run(ctx context.Context, client *provider.Client, prompt string, output Output) error {
	// 只解析一次绝对路径:系统 Prompt 要声明工作区,文件工具也不再依赖运行期间的工作目录。
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	files, err := tools.NewFileTools(workspace)
	if err != nil {
		return err
	}
	defer func() {
		if err := files.Close(); err != nil {
			output.ToolError(err)
		}
	}()
	registry, err := tools.NewBuiltinToolRegistry(files)
	if err != nil {
		return err
	}
	messages := []provider.Message{
		provider.NewMessage(provider.RoleSystem, systemPrompt(workspace), ""),
		provider.NewMessage(provider.RoleUser, prompt, ""),
	}
	toolDefinitions := registry.Definitions()
	for turn := 0; turn < maxTurns; turn++ {
		resp, err := client.Chat(ctx, provider.ChatRequest{
			Messages: messages,
			Tools:    toolDefinitions,
		})
		if err != nil {
			return err
		}
		toolCalls, err := resp.ToolCalls()
		if err != nil {
			return fmt.Errorf("invalid response from model: %w", err)
		}
		content := resp.Content()
		if content == "" && len(toolCalls) == 0 {
			return errors.New("empty response from model")
		}
		if content != "" {
			output.Message(content)
		}
		if len(toolCalls) == 0 {
			return nil
		}
		// 留下完整 assistant 消息,后续工具结果通过调用 ID 与它对应。
		messages = append(messages, resp.Choices[0].Message)
		for _, call := range toolCalls {
			output.ToolCall(call)
			result, err := registry.Execute(ctx, call, output)
			output.ToolResult(result)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				output.ToolError(err)
				result += "\nerror: " + err.Error()
			}
			messages = append(messages, provider.NewMessage(provider.RoleTool, result, call.ID))
		}
	}
	content, err := summarizeAtTurnLimit(ctx, client, messages)
	if err != nil {
		output.Message(fmt.Sprintf("已达到最大执行轮数（%d），本次任务已停止，未能生成最终总结。请结合上方工具输出确认已完成的工作。", maxTurns))
		return fmt.Errorf("reached maximum model turns (%d); summarize: %w", maxTurns, err)
	}
	output.Message(content)
	return fmt.Errorf("reached maximum model turns (%d)", maxTurns)
}

// summarizeAtTurnLimit 在完整工具结果之后请求一次总结,不再提供或执行工具。
func summarizeAtTurnLimit(ctx context.Context, client *provider.Client, messages []provider.Message) (string, error) {
	messages = append(messages, provider.NewMessage(provider.RoleSystem, turnLimitSummaryPrompt, ""))
	resp, err := client.Chat(ctx, provider.ChatRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	calls, err := resp.ToolCalls()
	if err != nil {
		return "", fmt.Errorf("invalid summary response: %w", err)
	}
	if len(calls) > 0 {
		return "", errors.New("summary response requested tools")
	}
	content := resp.Content()
	if strings.TrimSpace(content) == "" {
		return "", errors.New("empty summary response from model")
	}
	return content, nil
}
