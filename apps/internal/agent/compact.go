package agent

import (
	"context"
	"fmt"

	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
)

// compactIfNeeded 只在完整工具结果之后、下一次模型请求之前检查预算。
func (s *Session) compactIfNeeded(req provider.ChatRequest, output Output) {
	if !s.contextBudget.ShouldCompact(req) {
		return
	}
	s.messages = compaction.Compact(s.messages)
	output.Message(fmt.Sprintf("上下文剩余不足 %d token，已自动触发压缩；当前压缩策略尚未实现，历史保持不变。", compaction.ReservedTokens))
}

// chatWithContextRecovery 对上下文超限触发压缩，并只重试当前模型请求一次。
// 已完成的工具结果留在历史中，恢复不会重新执行工具或重复追加用户输入。
func (s *Session) chatWithContextRecovery(ctx context.Context, req provider.ChatRequest, output Output, stats *TaskStats) (*provider.ChatResponse, error) {
	resp, err := s.chat(ctx, req, output)
	if !provider.IsContextOverflow(err) {
		return resp, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// 总结请求带有额外系统指令；压缩历史后仍须保留，且不能写入会话历史。
	extra := append([]provider.Message(nil), req.Messages[len(s.messages):]...)
	s.messages = compaction.Compact(s.messages)
	req.Messages = append(s.messages, extra...)
	output.Message("模型提示上下文超限，已触发压缩，将重试当前模型请求一次（当前压缩策略尚未实现）。")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	stats.ModelCalls++
	resp, err = s.chat(ctx, req, output)
	if err != nil {
		return nil, fmt.Errorf("context overflow recovery: %w", err)
	}
	return resp, nil
}
