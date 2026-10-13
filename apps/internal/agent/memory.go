package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/MiniCode-go/minicode/internal/memory"
	"github.com/MiniCode-go/minicode/internal/provider"
)

const (
	projectMemoryPrefix = "以下是当前工作区 MiniCode.md 中的项目记忆，作为项目背景和长期约定使用。若与系统规则或用户本次明确要求冲突，以系统规则和用户本次要求为准。\n<project-memory>\n"
	projectMemorySuffix = "\n</project-memory>"
)

// reloadProjectMemory 在会话创建和用户轮次开始时重新读取磁盘文件。
// 缺失、清空或读取失败都清除旧内容；错误由调用方返回，阻止继续使用过时记忆。
func (s *Session) reloadProjectMemory(ctx context.Context) error {
	s.projectMemory = ""
	doc, err := memory.Load(ctx, s.workspace, memory.DefaultTokenBudget)
	if err != nil {
		return fmt.Errorf("agent: load project memory: %w", err)
	}
	if doc != nil {
		s.projectMemory = doc.Content
	}
	return nil
}

// isProjectMemoryMutation 仅在成功的文件变更后判断是否需要刷新根目录记忆。
func isProjectMemoryMutation(call provider.ToolCall) bool {
	if call.Function.Name != "write" && call.Function.Name != "edit" {
		return false
	}
	var arguments struct {
		Path string `json:"path"`
	}
	return json.Unmarshal([]byte(call.Function.Arguments), &arguments) == nil && filepath.Clean(arguments.Path) == memory.FileName
}
