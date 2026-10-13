//go:build darwin || linux

package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/charmbracelet/x/termios"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

func TestSessionScreenHelper(t *testing.T) {
	if os.Getenv("MINICODE_SCREEN_TEST_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "previous-shell-output")
	before, err := termios.GetTermios(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	code := cli.Run(nil, os.Stdin, os.Stdout, os.Stderr)
	after, err := termios.GetTermios(int(os.Stdin.Fd()))
	if err == nil {
		// 恢复 canonical 模式时内核可设置待重输标志，它不是遗留的 raw/echo 配置。
		before.Lflag &^= syscall.PENDIN
		after.Lflag &^= syscall.PENDIN
	}
	if err != nil || !reflect.DeepEqual(before, after) {
		fmt.Fprintf(os.Stderr, "terminal settings were not restored: before=%#v after=%#v error=%v\n", before, after, err)
		os.Exit(97)
	}
	os.Exit(code)
}

func TestMiniCode_FullScreenLongInputViewport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("editing the draft must not submit a model request")
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	terminal := newSessionTerminal(t, server.URL, 40, 12)
	terminal.expect(func(state terminalState) bool { return state.promptAtBottom("> ") && state.visible })
	terminal.send("one\n中文two\nthree\nfour\nfive\nsix\nseven")
	terminal.expect(func(state terminalState) bool {
		return state.cursor.Y == 11 && state.cursor.X == 5 && strings.TrimSpace(state.rows[8]) == "four" && strings.TrimSpace(state.rows[11]) == "seven"
	})
	terminal.send(strings.Repeat("\x1b[A", 6) + "X")
	terminal.expect(func(state terminalState) bool {
		return state.cursor.Y == 8 && state.cursor.X == 6 && strings.TrimSpace(state.rows[8]) == "> oneX" && strings.TrimSpace(state.rows[9]) == "中文two"
	})
	terminal.resize(12, 7)
	terminal.expect(func(state terminalState) bool {
		return len(state.rows) == 7 && state.cursor.Y == 5 && strings.TrimSpace(state.rows[5]) == "> oneX" && strings.TrimSpace(state.rows[6]) == "中文two"
	})
	terminal.send(strings.Repeat("\x1b[B", 6) + "\x1b[FY")
	terminal.expect(func(state terminalState) bool {
		return state.cursor.Y == 6 && state.cursor.X == 6 && strings.TrimSpace(state.rows[6]) == "sevenY"
	})
	terminal.send("\x15")
	terminal.exit(0)
}

func TestMiniCode_FullScreenInputStreamingAndResize(t *testing.T) {
	requests := make(chan provider.ChatRequest, 4)
	finish := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			var content strings.Builder
			content.WriteString("```text\n")
			for n := range 30 {
				fmt.Fprintf(&content, "reply-line-%02d\n", n)
			}
			content.WriteString("stream-tail\n```")
			encoded, _ := json.Marshal(content.String())
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", encoded)
			w.(http.Flusher).Flush()
			select {
			case <-finish:
				io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			case <-r.Context().Done():
			}
			return
		}
		writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "second-answer", ""), FinishReason: "stop"}}})
	}))
	t.Cleanup(server.Close)
	terminal := newSessionTerminal(t, server.URL, 60, 14)
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && state.visible && strings.Contains(state.text(), "[会话]") && !strings.Contains(state.text(), "previous-shell-output")
	})
	terminal.send("中文first\x1b[13;2usecond")
	terminal.expect(func(state terminalState) bool {
		return strings.TrimSpace(state.rows[len(state.rows)-2]) == "> 中文first" && strings.TrimSpace(state.rows[len(state.rows)-1]) == "second" && state.cursor.Y == len(state.rows)-1 && state.cursor.X == 6
	})
	terminal.send("\x1b[A\x1b[FX\r")
	request := waitScreenRequest(t, requests)
	if got := request.Messages[len(request.Messages)-1].Text(); got != "中文firstX\nsecond" {
		t.Fatalf("submitted input = %q", got)
	}
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && strings.Contains(state.text(), "stream-tail") && !strings.Contains(state.text(), "reply-line-00")
	})
	// 没有新增流事件时，窗口变化也应重新排版且保持底部输入框。
	terminal.resize(24, 8)
	terminal.expect(func(state terminalState) bool {
		return len(state.rows) == 8 && state.promptAtBottom("> ") && strings.Contains(state.text(), "stream-tail")
	})
	terminal.resize(60, 14)
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && strings.Contains(state.text(), "reply-line-29")
	})
	close(finish)
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && state.visible && strings.Contains(state.text(), "[统计]")
	})
	terminal.send("\x1b[5~")
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && !strings.Contains(state.text(), "stream-tail") && strings.Contains(state.text(), "reply-line-")
	})
	terminal.send("\x1b[6~")
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && strings.Contains(state.text(), "stream-tail")
	})
	terminal.send("follow-up\r")
	waitScreenRequest(t, requests)
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && state.visible && strings.Contains(state.text(), "second-answer")
	})
	terminal.exit(0)
}

