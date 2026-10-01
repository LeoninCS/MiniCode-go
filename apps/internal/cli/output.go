// Package cli 提供 MiniCode 命令行界面的输出与终端展示能力。
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// Output 将 Agent 的运行过程展示到 CLI 的标准输出和错误输出。
type Output struct {
	stdout io.Writer
	stderr io.Writer

	// interactive 只在真实终端中启用游标控制。重定向到管道或文件时保留完整运行日志。
	interactive bool
	// running 标记当前轮次是否已经保存了“运行中输出”的起始游标。
	running bool
}

// NewOutput 创建 CLI 输出适配器。interactive 应在输出连接真实终端时设为 true。
func NewOutput(stdout, stderr io.Writer, interactive bool) *Output {
	return &Output{stdout: stdout, stderr: stderr, interactive: interactive}
}

// BeginRunning 保存本轮运行输出的起始位置。最终回答到达时会回到这里清理临时输出。
func (o *Output) BeginRunning() {
	if !o.interactive {
		return
	}
	fmt.Fprint(o.stdout, "\x1b[s")
	o.running = true
}

// ClearRunning 删除本轮工具调用和实时输出，但不会影响起始位置之前的历史回答。
func (o *Output) ClearRunning() {
	if !o.interactive || !o.running {
		return
	}
	// 恢复保存的游标并清除到屏幕末尾，让最终回答紧接在上一轮内容下面。
	fmt.Fprint(o.stdout, "\x1b[u\x1b[J")
	o.running = false
}

// Write 原样展示工具执行时产生的输出。
func (o *Output) Write(p []byte) (int, error) {
	return o.stdout.Write(p)
}

// Message 渲染并展示模型回复。
func (o *Output) Message(content string) {
	fmt.Fprint(o.stdout, RenderMarkdown(o.stdout, content))
}

// ToolCall 在执行前展示工具名称与参数。
func (o *Output) ToolCall(call provider.ToolCall) {
	fmt.Fprintln(o.stdout, "tool: "+call.Function.Name)
	fmt.Fprintln(o.stdout, "arguments: "+call.Function.Arguments)
}

// ToolResult 为未以换行结尾的工具输出补上换行。
func (o *Output) ToolResult(result string) {
	if result != "" && !strings.HasSuffix(result, "\n") {
		fmt.Fprintln(o.stdout)
	}
}

// ToolError 展示会回传模型的工具错误。
func (o *Output) ToolError(err error) {
	fmt.Fprintln(o.stderr, "minicode: "+err.Error())
}
