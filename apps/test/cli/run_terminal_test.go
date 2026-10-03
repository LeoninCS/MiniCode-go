//go:build darwin || linux

package cli_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestMiniCode_TerminalCtrlC(t *testing.T) {
	binary := buildMiniCode(t)
	for _, tc := range []struct {
		name    string
		keys    string
		running bool
		initial bool
	}{
		{name: "empty input legacy", keys: "\x03"},
		{name: "empty input kitty", keys: "\x1b[99;5u"},
		{name: "multiline input kitty", keys: "draft\ntext\x1b[99;5u"},
		{name: "multiline input xterm", keys: "draft\ntext\x1b[27;5;99~"},
		{name: "waiting for model", running: true},
		{name: "initial model request", running: true, initial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			canceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
				close(canceled)
			}))
			t.Cleanup(server.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			args := []string{"-api-key", "test-key", "-base-url", server.URL, "-model", "test-model"}
			if tc.initial {
				args = append(args, "request")
			}
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = t.TempDir()
			command.Env = append(os.Environ(), "TERM=xterm-256color")
			terminal, err := pty.StartWithSize(command, &pty.Winsize{Cols: 60, Rows: 12})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			exited := make(chan struct{})
			var exitErr error
			go func() {
				exitErr = command.Wait()
				close(exited)
			}()
			t.Cleanup(func() {
				cancel()
				_ = terminal.Close()
				<-exited
			})
			chunks := make(chan string, 32)
			go func() {
				defer close(chunks)
				buffer := make([]byte, 4096)
				for {
					n, err := terminal.Read(buffer)
					if n > 0 {
						select {
						case chunks <- string(buffer[:n]):
						case <-ctx.Done():
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			var output strings.Builder
			waitForOutput := func(marker string) {
				t.Helper()
				deadline := time.NewTimer(3 * time.Second)
				defer deadline.Stop()
				for !strings.Contains(output.String(), marker) {
					select {
					case chunk, ok := <-chunks:
						if !ok {
							t.Fatalf("terminal closed before %q: %q", marker, output.String())
						}
						output.WriteString(chunk)
					case <-deadline.C:
						t.Fatalf("waiting for %q: %q", marker, output.String())
					}
				}
			}
			send := func(keys string) {
				t.Helper()
				if _, err := io.WriteString(terminal, keys); err != nil {
					t.Fatal(err)
				}
			}

			if !tc.initial {
				waitForOutput("\x1b[?25h") // 输入框已进入 raw 模式并完成首次绘制。
			}
			if tc.running {
				if !tc.initial {
					send("request\r")
				}
				waitForOutput("\x1b[?1049h")
				if strings.Count(output.String(), "\x1b[>1u") != strings.Count(output.String(), "\x1b[<u") {
					t.Fatalf("extended keyboard protocol still enabled while running: %q", output.String())
				}
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("model request did not start")
				}
				send("\x03") // 普通键盘模式下，由内核生成 SIGINT。
			} else {
				send(tc.keys)
			}
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				t.Fatal("Ctrl+C did not exit the process")
			}
			// 收齐退出前的控制序列，确认没有把键盘协议或备用屏幕留给 shell。
			for chunk := range chunks {
				output.WriteString(chunk)
			}
			for _, pair := range [][2]string{{"\x1b[>1u", "\x1b[<u"}, {"\x1b[?1049h", "\x1b[?1049l"}} {
				if strings.Count(output.String(), pair[0]) != strings.Count(output.String(), pair[1]) {
					t.Fatalf("terminal state not restored after exit: %q", output.String())
				}
			}
			var processError *exec.ExitError
			if !errors.As(exitErr, &processError) || processError.ExitCode() != 1 {
				t.Fatalf("exit = %v; want handled interrupt with exit code 1", exitErr)
			}
			if tc.running {
				select {
				case <-canceled:
				case <-time.After(3 * time.Second):
					t.Fatal("model request was not canceled")
				}
			} else {
				select {
				case <-started:
					t.Fatal("Ctrl+C submitted the draft to the model")
				default:
				}
			}
		})
	}
}
