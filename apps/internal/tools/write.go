package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// WriteFile 创建或完整覆盖 UTF-8 文本文件,自动创建父目录。
// 文件通过同目录临时文件原子替换;已有文件保留权限,新文件使用 0644。
// 写入成功后更新内容版本,允许直接继续编辑。
func (f *FileTools) WriteFile(ctx context.Context, path, content string) (string, error) {
	path, err := cleanFilePath(path)
	if err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	data := []byte(content)
	if err := validateText(data); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	if err := f.root.MkdirAll(filepath.Dir(path), dirPermissions); err != nil {
		return "", fmt.Errorf("write: create parent directories: %w", err)
	}
	if err := fsWriteFile(ctx, f.root, path, data, nil); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	f.readVersions[path] = sha256.Sum256(data)
	return fmt.Sprintf("Wrote %d bytes to %s", len(data), path), nil
}

// executeWriteTool 校验写入参数并创建或覆盖文件。
func executeWriteTool(ctx context.Context, call provider.ToolCall, files *FileTools) (string, error) {
	var arguments struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeToolArguments(call, &arguments, []string{"path", "content"}); err != nil {
		return "", err
	}
	return files.WriteFile(ctx, arguments.Path, arguments.Content)
}

// buildWriteTool 构造发送给模型的 write 工具声明。
func buildWriteTool() provider.Tool {
	return provider.Tool{
		Type: provider.ToolTypeFunction,
		Function: provider.FunctionDefinition{
			Name:        toolNameWrite,
			Description: "Create or completely overwrite a UTF-8 text file in the workspace. Parent directories are created automatically.",
			Parameters: provider.JSONSchema{
				"type": "object",
				"properties": map[string]provider.JSONSchema{
					"path":    {"type": "string", "description": "File path relative to the workspace."},
					"content": {"type": "string", "description": "Complete file content. An empty string creates or overwrites an empty file."},
				},
				"required":             []string{"path", "content"},
				"additionalProperties": false,
			},
		},
	}
}
