// Package cli 提供 MiniCode 命令行界面的输出与终端展示能力。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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

func formatTaskDuration(duration time.Duration) string {
	formatted := formatDuration(duration)
	if formatted == "<1ms" {
		return "<1 毫秒"
	}
	if strings.HasSuffix(formatted, "ms") {
		return strings.TrimSuffix(formatted, "ms") + " 毫秒"
	}
	return strings.TrimSuffix(formatted, "s") + " 秒"
}

func formatCount(value int) string {
	digits := fmt.Sprintf("%d", value)
	start := 0
	if strings.HasPrefix(digits, "-") {
		start = 1
	}
	for i := len(digits) - 3; i > start; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiBlue   = "\x1b[34m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// TaskDone 展示单项任务的累计统计。交互模式写入 stdout 并使用 ANSI 颜色，
// 管道模式写入 stderr 且保持纯文本，避免控制字符污染日志或后续处理。
func (o *Output) TaskDone(stats agent.TaskStats) {
	writer := o.stderr
	if o.interactive {
		writer = o.stdout
	}

	parts := []string{
		formatTaskDuration(stats.Duration),
		fmt.Sprintf("模型 %s 次", formatCount(stats.ModelCalls)),
		fmt.Sprintf("工具 %s 次", formatCount(stats.ToolCalls)),
	}
	if stats.UsageResponses == 0 {
		parts = append(parts, "Token 不可用")
	} else {
		tokens := fmt.Sprintf("Token %s（输入 %s / 输出 %s）",
			formatCount(stats.TotalTokens), formatCount(stats.InputTokens), formatCount(stats.OutputTokens))
		if stats.UsageResponses < stats.ModelCalls {
			tokens += "（部分统计）"
		}
		parts = append(parts, tokens)
	}

	if !o.interactive {
		_, _ = fmt.Fprintln(writer, "[统计] "+strings.Join(parts, " ｜ "))
		return
	}

	colored := []string{
		ansiGreen + parts[0] + ansiReset,
		ansiBlue + parts[1] + ansiReset,
		ansiBlue + parts[2] + ansiReset,
	}
	tokenColor := ansiGreen
	if stats.UsageResponses == 0 || stats.UsageResponses < stats.ModelCalls {
		tokenColor = ansiYellow
	}
	colored = append(colored, tokenColor+parts[3]+ansiReset)
	separator := " " + ansiDim + "｜" + ansiReset + " "
	prefix := ansiBold + ansiCyan + "[统计]" + ansiReset + " "
	_, _ = fmt.Fprintln(writer, prefix+strings.Join(colored, separator))
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
