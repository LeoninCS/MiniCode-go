// Package compaction 提供上下文压缩能力。
package compaction

import "github.com/MiniCode-go/minicode/internal/provider"

// Compact 是上下文压缩入口；当前未实现压缩策略，原样返回消息。
func Compact(messages []provider.Message) []provider.Message {
	// TODO: 实现上下文压缩策略。
	return messages
}
