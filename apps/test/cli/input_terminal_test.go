//go:build darwin || linux

package cli_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// 子进程提供真实 stdin/stdout 终端，覆盖 readline 的 raw 模式和重绘路径。
func TestInputTerminalHelper(t *testing.T) {
	if os.Getenv("MINICODE_INPUT_TEST_HELPER") != "1" {
		return
	}
	input, err := cli.NewInput(os.Stdin, os.Stdout, os.Stderr, true)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	for {
		line, err := input.Readline("> ")
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("submitted=%q\n", line)
	}
}

func TestInputTerminalMultiline(t *testing.T) {
	send, expect := inputTerminal(t, 60, 12)
	expect([]string{"> "}, 2, 0)
	send("first\x1b[13;2usecond")
	expect([]string{"> first", "second"}, 6, 1)
	send("\x1b[A\x1b[DX")
	expect([]string{"> firsXt", "second"}, 7, 0)
	send("\x1b[B\x1b[F\nthird")
	expect([]string{"> firsXt", "second", "third"}, 5, 2)
	send("\x1b[A\x1b[H\x7f")
	expect([]string{"> firsXtsecond", "third"}, 8, 0)
	send("\r")
	expect([]string{"> firsXtsecond", "third", `submitted="firsXtsecond\nthird"`, "> "}, 2, 3)
	// 新一轮空输入上的上下键不会调出历史记录。
	send("\x1b[A\x1b[Bfresh")
	expect([]string{"> firsXtsecond", "third", `submitted="firsXtsecond\nthird"`, "> fresh"}, 7, 3)
}

func TestInputTerminalWrappedLines(t *testing.T) {
	send, expect := inputTerminal(t, 12, 8)
	expect([]string{"> "}, 2, 0)
	send("abc\n123456789012")
	expect([]string{"> abc", "12345678901", "2"}, 1, 2)
	send("\x1b[AX")
	expect([]string{"> abc", "1X234567890", "12"}, 2, 1)
	send("\x1b[B\x1b[DY")
	expect([]string{"> abc", "1X234567890", "1Y2"}, 2, 2)
	send("\x1b[BZ")
	expect([]string{"> abc", "1X234567890", "1YZ2"}, 3, 2)
}

func TestInputTerminalScrollsWithCursor(t *testing.T) {
	send, expect := inputTerminal(t, 20, 5)
	expect([]string{"> "}, 2, 0)
	send("one\ntwo\nthree\nfour\nfive\nsix")
	expect([]string{"three", "four", "five", "six"}, 3, 3)
	send(strings.Repeat("\x1b[A", 5) + "X")
	expect([]string{"> oneX", "two", "three", "four"}, 6, 0)
	send(strings.Repeat("\x1b[B", 5) + "Y")
	expect([]string{"three", "four", "five", "sixY"}, 4, 3)
}

func TestInputTerminalWideCharacterCursor(t *testing.T) {
	send, expect := inputTerminal(t, 24, 8)
	expect([]string{"> "}, 2, 0)
	send("中文abc\n12345\x1b[AX")
	expect([]string{"> 中文aXbc", "12345"}, 8, 0)
}

func inputTerminal(t *testing.T, width, height int) (func(string), func([]string, int, int)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInputTerminalHelper$")
	command.Env = append(os.Environ(), "MINICODE_INPUT_TEST_HELPER=1", "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		cancel()
		terminal.Close()
		<-done
	})
	screen := vt10x.New(vt10x.WithSize(width, height), vt10x.WithWriter(terminal))
	go func() {
		reader := bufio.NewReader(terminal)
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
			// vt10x 会把 Kitty 的协议开关误解为 ANSI 光标恢复。
			// 忽略这两个不影响显示的控制序列；协议生命周期由独立用例验证。
			if sequence != "\x1b[>1u" && sequence != "\x1b[<u" {
				screen.Write([]byte(sequence))
			}
		}
	}()
	send := func(keys string) {
		t.Helper()
		if _, err := io.WriteString(terminal, keys); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(want []string, x, y int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		var rows []string
		var cursor vt10x.Cursor
		for time.Now().Before(deadline) {
			screen.Lock()
			rows = make([]string, height)
			for row := range rows {
				var contents strings.Builder
				for col := 0; col < width; col++ {
					contents.WriteRune(screen.Cell(col, row).Char)
				}
				rows[row] = contents.String()
			}
			cursor = screen.Cursor()
			screen.Unlock()
			matches := cursor.X == x && cursor.Y == y
			for n, row := range rows {
				expected := ""
				if n < len(want) {
					expected = want[n]
				}
				matches = matches && strings.TrimRight(row, " ") == strings.TrimRight(expected, " ")
			}
			if matches {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("terminal rows = %q, cursor = (%d,%d); want %q, cursor = (%d,%d)", rows, cursor.X, cursor.Y, want, x, y)
	}
	return send, expect
}
