// Package cli 提供 MiniCode 命令行界面的输出与终端展示能力。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/provider"
)

// Output 将 Agent 的最终回复、任务错误和审批请求展示到 CLI。
type Output struct {
	stdout      io.Writer
	stderr      io.Writer
	interactive bool
}

// NewOutput 创建 CLI 输出适配器。实时过程只展示在交互终端，避免污染管道输出。
func NewOutput(stdout, stderr io.Writer, interactive bool) *Output {
	return &Output{stdout: stdout, stderr: stderr, interactive: interactive}
}

// ModelStart 展示当前模型调用轮次。
func (o *Output) ModelStart(turn int) {
	if !o.interactive {
		return
	}
	_, _ = fmt.Fprintf(o.stdout, "[turn %d] waiting for model\n", turn)
}

// ToolStart 展示即将执行的工具；参数和结果不在过程事件中输出。
func (o *Output) ToolStart(turn int, call provider.ToolCall) {
	if !o.interactive {
		return
	}
	_, _ = fmt.Fprintf(o.stdout, "[turn %d] calling %s\n", turn, call.Function.Name)
}

// ToolDone 展示工具终态和耗时。
func (o *Output) ToolDone(name string, status agent.ToolStatus, duration time.Duration) {
	if !o.interactive {
		return
	}
	_, _ = fmt.Fprintf(o.stdout, "[state] %s %s in %s\n", name, status, formatDuration(duration))
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(100 * time.Millisecond).String()
}

// Message 渲染并展示模型最终回复。
func (o *Output) Message(content string) {
	_, _ = fmt.Fprint(o.stdout, RenderMarkdown(o.stdout, content))
}

// ToolCall 只在等待用户审批时展示工具名称与参数。
func (o *Output) ToolCall(call provider.ToolCall) {
	_, _ = fmt.Fprintln(o.stdout, "tool: "+call.Function.Name)
	_, _ = fmt.Fprintln(o.stdout, "arguments: "+call.Function.Arguments)
}

// ToolError 展示无法继续由模型处理的任务错误，并将取消与超时映射为明确提示。
func (o *Output) ToolError(err error) {
	message := err.Error()
	switch {
	case errors.Is(err, context.Canceled):
		message = "任务已中断"
	case errors.Is(err, context.DeadlineExceeded):
		message = "任务执行超时"
	}
	_, _ = fmt.Fprintln(o.stderr, "minicode: "+message)
}
