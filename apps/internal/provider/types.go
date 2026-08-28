// Package provider 定义模型提供方共用的协议结构体。
//
// Day 1 的范围仅覆盖单次非流式文本请求:消息、请求、响应、错误。
// 工具调用、Agent Loop、流式和 Provider 抽象层留到后续 Day 推进。
package provider

// Role 表示消息在一段对话中的角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是协议中的一条对话消息。
//
// Content 故意保持为 string,符合 Day 1 的最小需求:
// 文本进出。Day 2 起会让 Content 也支持多模态/工具调用
// 时,再扩展为多段结构体或指针类型。
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 是发往模型服务的一次非流式对话请求。
//
// 仅保留 Day 1 必须的字段;temperature、top_p、tools 等
// 字段在 Day 2～Day 4 引入工具后再补齐。
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// ChatResponse 是模型服务对一次非流式请求的完整回复。
type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice 是 ChatResponse 中的一条候选回复。
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage 记录本次请求的 token 消耗。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// FirstContent 提取第一条候选回复的文本内容。
//
// 约定 Day 1 的非流式响应里只关心第一条 choice 的文本。
// 后续 Day 11 引入流式后,本方法仍可作为最终聚合结果的读取入口。
func (r *ChatResponse) FirstContent() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// APIError 表示模型服务返回的业务错误,例如鉴权失败、参数非法、配额耗尽。
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
	Type       string `json:"type,omitempty"`
	Param      string `json:"param,omitempty"`
}

// Error 实现 error 接口,便于上层用 errors.Is/As 区分错误来源。
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return "provider api error: " + e.Code + ": " + e.Message
	}
	return "provider api error: " + e.Message
}
