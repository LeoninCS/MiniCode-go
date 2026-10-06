package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestOutputShowsFinalReplyAndApprovalOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := cli.NewOutput(&stdout, &stderr, true)

	output.ToolCall(provider.ToolCall{Function: provider.FunctionCall{
		Name:      "bash",
		Arguments: `{"command":"go test ./..."}`,
	}})
	output.Message("final answer")

	want := "tool: bash\narguments: {\"command\":\"go test ./...\"}\nfinal answer\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
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
