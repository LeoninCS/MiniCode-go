package tools

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// EditFile 精确替换旧文本,新文本可以为空。
// replaceAll 为 false 时要求唯一匹配,为 true 时从左到右替换所有不重叠的匹配。
// 同一实例中须已读取或成功写入该路径,且文件内容此后未被外部修改。
// 匹配前统一换行为 LF,写回时使用原文件首次出现的 LF/CRLF 格式。
func (f *FileTools) EditFile(ctx context.Context, path, oldText, newText string, replaceAll bool) (string, error) {
	path, err := cleanFilePath(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	if oldText == "" {
		return "", errors.New("edit_file: old_text must not be empty")
	}
	if err := validateText([]byte(newText)); err != nil {
		return "", fmt.Errorf("edit_file: new_text: %w", err)
	}
	version, ok := f.readVersions[path]
	if !ok {
		return "", errors.New("edit_file: call read_file on this path before editing")
	}
	content, err := fsReadFile(ctx, f.root, path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	if sha256.Sum256(content) != version {
		return "", errors.New("edit_file: file changed since last read or write; read_file again before editing")
	}
	lineEnding := detectLineEnding(string(content))
	text := normalizeToLF(string(content))
	oldText = normalizeToLF(oldText)
	newText = normalizeToLF(newText)
	index := strings.Index(text, oldText)
	if index < 0 {
		return "", errors.New("edit_file: old_text was not found")
	}
	// 单处模式从首个匹配的下一个字节继续查找,也拒绝相互重叠的重复匹配。
	if !replaceAll && strings.Contains(text[index+1:], oldText) {
		return "", errors.New("edit_file: old_text must occur exactly once; include surrounding code in old_text to make the match unique, then retry")
	}
	count := 1
	if replaceAll {
		count = strings.Count(text, oldText)
	}
	// 先检查增长量,避免大量匹配生成过大的字符串;恢复换行后仍校验实际字节数。
	if growth := len(newText) - len(oldText); growth > 0 && count > (maxFileBytes-len(text))/growth {
		return "", fmt.Errorf("edit_file: content exceeds %d byte limit", maxFileBytes)
	}
	updatedText := strings.Replace(text, oldText, newText, count)
	if lineEnding == "\r\n" {
		updatedText = strings.ReplaceAll(updatedText, "\n", "\r\n")
	}
	updated := []byte(updatedText)
	if err := validateText(updated); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	if err := fsWriteFile(ctx, f.root, path, updated, &version); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	f.readVersions[path] = sha256.Sum256(updated)
	if count == 1 {
		return fmt.Sprintf("Replaced one occurrence in %s", path), nil
	}
	return fmt.Sprintf("Replaced %d occurrences in %s", count, path), nil
}

// detectLineEnding 按首个 LF/CRLF 判断换行格式,未找到时默认使用 LF。
func detectLineEnding(text string) string {
	index := strings.IndexByte(text, '\n')
	if index > 0 && text[index-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// normalizeToLF 将 CRLF 和单独的 CR 统一为 LF。
func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}
