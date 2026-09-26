// Package provider 定义模型提供方共用的协议结构体。
//
// 当前覆盖单次非流式文本请求和 OpenAI 兼容的函数工具调用协议。
// Agent Loop、工具执行、流式和 Provider 抽象层留到后续 Day 推进。
package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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
// assistant 消息通过 ToolCalls 携带模型请求的工具调用;
// tool 消息通过 ToolCallID 关联调用并用 Content 回传执行结果。
type Message struct {
	Role       Role       `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// NewMessage 构造一条带文本内容的消息。
// 普通消息的 toolCallID 传空字符串;工具结果使用 RoleTool 并传入对应的调用 ID。
func NewMessage(role Role, content, toolCallID string) Message {
	return Message{
		Role:       role,
		Content:    &content,
		ToolCallID: toolCallID,
	}
}

// Text 返回消息的文本内容;content 为 null 时返回空字符串。
func (m Message) Text() string {
	if m.Content == nil {
		return ""
	}
	return *m.Content
}

// ChatRequest 是发往模型服务的一次非流式对话请求。
// 使用接口默认的单条回复,不设置多候选参数。
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
}

// ToolType 表示模型可调用的工具类型。
type ToolType string

const (
	// ToolTypeFunction 是 Chat Completions 当前使用的函数工具类型。
	ToolTypeFunction ToolType = "function"
)

// JSONSchema 是工具参数使用的 JSON Schema 对象。
//
// JSON Schema 本身可扩展,因此使用 map 保留完整表达能力,
// 避免 Provider 层只支持少量关键字。
type JSONSchema map[string]any

// Tool 是随 ChatRequest 发送给模型的一项工具声明。
type Tool struct {
	Type     ToolType           `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition 描述函数工具的名称、用途和参数结构。
type FunctionDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  JSONSchema `json:"parameters,omitempty"`
	Strict      *bool      `json:"strict,omitempty"`
}

// ToolCall 是模型返回的一次结构化工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     ToolType     `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 保存模型选择的工具名称和 JSON 字符串参数。
// Arguments 必须先校验和解码,不能直接用于执行工具。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Validate 校验工具调用的协议字段和参数 JSON。
// 具体工具仍需在执行前按自己的 schema 校验字段和值。
func (c ToolCall) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("provider: tool call has empty ID")
	}
	if c.Type != ToolTypeFunction {
		return fmt.Errorf("provider: tool call %q has unsupported type %q", c.ID, c.Type)
	}
	if strings.TrimSpace(c.Function.Name) == "" {
		return fmt.Errorf("provider: tool call %q has empty function name", c.ID)
	}
	var arguments map[string]json.RawMessage
	if err := c.DecodeArguments(&arguments); err != nil {
		return fmt.Errorf("provider: validate tool call %q: %w", c.ID, err)
	}
	return nil
}

// DecodeArguments 校验 Arguments 是一个 JSON 对象,再解码到 dst。
// dst 通常是工具自己的参数结构体或 map。
func (c ToolCall) DecodeArguments(dst any) error {
	if dst == nil {
		return errors.New("provider: nil tool arguments destination")
	}
	raw := strings.TrimSpace(c.Function.Arguments)
	if raw == "" {
		return fmt.Errorf("provider: tool %q has empty arguments", c.Function.Name)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return fmt.Errorf("provider: tool %q arguments must be a JSON object: %w", c.Function.Name, err)
	}
	if object == nil {
		return fmt.Errorf("provider: tool %q arguments must be a JSON object", c.Function.Name)
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("provider: decode tool %q arguments: %w", c.Function.Name, err)
	}
	return nil
}

// ChatResponse 是模型服务对一次非流式请求的完整回复。
// Choices 保留接口要求的数组结构,业务统一读取 Choices[0]。
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

// Content 提取回复的文本内容。
//
// content 为 null 的工具调用回复会返回空字符串;
// 调用方应先通过 ToolCalls 判断是否存在工具调用。
// 后续 Day 11 引入流式后,本方法仍可作为最终聚合结果的读取入口。
func (r *ChatResponse) Content() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Text()
}

// ToolCalls 提取并校验回复中的全部工具调用。
// 返回空切片表示该回复是普通文本或没有请求工具。
func (r *ChatResponse) ToolCalls() ([]ToolCall, error) {
	if r == nil || len(r.Choices) == 0 {
		return nil, nil
	}
	choice := r.Choices[0]
	calls := choice.Message.ToolCalls
	if len(calls) > 0 && (choice.FinishReason == "length" || choice.FinishReason == "content_filter") {
		return nil, fmt.Errorf("provider: tool calls returned with finish reason %q", choice.FinishReason)
	}
	seenIDs := make(map[string]struct{}, len(calls))
	for i := range calls {
		if err := calls[i].Validate(); err != nil {
			return nil, fmt.Errorf("provider: invalid tool call at index %d: %w", i, err)
		}
		if _, exists := seenIDs[calls[i].ID]; exists {
			return nil, fmt.Errorf("provider: duplicate tool call ID %q", calls[i].ID)
		}
		seenIDs[calls[i].ID] = struct{}{}
	}
	return calls, nil
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
