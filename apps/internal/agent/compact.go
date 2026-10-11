package agent

import (
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
