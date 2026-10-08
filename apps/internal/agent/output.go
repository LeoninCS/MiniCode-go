package agent

import (
	"time"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// ToolStatus 表示一次工具调用在过程展示中的终态。
type ToolStatus string

const (
	ToolCompleted ToolStatus = "completed"
	ToolFailed    ToolStatus = "failed"
	ToolDenied    ToolStatus = "denied"
	ToolCanceled  ToolStatus = "canceled"
)

// Output 由调用方实现，展示最终回复、运行过程和无法继续处理的任务错误。
// 过程事件只包含轮次、工具和状态，不暴露工具结果或模型中间文本。
type Output interface {
	Message(content string)
	ModelStart(turn int)
	ToolStart(turn int, call provider.ToolCall)
	ToolDone(name string, status ToolStatus, duration time.Duration)
	ToolError(err error)
}
