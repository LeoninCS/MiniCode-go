// Package cli 提供 MiniCode 命令行界面的输出与终端展示能力。
package cli

import (
	"io"
	"os"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/term"
)

const (
	envGlamourStyle       = "GLAMOUR_STYLE"
	defaultMarkdownStyle  = "dracula"
	defaultMarkdownWidth  = 80
	defaultTerminalHeight = 24
)

// RenderMarkdown 为终端渲染模型回复;非终端或渲染失败时保留原文。
func RenderMarkdown(stdout io.Writer, content string) string {
	plain := content + "\n"
	if !IsTerminal(stdout) {
		return plain
	}

	width, _ := outputSize(stdout)
	renderer, err := newMarkdownRenderer(width)
	if err != nil {
		return plain
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return plain
	}
	return rendered
}

func newMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	style := os.Getenv(envGlamourStyle)
	if style == "" {
		style = defaultMarkdownStyle
	}
	return glamour.NewTermRenderer(
		glamour.WithStylePath(style),
		glamour.WithWordWrap(width),
	)
}

func outputSize(stdout io.Writer) (width, height int) {
	if IsTerminal(stdout) {
		width, height, _ = term.GetSize(stdout.(*os.File).Fd())
	}
	if width <= 0 {
		width = defaultMarkdownWidth
	}
	if height <= 0 {
		height = defaultTerminalHeight
	}
	return width, height
}

// IsTerminal 判断输入或输出是否连接到终端。
func IsTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
