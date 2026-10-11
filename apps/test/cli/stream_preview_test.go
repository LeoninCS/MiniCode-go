package cli_test

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/charmbracelet/x/ansi"
)

// 预览由工作协程写入，测试读取输出时也要同步。
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func waitPreview(t *testing.T, out *lockedBuffer, text string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(ansi.Strip(out.String()), text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("preview did not contain %q: %q", text, out.String())
}

func TestOutputPreviewRendersPartialMarkdownAndMergesDeltas(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "dracula")
	var stdout lockedBuffer
	output := cli.NewOutput(&stdout, &bytes.Buffer{}, true)
	t.Cleanup(output.StreamDone)
	output.StreamEvent(provider.StreamEvent{Content: "# 标题\n\n**强调"})
	waitPreview(t, &stdout, "强调") // 未闭合的加粗仍可读。
	output.StreamEvent(provider.StreamEvent{Content: "**\n\n- 第一项\n\n```go\nfmt.Println(\"hello\")"})
	waitPreview(t, &stdout, "hello") // 未闭合代码块已经渲染。
	last := lastPreviewFrame(stdout.String())
	if strings.Contains(ansi.Strip(last), "**强调**") || strings.Contains(ansi.Strip(last), "```") || !strings.Contains(ansi.Strip(last), "•") {
		t.Fatalf("Markdown was not rendered before stream completion: %q", last)
	}
	before := strings.Count(stdout.String(), "\x1b[?2026h")
	output.StreamEvent(provider.StreamEvent{Content: "\n```\n\n"})
	for i := range 100 {
		output.StreamEvent(provider.StreamEvent{Content: fmt.Sprintf("%d ", i)})
	}
	waitPreview(t, &stdout, "99")
	if frames := strings.Count(stdout.String(), "\x1b[?2026h") - before; frames >= 100 {
		t.Fatalf("one frame per delta instead of merged refresh: %d", frames)
	}
	output.StreamDone()
	closed := stdout.String()
	time.Sleep(120 * time.Millisecond)
	if got := stdout.String(); got != closed {
		t.Fatalf("preview wrote after close: %q", got[len(closed):])
	}
	output.StreamEvent(provider.StreamEvent{Content: "新一轮回复"})
	waitPreview(t, &stdout, "新一轮回复")
	if frame := ansi.Strip(lastPreviewFrame(stdout.String())); strings.Contains(frame, "标题") || strings.Contains(frame, "hello") {
		t.Fatalf("previous model response leaked into new preview: %q", frame)
	}
	output.StreamDone()
}

func TestOutputPreviewFallsBackToTextWhenStyleFails(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", filepath.Join(t.TempDir(), "missing-style.json"))
	var stdout lockedBuffer
	output := cli.NewOutput(&stdout, &bytes.Buffer{}, true)
	t.Cleanup(output.StreamDone)
	output.StreamEvent(provider.StreamEvent{Content: "## 原文回退\n\n**仍可读**"})
	waitPreview(t, &stdout, "**仍可读**")
	output.StreamDone()
	if !strings.Contains(ansi.Strip(stdout.String()), "## 原文回退") {
		t.Fatalf("missing raw text after renderer failure: %q", stdout.String())
	}
}

func TestOutputPreviewConcurrentCloseRestoresTerminalOnce(t *testing.T) {
	var stdout lockedBuffer
	output := cli.NewOutput(&stdout, &bytes.Buffer{}, true)
	output.StreamEvent(provider.StreamEvent{Content: "未完成"})
	var closers sync.WaitGroup
	for range 8 {
		closers.Go(output.StreamDone)
	}
	closers.Wait()
	output.Message("主屏幕")
	got := stdout.String()
	if strings.Count(got, "\x1b[?1049l") != 1 || !strings.HasSuffix(got, "\x1b[?1049l主屏幕\n") {
		t.Fatalf("terminal was not restored once before final output: %q", got)
	}
}

func lastPreviewFrame(output string) string {
	start := strings.LastIndex(output, "\x1b[?2026h")
	if start < 0 {
		return ""
	}
	return output[start:]
}
