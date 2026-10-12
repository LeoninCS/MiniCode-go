package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
)

// compactIfNeeded 只在完整工具结果之后、下一次模型请求之前检查预算。
func (s *Session) compactIfNeeded(ctx context.Context, req provider.ChatRequest, output Output, stats *TaskStats) error {
	if !s.contextBudget.ShouldCompact(req) {
		return nil
	}
	output.Message(fmt.Sprintf("上下文剩余不足 %d token，已自动触发压缩。", compaction.ReservedTokens))
	_, err := s.compact(ctx, stats)
	return err
}

// compact 是三个入口共用的调用适配，只提交完整成功的摘要。
func (s *Session) compact(ctx context.Context, stats *TaskStats) (bool, error) {
	result, err := compaction.Compact(ctx, s.client, s.messages[1:], s.compaction)
	stats.ModelCalls += result.ModelCalls
	stats.InputTokens += result.Usage.PromptTokens
	stats.OutputTokens += result.Usage.CompletionTokens
	stats.TotalTokens += result.Usage.TotalTokens
	stats.UsageResponses += result.UsageResponses
	if err != nil {
		return false, err
	}
	if result.Changed {
		s.compaction = result.State
		// 新历史与上次模型用量不再对应，直到主对话下一次回复再重新校准。
		s.contextBudget = compaction.TokenTrigger{}
	}
	return result.Changed, nil
}

// contextMessages 临时构造模型上下文，完整原文继续留在 s.messages 和会话存档中。
func (s *Session) contextMessages() []provider.Message {
	body := s.compaction.ContextMessages(s.messages[1:])
	messages := make([]provider.Message, 0, 1+len(body))
	messages = append(messages, s.messages[0])
	return append(messages, body...)
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
	extra := append([]provider.Message(nil), req.Messages[len(s.contextMessages()):]...)
	if _, compactErr := s.compact(ctx, stats); compactErr != nil {
		return nil, fmt.Errorf("context overflow recovery: %w", errors.Join(err, compactErr))
	}
	req.Messages = append(s.contextMessages(), extra...)
	output.Message("模型提示上下文超限，已触发压缩，将重试当前模型请求一次。")
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
