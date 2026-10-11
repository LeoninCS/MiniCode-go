//go:build darwin || linux

package cli_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

func TestOutputTerminalPreviewReflowsPartialMarkdown(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "dracula")
	output, screen, _ := previewTerminal(t, 60, 14)
	output.StreamEvent(provider.StreamEvent{Content: "# Stream title\n\n**bol"})
	expectPreviewScreen(t, screen, func(text string) bool { return strings.Contains(text, "bol") })
	output.StreamEvent(provider.StreamEvent{Content: "d**\n\n- first item\n\n```go\nfmt.Println(\"hello\")"})
	expectPreviewScreen(t, screen, func(text string) bool {
		cleanBoldLine := false
		for _, row := range strings.Split(text, "\n") {
			cleanBoldLine = cleanBoldLine || strings.TrimSpace(row) == "bold"
		}
		return cleanBoldLine && strings.Contains(text, "first item") && strings.Contains(text, "hello") && !strings.Contains(text, "```")
	})
	// 表头和分隔符分次到达，后续内容能改变已有行的排版。
	output.StreamEvent(provider.StreamEvent{Content: "\n```\n\n| Name | Value |\n| ---"})
	expectPreviewScreen(t, screen, func(text string) bool { return strings.Contains(text, "Name") })
	output.StreamEvent(provider.StreamEvent{Content: " | --- |\n| answer | 42 |"})
	expectPreviewScreen(t, screen, func(text string) bool { return strings.Contains(text, "answer") && strings.Contains(text, "42") })
	output.StreamDone()
	output.Message("completed")
	expectPreviewScreen(t, screen, func(text string) bool {
		return strings.Contains(text, "completed") && !strings.Contains(text, "first item")
	})
	screen.Lock()
	defer screen.Unlock()
	if screen.Mode()&vt10x.ModeAltScreen != 0 || !screen.CursorVisible() {
		t.Fatal("alternate screen or hidden cursor remained after completion")
	}
}

func TestOutputTerminalPreviewFollowsTailAndResizesWithoutNewDeltas(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "dracula")
	output, screen, resize := previewTerminal(t, 44, 8)
	var content strings.Builder
	content.WriteString("```text\n")
	for i := range 30 {
		fmt.Fprintf(&content, "line-%02d\n", i)
	}
	content.WriteString(strings.Repeat("中", 12) + " tail-end")
	output.StreamEvent(provider.StreamEvent{Content: content.String()})
	expectPreviewScreen(t, screen, func(text string) bool {
		return strings.Contains(text, "line-29") && strings.Contains(text, "tail-end") && !strings.Contains(text, "line-00")
	})
	resize(20, 5)
	expectPreviewScreen(t, screen, func(text string) bool {
		return strings.Contains(text, "tail-end") && !strings.Contains(text, "line-29") && strings.Count(text, "中") == 12
	})
	resize(55, 9)
	expectPreviewScreen(t, screen, func(text string) bool {
		return strings.Contains(text, "line-29") && strings.Contains(text, strings.Repeat("中", 12)+" tail-end") && strings.Count(text, "tail-end") == 1
	})
	output.StreamDone()
	output.Message("done")
	expectPreviewScreen(t, screen, func(text string) bool { return strings.Contains(text, "done") && !strings.Contains(text, "tail-end") })
}

func TestOutputTerminalPreviewClearsOldRowsAndFallbackIsReadable(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "missing-stream-style.json")
	output, screen, _ := previewTerminal(t, 25, 8)
	output.StreamEvent(provider.StreamEvent{Content: "raw-" + strings.Repeat("x", 18)})
	expectPreviewScreen(t, screen, func(text string) bool { return strings.Contains(text, "raw-"+strings.Repeat("x", 18)) })
	output.StreamDone()
	output.StreamEvent(provider.StreamEvent{Content: "short\t中文"})
	expectPreviewScreen(t, screen, func(text string) bool {
		return strings.Contains(text, "short    中文") && !strings.Contains(text, "raw-") && !strings.Contains(text, "xxx")
	})
	output.StreamDone()
}

func previewTerminal(t *testing.T, width, height int) (*cli.Output, vt10x.Terminal, func(int, int)) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	screen := vt10x.New(vt10x.WithSize(width, height))
	output := cli.NewOutput(slave, &bytes.Buffer{}, true)
	done := make(chan struct{})
	t.Cleanup(func() {
		output.StreamDone()
		_ = slave.Close()
		_ = master.Close()
		<-done
	})
	go func() {
		defer close(done)
		reader := bufio.NewReader(master)
		for {
			r, _, err := reader.ReadRune()
			if err != nil {
				return
			}
			sequence := string(r)
			// 整条 CSI 序列一起交给模拟终端，避免测试读取半帧控制状态。
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
			_, _ = io.WriteString(screen, sequence)
		}
	}()
	resize := func(w, h int) {
		t.Helper()
		screen.Resize(w, h)
		if err := pty.Setsize(slave, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)}); err != nil {
			t.Fatal(err)
		}
	}
	resize(width, height)
	return output, screen, resize
}

func expectPreviewScreen(t *testing.T, screen vt10x.Terminal, matches func(string) bool) {
	t.Helper()
	var text string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		screen.Lock()
		width, height := screen.Size()
		var contents strings.Builder
		for row := range height {
			for col := range width {
				contents.WriteRune(screen.Cell(col, row).Char)
			}
			contents.WriteByte('\n')
		}
		text = contents.String()
		screen.Unlock()
		if matches(text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("unexpected terminal content:\n%s", text)
}