func TestMiniCode_FullScreenApprovalAndError(t *testing.T) {
	requests := make(chan provider.ChatRequest, 4)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		switch calls.Add(1) {
		case 1:
			message := provider.NewMessage(provider.RoleAssistant, "需要执行命令。", "")
			message.ToolCalls = []provider.ToolCall{{ID: "approval", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"true"}`}}}
			writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: message, FinishReason: "tool_calls"}}})
		case 2:
			writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "denied-answer", ""), FinishReason: "stop"}}})
		default:
			http.Error(w, `{"error":{"message":"test-model-error"}}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	terminal := newSessionTerminal(t, server.URL, 80, 16)
	terminal.expect(func(state terminalState) bool { return state.promptAtBottom("> ") && state.visible })
	terminal.send("run a command\r")
	waitScreenRequest(t, requests)
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("允许执行？[Y/n] ") && strings.Contains(state.text(), "tool: bash") && state.visible
	})
	terminal.send("invalid\r")
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("允许执行？[Y/n] ") && strings.Contains(state.text(), "请输入 y/yes")
	})
	terminal.send("n\r")
	request := waitScreenRequest(t, requests)
	if got := request.Messages[len(request.Messages)-1].Text(); !strings.Contains(got, "denied by user") {
		t.Fatalf("tool result = %q", got)
	}
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && state.visible && strings.Contains(state.text(), "denied-answer")
	})
	terminal.send("next task\r")
	waitScreenRequest(t, requests)
	terminal.expect(func(state terminalState) bool {
		return state.promptAtBottom("> ") && state.visible && strings.Contains(state.text(), "test-model-error")
	})
	terminal.exit(1)
}

func waitScreenRequest(t *testing.T, requests <-chan provider.ChatRequest) provider.ChatRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not start")
		return provider.ChatRequest{}
	}
}

type terminalState struct {
	rows    []string
	cursor  vt10x.Cursor
	visible bool
	mode    vt10x.ModeFlag
}

func (state terminalState) text() string { return strings.Join(state.rows, "\n") }

func (state terminalState) promptAtBottom(prompt string) bool {
	return state.mode&vt10x.ModeAltScreen != 0 && strings.TrimRight(state.rows[len(state.rows)-1], " ") == strings.TrimRight(prompt, " ")
}

type sessionTerminal struct {
	t        *testing.T
	master   *os.File
	screen   vt10x.Terminal
	output   lockedBuffer
	exited   chan error
	readDone chan struct{}
}

