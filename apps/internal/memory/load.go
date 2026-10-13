package memory

import (
	"bytes"
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
	// 读取期间文件可能增长，不能只依赖 Stat 的大小。
	if len(content) > MaxFileBytes {
		return nil, fmt.Errorf("memory: %s exceeds %d byte limit", FileName, MaxFileBytes)
	}
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("memory: %s must contain valid UTF-8 text", FileName)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, fmt.Errorf("memory: %s must not contain NUL bytes", FileName)
	}
	doc := &Document{Content: string(content)}
	doc.EstimatedTokens = token.EstimateText(doc.Content)
	if doc.EstimatedTokens > tokenBudget {
		return nil, fmt.Errorf("memory: %s requires approximately %d tokens, exceeds %d token budget", FileName, doc.EstimatedTokens, tokenBudget)
	}
	return doc, nil
}
