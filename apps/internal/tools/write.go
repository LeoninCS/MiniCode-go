package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

// WriteFile 创建或完整覆盖 UTF-8 文本文件,自动创建父目录。
// 文件通过同目录临时文件原子替换;已有文件保留权限,新文件使用 0644。
// 写入成功后更新内容版本,允许直接继续编辑。
func (f *FileTools) WriteFile(ctx context.Context, path, content string) (string, error) {
	path, err := cleanFilePath(path)
	if err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	data := []byte(content)
	if err := validateText(data); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	if err := f.root.MkdirAll(filepath.Dir(path), dirPermissions); err != nil {
		return "", fmt.Errorf("write_file: create parent directories: %w", err)
	}
	if err := fsWriteFile(ctx, f.root, path, data, nil); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	f.readVersions[path] = sha256.Sum256(data)
	return fmt.Sprintf("Wrote %d bytes to %s", len(data), path), nil
}
