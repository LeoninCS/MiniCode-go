package tools

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ReadFile 按行读取 UTF-8 文本并添加行号,offset 从 1 开始。
// offset 和 limit 为 0 时分别使用 1 和 200;正文最多返回 64 KiB,超出时标记截断。
func (f *FileTools) ReadFile(ctx context.Context, path string, offset, limit int) (string, error) {
	path, err := cleanFilePath(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	if offset < 0 || limit < 0 {
		return "", errors.New("read_file: offset and limit must not be negative")
	}
	if offset == 0 {
		offset = 1
	}
	if limit == 0 {
		limit = defaultReadLines
	}
	content, err := fsReadFile(ctx, f.root, path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	if len(content) == 0 {
		if offset != 1 {
			return "", errors.New("read_file: offset is beyond end of file")
		}
		f.readVersions[path] = sha256.Sum256(content)
		return "[empty file]", nil
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if offset > len(lines) {
		return "", fmt.Errorf("read_file: offset %d is beyond end of file (%d lines)", offset, len(lines))
	}
	end := offset - 1 + min(limit, len(lines)-(offset-1))
	var output strings.Builder
	truncated := end < len(lines)
	for i := offset - 1; i < end; i++ {
		line := fmt.Sprintf("%d: %s\n", i+1, lines[i])
		remaining := maxOutputBytes - output.Len()
		if len(line) > remaining {
			line = line[:remaining]
			for !utf8.ValidString(line) {
				line = line[:len(line)-1]
			}
			output.WriteString(line)
			truncated = true
			break
		}
		output.WriteString(line)
	}
	if truncated {
		output.WriteString(outputTruncationMarker)
	}
	f.readVersions[path] = sha256.Sum256(content)
	return output.String(), nil
}
