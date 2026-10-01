// Package cli 提供 MiniCode 命令行界面的输出与终端展示能力。
package cli

import (
	"io"
	"os"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/term"
)

const (
	envGlamourStyle      = "GLAMOUR_STYLE"
	defaultMarkdownStyle = "dracula"
	defaultMarkdownWidth = 80
)

// RenderMarkdown 为终端渲染模型回复;非终端或渲染失败时保留原文。
func RenderMarkdown(stdout io.Writer, content string) string {
	plain := content + "\n"
	if !IsTerminal(stdout) {
		return plain
	}

	file := stdout.(*os.File)
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		width = defaultMarkdownWidth
	}
	style := os.Getenv(envGlamourStyle)
	if style == "" {
		style = defaultMarkdownStyle
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return plain
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return plain
	}
	return rendered
}

// IsTerminal 判断输入或输出是否连接到终端。
func IsTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
