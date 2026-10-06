package agent

import (
	"context"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// ToolApprover 在有副作用的工具执行前向宿主请求授权。
// content 是模型在本次工具调用中附带的说明，宿主可在审批时一并展示。
// 返回 false 表示用户拒绝；拒绝不是运行错误，Agent 会把结果回传模型。
type ToolApprover interface {
	Approve(ctx context.Context, content string, call provider.ToolCall) (bool, error)
}

// ToolApproverFunc 让函数可以直接作为 ToolApprover 使用。
type ToolApproverFunc func(ctx context.Context, content string, call provider.ToolCall) (bool, error)

func (f ToolApproverFunc) Approve(ctx context.Context, content string, call provider.ToolCall) (bool, error) {
	return f(ctx, content, call)
}

func requiresApproval(name string) bool {
	switch name {
	case "bash", "write", "edit":
		return true
	default:
		return false
	}
}