func newSessionTerminal(t *testing.T, baseURL string, width, height int) *sessionTerminal {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSessionScreenHelper$")
	command.Dir = t.TempDir()
	writeModelConfig(t, command.Dir, baseURL)
	command.Env = append(os.Environ(), "MINICODE_SCREEN_TEST_HELPER=1", "TERM=xterm-256color", "GLAMOUR_STYLE=dracula")
	master, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	terminal := &sessionTerminal{t: t, master: master, screen: vt10x.New(vt10x.WithSize(width, height)), exited: make(chan error, 1), readDone: make(chan struct{})}
	go func() { terminal.exited <- command.Wait(); close(terminal.exited) }()
	go terminal.read()
	t.Cleanup(func() {
		cancel()
		master.Close()
		<-terminal.exited
		<-terminal.readDone
	})
	return terminal
}

func (terminal *sessionTerminal) read() {
	defer close(terminal.readDone)
	reader := bufio.NewReader(terminal.master)
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			return
		}
		sequence := string(r)
		if r == '\x1b' {
			next, err := reader.ReadByte()
			if err != nil {
				return
			}
			sequence += string(next)
			if next == '[' {
				for {
					next, err = reader.ReadByte()
					if err != nil {
						return
					}
					sequence += string(next)
					if next >= 0x40 && next <= 0x7e {
						break
					}
				}
			}
		}
		io.WriteString(&terminal.output, sequence)
		if sequence != "\x1b[>1u" && sequence != "\x1b[<u" {
			io.WriteString(terminal.screen, sequence)
		}
	}
}

func (terminal *sessionTerminal) send(keys string) {
	terminal.t.Helper()
	if _, err := io.WriteString(terminal.master, keys); err != nil {
		terminal.t.Fatal(err)
	}
}

func (terminal *sessionTerminal) resize(width, height int) {
	terminal.t.Helper()
	terminal.screen.Resize(width, height)
	if err := pty.Setsize(terminal.master, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)}); err != nil {
		terminal.t.Fatal(err)
	}
}

func (terminal *sessionTerminal) expect(matches func(terminalState) bool) {
	terminal.t.Helper()
	var state terminalState
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		terminal.screen.Lock()
		width, height := terminal.screen.Size()
		state = terminalState{rows: make([]string, height), cursor: terminal.screen.Cursor(), visible: terminal.screen.CursorVisible(), mode: terminal.screen.Mode()}
		for row := range height {
			var text strings.Builder
			for col := range width {
				text.WriteRune(terminal.screen.Cell(col, row).Char)
			}
			state.rows[row] = text.String()
		}
		terminal.screen.Unlock()
		if matches(state) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	output := terminal.output.String()
	terminal.t.Fatalf("unexpected screen, cursor=(%d,%d), visible=%v, mode=%v:\n%s\nrecent output: %q", state.cursor.X, state.cursor.Y, state.visible, state.mode, state.text(), output[max(0, len(output)-2500):])
}

func (terminal *sessionTerminal) exit(code int) {
	terminal.t.Helper()
	terminal.send("/quit\r")
	select {
	case err := <-terminal.exited:
		actual := 0
		if err != nil {
			if processError, ok := err.(*exec.ExitError); ok {
				actual = processError.ExitCode()
			} else {
				terminal.t.Fatal(err)
			}
		}
		if actual != code {
			output := terminal.output.String()
			terminal.t.Fatalf("exit code = %d; want %d; recent output=%q", actual, code, output[max(0, len(output)-1500):])
		}
	case <-time.After(3 * time.Second):
		terminal.t.Fatal("session did not exit")
	}
	<-terminal.readDone
	output := terminal.output.String()
	if strings.Count(output, "\x1b[?1049h") != 1 || strings.Count(output, "\x1b[?1049l") != 1 {
		terminal.t.Fatalf("session did not own one alternate screen: %q", output)
	}
	terminal.screen.Lock()
	defer terminal.screen.Unlock()
	if terminal.screen.Mode()&vt10x.ModeAltScreen != 0 || !terminal.screen.CursorVisible() {
		terminal.t.Fatal("terminal screen or cursor was not restored")
	}
}
