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

const approvalPrompt = "允许执行？[y/N] "

// toolApprover 使用与任务输入相同的终端，请用户逐次批准有副作用的工具。
type toolApprover struct {
	input       agent.Input
	output      *Output
	autoApprove bool
}

func (a *toolApprover) Approve(ctx context.Context, _ provider.ToolCall) (bool, error) {
	if a.autoApprove {
		return true, nil
	}

	// Agent 已恢复主屏幕并展示完整指令；这里仅负责读取判断。
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
			fmt.Fprintln(a.output.stderr, "请输入 y/yes 批准，或 n/no 拒绝。")
		}
	}
}
