package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestOutputShowsProcessFinalReplyAndApproval(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := cli.NewOutput(&stdout, &stderr, true)
	call := provider.ToolCall{Function: provider.FunctionCall{
		Name:      "bash",
		Arguments: `{"command":"go test ./..."}`,
	}}

	output.ModelStart(1)
	output.ToolStart(1, call)
	output.ToolDone("bash", agent.ToolCompleted, 1200*time.Millisecond)
	output.ToolCall(call)
	output.Message("final answer")
	output.TaskDone(agent.TaskStats{
		Duration: 2300 * time.Millisecond, ModelCalls: 2, ToolCalls: 1,
		InputTokens: 3200, OutputTokens: 540, TotalTokens: 3740, UsageResponses: 2,
	})

	want := "[turn 1] waiting for model\n" +
		"[turn 1] calling bash\n" +
		"[state] bash completed in 1.2s\n" +
		"tool: bash\narguments: {\"command\":\"go test ./...\"}\nfinal answer\n" +
		"\x1b[1m\x1b[36m[统计]\x1b[0m \x1b[32m2.3 秒\x1b[0m \x1b[2m｜\x1b[0m " +
		"\x1b[34m模型 2 次\x1b[0m \x1b[2m｜\x1b[0m \x1b[34m工具 1 次\x1b[0m \x1b[2m｜\x1b[0m " +
		"\x1b[32mToken 3,740（输入 3,200 / 输出 540）\x1b[0m\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestOutputHidesProcessInNonInteractiveMode(t *testing.T) {
	var stdout bytes.Buffer
	output := cli.NewOutput(&stdout, &bytes.Buffer{}, false)
	call := provider.ToolCall{Function: provider.FunctionCall{Name: "read"}}
	output.ModelStart(1)
	output.ToolStart(1, call)
	output.ToolDone("read", agent.ToolFailed, time.Millisecond)
	output.Message("final")
	if got, want := stdout.String(), "final\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestOutputTaskStatsInNonInteractiveMode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats agent.TaskStats
		want  string
	}{
		{
			name:  "unavailable usage",
			stats: agent.TaskStats{Duration: 500 * time.Microsecond, ModelCalls: 1},
			want:  "[统计] <1 毫秒 ｜ 模型 1 次 ｜ 工具 0 次 ｜ Token 不可用\n",
		},
		{
			name:  "partial usage",
			stats: agent.TaskStats{Duration: 1250 * time.Millisecond, ModelCalls: 2, ToolCalls: 3, InputTokens: 10000, OutputTokens: 5000, TotalTokens: 15000, UsageResponses: 1},
			want:  "[统计] 1.3 秒 ｜ 模型 2 次 ｜ 工具 3 次 ｜ Token 15,000（输入 10,000 / 输出 5,000）（部分统计）\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			output := cli.NewOutput(&stdout, &stderr, false)
			output.TaskDone(tc.stats)
			if stdout.Len() != 0 || stderr.String() != tc.want {
				t.Fatalf("stdout = %q, stderr = %q, want stderr %q", stdout.String(), stderr.String(), tc.want)
			}
		})
	}
}

func TestOutputToolErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "canceled", err: fmt.Errorf("provider: %w", context.Canceled), want: "minicode: 任务已中断\n"},
		{name: "deadline exceeded", err: fmt.Errorf("bash: %w", context.DeadlineExceeded), want: "minicode: 任务执行超时\n"},
		{name: "ordinary failure", err: errors.New("provider api error: invalid key"), want: "minicode: provider api error: invalid key\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			output := cli.NewOutput(&bytes.Buffer{}, &stderr, false)
			output.ToolError(tc.err)
			if got := stderr.String(); got != tc.want {
				t.Fatalf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}
