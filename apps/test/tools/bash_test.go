package tools_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/tools"
)

func TestRunBash_OutputAndExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		want    string
		exit    int
	}{
		{name: "stdout and stderr", command: "printf 'out\\n'; printf 'err\\n' >&2", want: "out\nerr\n"},
		{name: "empty output", command: ":"},
		{name: "nonzero exit", command: "printf partial; exit 7", want: "partial", exit: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			result, err := tools.RunBash(context.Background(), tc.command, &stdout)
			if tc.exit == 0 && err != nil {
				t.Fatalf("run bash: %v", err)
			}
			if tc.exit != 0 {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.exit {
					t.Fatalf("expected exit %d, got %v", tc.exit, err)
				}
			}
			if result != tc.want || stdout.String() != tc.want {
				t.Fatalf("result = %q, stdout = %q", result, stdout.String())
			}
		})
	}
}

func TestRunBash_RejectsEmptyCommand(t *testing.T) {
	if _, err := tools.RunBash(context.Background(), " \n", io.Discard); err == nil {
		t.Fatal("expected an error for empty command")
	}
}

func TestRunBash_WorkingDirectory(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	result, err := tools.RunBash(context.Background(), "pwd -P", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result) != want {
		t.Fatalf("working directory = %q, want %q", result, want)
	}
}

func TestRunBash_TruncatesOutput(t *testing.T) {
	var stdout bytes.Buffer
	result, err := tools.RunBash(context.Background(), "printf '%070000d' 0", &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) >= 70000 || !strings.HasSuffix(result, "\n[output truncated]\n") || result != stdout.String() {
		t.Fatalf("output length = %d, terminal length = %d", len(result), stdout.Len())
	}
}

func TestRunBash_CancelsChildProcesses(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	t.Setenv("MINICODE_TEST_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := tools.RunBash(ctx, `sh -c 'sleep 1; printf leaked > "$MINICODE_TEST_MARKER"' & wait`, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	// 等待子进程原本会写文件的时刻,确保取消后没有继续执行。
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command continued after cancellation: %v", err)
	}
}

func TestRunBash_CleansUpBackgroundCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	t.Setenv("MINICODE_TEST_MARKER", marker)
	_, err := tools.RunBash(context.Background(), `sh -c 'sleep 1; printf leaked > "$MINICODE_TEST_MARKER"' >/dev/null 2>&1 &`, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("background command continued after shell exited: %v", err)
	}
}

// cancelOnWrite 只有收到实时输出后才取消命令。
type cancelOnWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

func TestRunBash_StreamsBeforeCommandFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stdout := &cancelOnWrite{cancel: cancel}
	result, err := tools.RunBash(ctx, "printf ready; sleep 10", stdout)
	if !errors.Is(err, context.Canceled) || result != "ready" || stdout.String() != "ready" {
		t.Fatalf("result = %q, stdout = %q, error = %v", result, stdout.String(), err)
	}
}
