package cli_test

import (
	"bytes"
	"testing"

	"github.com/MiniCode-go/minicode/internal/cli"
)

func TestOutputRunningOutput(t *testing.T) {
	t.Run("interactive clears only current running area", func(t *testing.T) {
		var stdout bytes.Buffer
		output := cli.NewOutput(&stdout, &bytes.Buffer{}, true)

		output.BeginRunning()
		_, _ = output.Write([]byte("tool output\n"))
		output.ClearRunning()
		output.Message("final answer")

		want := "\x1b[stool output\n\x1b[u\x1b[Jfinal answer\n"
		if got := stdout.String(); got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("redirected output keeps complete log", func(t *testing.T) {
		var stdout bytes.Buffer
		output := cli.NewOutput(&stdout, &bytes.Buffer{}, false)

		output.BeginRunning()
		_, _ = output.Write([]byte("tool output\n"))
		output.ClearRunning()
		output.Message("final answer")

		want := "tool output\nfinal answer\n"
		if got := stdout.String(); got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("clear is idempotent", func(t *testing.T) {
		var stdout bytes.Buffer
		output := cli.NewOutput(&stdout, &bytes.Buffer{}, true)
		output.BeginRunning()
		output.ClearRunning()
		output.ClearRunning()

		if got, want := stdout.String(), "\x1b[s\x1b[u\x1b[J"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}
