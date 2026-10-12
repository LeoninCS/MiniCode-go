package compaction

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// State 是模型上下文的压缩检查点，原始消息仍由 Session 完整保存。
// FirstKeptMessageIndex 从不含系统 Prompt 的原始历史起计数。
type State struct {
	Summary               string `json:"summary"`
	FirstKeptMessageIndex int    `json:"first_kept_message_index"`
}

// Validate 校验检查点及保留边界，不能让保留的工具结果失去对应调用。
// nil 表示尚未压缩。
func (s *State) Validate(history []provider.Message) error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(s.Summary) == "" {
		return errors.New("compaction: empty checkpoint summary")
	}
	if s.FirstKeptMessageIndex < 0 || s.FirstKeptMessageIndex > len(history) {
		return fmt.Errorf("compaction: first kept message index %d is outside history of %d messages", s.FirstKeptMessageIndex, len(history))
	}
	if s.FirstKeptMessageIndex < len(history) && history[s.FirstKeptMessageIndex].Role == provider.RoleTool {
		return errors.New("compaction: kept boundary splits a tool call and its results")
	}
	return nil
}

// ContextMessages 构造模型要读取的消息副本，不包含系统 Prompt。
// 调用前须 Validate；nil 检查点返回全部原始历史的副本。
func (s *State) ContextMessages(history []provider.Message) []provider.Message {
	if s == nil {
		return append([]provider.Message(nil), history...)
	}
	messages := make([]provider.Message, 0, 1+len(history)-s.FirstKeptMessageIndex)
	messages = append(messages, provider.NewMessage(provider.RoleUser, summaryPrefix+s.Summary+summarySuffix, ""))
	return append(messages, history[s.FirstKeptMessageIndex:]...)
}

// LegacySummary 识别 v1 存档中作为 user 消息保存的旧摘要，仅用于格式迁移。
func LegacySummary(message provider.Message) (string, bool) {
	if message.Role != provider.RoleUser || message.ToolCallID != "" || len(message.ToolCalls) != 0 || !strings.HasPrefix(message.Text(), summaryPrefix) || !strings.HasSuffix(message.Text(), summarySuffix) {
		return "", false
	}
	summary := strings.TrimSuffix(strings.TrimPrefix(message.Text(), summaryPrefix), summarySuffix)
	return summary, strings.TrimSpace(summary) != ""
}
