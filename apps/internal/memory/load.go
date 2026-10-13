package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/MiniCode-go/minicode/internal/token"
)

const (
	// FileName 是工作区根目录的项目记忆文件名。
	FileName = "MiniCode.md"
	// MaxFileBytes 限制一次读取的记忆文件大小。
	MaxFileBytes = 64 * 1024
	// DefaultTokenBudget 是项目记忆原文的默认注入预算。
	DefaultTokenBudget = 4 * 1024
)

// Document 保留记忆原文及其本地估算的 token 数。
type Document struct {
	Content         string
	EstimatedTokens int
}

// Load 读取指定工作区根目录的 MiniCode.md，不查找父目录或创建文件。
// 文件不存在时返回 nil, nil；已有空文件返回空 Document。
// tokenBudget 只限制原文，调用方需另计消息包装开销；预算为零时仅接受空内容。
// UTF-8、文件大小或预算检查失败时返回错误，不返回截断或部分内容。
func Load(ctx context.Context, workspace string, tokenBudget int) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("memory: workspace must not be empty")
	}
	if tokenBudget < 0 {
		return nil, errors.New("memory: token budget must not be negative")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, fmt.Errorf("memory: open workspace: %w", err)
	}
	defer func() { _ = root.Close() }()

	// 非阻塞打开并检查实际句柄，避免 FIFO 阻塞，也限制符号链接在工作区内。
	file, err := root.OpenFile(FileName, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 断开的符号链接属于读取错误，只有文件名确实不存在才视为无记忆。
			if _, statErr := root.Lstat(FileName); errors.Is(statErr, os.ErrNotExist) {
				if err := ctx.Err(); err != nil {
					return nil, fmt.Errorf("memory: %w", err)
				}
				return nil, nil
			}
		}
		return nil, fmt.Errorf("memory: open %s: %w", FileName, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("memory: stat %s: %w", FileName, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("memory: %s must be a regular file", FileName)
	}
	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("memory: %s exceeds %d byte limit", FileName, MaxFileBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("memory: read %s: %w", FileName, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	// 读取期间文件可能增长，由内容校验再次检查实际大小。
	return NewDocument(string(content), tokenBudget)
}

// NewDocument 校验完整记忆原文，供读取和写入前共用；超限时不截断。
// tokenBudget 只覆盖原文，不包含消息包装开销。
func NewDocument(content string, tokenBudget int) (*Document, error) {
	if tokenBudget < 0 {
		return nil, errors.New("memory: token budget must not be negative")
	}
	if err := validateContent(FileName, content); err != nil {
		return nil, err
	}
	estimated := token.EstimateText(content)
	if estimated > tokenBudget {
		return nil, fmt.Errorf("memory: %s requires approximately %d tokens, exceeds %d token budget", FileName, estimated, tokenBudget)
	}
	return &Document{Content: content, EstimatedTokens: estimated}, nil
}

// validateContent 校验完整记忆文件，常驻正文与按需详情共用文本和大小限制。
func validateContent(name, content string) error {
	if len(content) > MaxFileBytes {
		return fmt.Errorf("memory: %s exceeds %d byte limit", name, MaxFileBytes)
	}
	if !utf8.ValidString(content) {
		return fmt.Errorf("memory: %s must contain valid UTF-8 text", name)
	}
	if strings.IndexByte(content, 0) >= 0 {
		return fmt.Errorf("memory: %s must not contain NUL bytes", name)
	}
	return nil
}
