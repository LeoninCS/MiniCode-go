package agent

import (
	"io"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// Output 由调用方实现,负责展示 Agent 的运行过程。
// Write 接收工具的实时输出;模型文本与工具结果均保留原文。
type Output interface {
	io.Writer
	Message(content string)
	ToolCall(call provider.ToolCall)
	// ToolResult 在工具结束后交付完整结果,其内容已通过 Write 实时输出。
	ToolResult(result string)
	ToolError(err error)
}

// RunningOutput 是终端展示层可选实现的生命周期接口。
// BeginRunning 记录本轮临时输出的起点，ClearRunning 在最终回答前删除这些输出。
// 非交互宿主无需实现，Agent 会继续保留完整输出。
type RunningOutput interface {
	BeginRunning()
	ClearRunning()
}
