// Package cli 提供 MiniCode 命令行界面的输出与终端展示能力。
package cli

import (
	"fmt"
	"io"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// Output 将 Agent 的最终回复、任务错误和审批请求展示到 CLI。
type Output struct {
	stdout io.Writer
	stderr io.Writer
}

// NewOutput 创建 CLI 输出适配器。interactive 保留用于兼容调用方；运行过程不再展示。
func NewOutput(stdout, stderr io.Writer, _ bool) *Output {
	return &Output{stdout: stdout, stderr: stderr}
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

// ToolError 展示无法继续由模型处理的任务错误。
func (o *Output) ToolError(err error) {
	_, _ = fmt.Fprintln(o.stderr, "minicode: "+err.Error())
}
