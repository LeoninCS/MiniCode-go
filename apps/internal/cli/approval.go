package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/ergochat/readline"
)

const approvalPrompt = "允许执行？[Y/n] "

// toolApprover 使用与任务输入相同的终端，请用户逐次批准有副作用的工具。
type toolApprover struct {
	input       agent.Input
	output      *Output
	autoApprove bool
}

func (a *toolApprover) Approve(ctx context.Context, content string, call provider.ToolCall) (bool, error) {
	// 无论是否自动批准，都展示与手动审批相同的模型说明和工具调用；
	// --yes 只跳过用户选择，不隐藏执行过程。
	if content != "" {
		a.output.Message(content)
	}
	a.output.ToolCall(call)
	if a.autoApprove {
		return true, nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		answer, err := a.input.Readline(approvalPrompt)
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				return false, context.Canceled
			}
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, fmt.Errorf("read tool approval: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			_, _ = fmt.Fprintln(a.output.stderr, "请输入 y/yes 批准，或 n/no 拒绝。")
		}
	}
}
