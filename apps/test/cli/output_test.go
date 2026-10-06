package cli_test

import (
	"bytes"
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
