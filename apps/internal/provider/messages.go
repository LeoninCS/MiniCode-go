package provider

import (
	"fmt"
	"sort"
)

// ValidateMessages 校验完整消息序列的角色字段、工具参数及调用与结果配对。
// 工具结果按 ID 配对，顺序不限；一个批次完成前不能插入其他角色的消息。
// 允许连续 user 消息和批次之外的 system 消息；空历史由请求入口另行检查。
func ValidateMessages(messages []Message) error {
	pending := make(map[string]struct{})
	assistantIndex := -1
	for index, message := range messages {
		if len(pending) > 0 {
			if message.Role != RoleTool {
				return fmt.Errorf("provider: message %d: %w", index, missingToolResults(assistantIndex, pending))
			}
			if _, ok := pending[message.ToolCallID]; !ok {
				return fmt.Errorf("provider: message %d: unexpected or duplicate tool result ID %q", index, message.ToolCallID)
			}
			if len(message.ToolCalls) != 0 || message.Content == nil {
				return fmt.Errorf("provider: message %d: invalid tool result for ID %q", index, message.ToolCallID)
			}
			delete(pending, message.ToolCallID)
			continue
		}

		switch message.Role {
		case RoleSystem, RoleUser:
			if message.Content == nil || len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				return fmt.Errorf("provider: message %d: invalid %s message", index, message.Role)
			}
		case RoleAssistant:
			if message.ToolCallID != "" || (message.Content == nil && len(message.ToolCalls) == 0) {
				return fmt.Errorf("provider: message %d: invalid assistant message", index)
			}
			assistantIndex = index
			for callIndex, call := range message.ToolCalls {
				if err := call.Validate(); err != nil {
					return fmt.Errorf("provider: message %d tool call %d: %w", index, callIndex, err)
				}
				if _, exists := pending[call.ID]; exists {
					return fmt.Errorf("provider: message %d: duplicate tool call ID %q", index, call.ID)
				}
				pending[call.ID] = struct{}{}
			}
		case RoleTool:
			return fmt.Errorf("provider: message %d: tool result ID %q has no preceding call", index, message.ToolCallID)
		default:
			return fmt.Errorf("provider: message %d: unsupported role %q", index, message.Role)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("provider: %w", missingToolResults(assistantIndex, pending))
	}
	return nil
}

func missingToolResults(assistantIndex int, pending map[string]struct{}) error {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return fmt.Errorf("assistant message %d tool calls are missing results for IDs %q", assistantIndex, ids)
}
