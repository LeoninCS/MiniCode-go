package compaction

import (
	"encoding/json"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/token"
)

const (
	// ContextWindow 是固定的上下文窗口大小，单位为 token。
	ContextWindow = 256 * 1024
	// ReservedTokens 是自动压缩触发前需要保留的上下文空间。
	ReservedTokens = 16 * 1024
)

// TokenTrigger 根据最近一次模型用量和新增内容判断是否需要压缩。
// 本地估算仅用于触发判断，不计入任务统计。
type TokenTrigger struct {
	reportedTokens  int
	estimatedTokens int
}

// ShouldCompact 在剩余空间严格小于 ReservedTokens 时返回 true。
func (t *TokenTrigger) ShouldCompact(req provider.ChatRequest) bool {
	used := estimateRequest(req)
	if t.reportedTokens > 0 {
		// 最近一次请求已包含的历史和工具声明不重复累计；只补充此后的变化。
		used += t.reportedTokens - t.estimatedTokens
	}
	return used > ContextWindow-ReservedTokens
}

// Record 使用完整回复的用量校准当前历史占用，不累计不同请求的总消耗。
func (t *TokenTrigger) Record(req provider.ChatRequest, resp *provider.ChatResponse) {
	if resp == nil || resp.Usage == nil || len(resp.Choices) == 0 {
		return
	}
	usage := resp.Usage
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 {
		return
	}
	used := usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens > used {
		used = usage.TotalTokens
	}
	if used == 0 {
		return
	}
	t.reportedTokens = used
	t.estimatedTokens = estimateRequest(req) + estimateMessage(resp.Choices[0].Message)
}

// estimateRequest 粗估消息、工具调用、工具结果和工具声明的 token 数。
// 各厂商的分词方式不同；没有服务用量时，此值只用于提前触发压缩。
func estimateRequest(req provider.ChatRequest) int {
	tokens := 3
	for _, message := range req.Messages {
		tokens += estimateMessage(message)
	}
	if len(req.Tools) > 0 {
		data, _ := json.Marshal(req.Tools)
		tokens += token.EstimateText(string(data))
	}
	return tokens
}

func estimateMessage(message provider.Message) int {
	tokens := 4 + token.EstimateText(string(message.Role)) + token.EstimateText(message.Text()) + token.EstimateText(message.ToolCallID)
	for _, call := range message.ToolCalls {
		tokens += 4 + token.EstimateText(call.ID) + token.EstimateText(string(call.Type)) + token.EstimateText(call.Function.Name) + token.EstimateText(call.Function.Arguments)
	}
	return tokens
}
