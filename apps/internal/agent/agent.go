// Package agent 管理模型调用、消息历史与工具执行循环。
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/MiniCode-go/minicode/internal/provider"
)

const maxTurns = 10

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
func Run(ctx context.Context, client *provider.Client, prompt string, output Output) error {
	messages := []provider.Message{provider.NewMessage(provider.RoleUser, prompt, "")}
	toolDefinitions := availableTools()
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
			result, err := executeTool(ctx, call, output)
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
	return fmt.Errorf("reached maximum model turns (%d)", maxTurns)
}
