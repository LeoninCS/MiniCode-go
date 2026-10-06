package agent

// Output 由调用方实现，只展示最终回复和无法继续处理的任务错误。
// 模型中间文本、工具调用及工具结果仍保留在消息历史中，但不会交付给展示层。
type Output interface {
	Message(content string)
	ToolError(err error)
}
