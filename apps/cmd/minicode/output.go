package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/terminal"
)

// cliOutput 将 Agent 的运行过程展示到 CLI 的标准输出和错误输出。
type cliOutput struct {
	stdout io.Writer
	stderr io.Writer
}

// Write 原样展示工具执行时产生的输出。
func (o *cliOutput) Write(p []byte) (int, error) {
	return o.stdout.Write(p)
}

// Message 渲染并展示模型回复。
func (o *cliOutput) Message(content string) {
	fmt.Fprint(o.stdout, terminal.RenderMarkdown(o.stdout, content))
}

// ToolCall 在执行前展示工具名称与参数。
func (o *cliOutput) ToolCall(call provider.ToolCall) {
	fmt.Fprintln(o.stdout, "tool: "+call.Function.Name)
	fmt.Fprintln(o.stdout, "arguments: "+call.Function.Arguments)
}

// ToolResult 为未以换行结尾的工具输出补上换行。
func (o *cliOutput) ToolResult(result string) {
	if result != "" && !strings.HasSuffix(result, "\n") {
		fmt.Fprintln(o.stdout)
	}
}

// ToolError 展示会回传模型的工具错误。
func (o *cliOutput) ToolError(err error) {
	fmt.Fprintln(o.stderr, "minicode: "+err.Error())
}
