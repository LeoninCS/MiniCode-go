package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	// OpenAI 表示 OpenAI 兼容的 Chat Completions 协议。
	OpenAI = "openai"
)

// ErrStreamingUnsupported 表示当前实现尚不支持流式请求。
var ErrStreamingUnsupported = errors.New("provider: streaming is not supported")

// Provider 以统一的请求、回复和 APIError 隔离各模型服务的协议差异。
// 实现必须遵守 context 取消；工具执行与消息历史由 Agent 管理。
// 服务返回的错误转换为 APIError，网络与取消错误保留原有 error 链。
type Provider interface {
	Chat(context.Context, ChatRequest) (*ChatResponse, error)
}

// StreamingProvider 是可选的流式能力，非流式实现只需满足 Provider。
type StreamingProvider interface {
	Provider
	ChatStream(context.Context, ChatRequest) (Stream, error)
}

// Stream 顺序交付增量事件；Recv 在流结束时返回 io.EOF。
// 创建请求时的 context 取消后，Recv 必须退出；调用方负责 Close 释放资源。
type Stream interface {
	Recv() (StreamEvent, error)
	Close() error
}

// StreamEvent 是各服务共用的增量回复，Usage 可在最后一个事件中单独出现。
type StreamEvent struct {
	Content      string
	ToolCalls    []ToolCallDelta
	FinishReason string
	Usage        *Usage
}

// ToolCallDelta 按 Index 关联同一次回复中的工具调用。
// Function.Name 和 Arguments 是增量片段，完整累积前不能作为 ToolCall 执行。
type ToolCallDelta struct {
	Index    int
	ID       string
	Type     ToolType
	Function FunctionCall
}

// New 按协议配置创建 Provider。省略 Provider 时使用 OpenAI 兼容实现。
// 新协议在这里选择实现，Agent 无需依赖具体的客户端类型。
func New(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "", OpenAI:
	default:
		return nil, fmt.Errorf("provider: unsupported provider %q", cfg.Provider)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("provider: empty API key")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("provider: empty model")
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Hostname() == "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("provider: base URL must be an absolute HTTP or HTTPS URL")
	}
	return NewClient(cfg), nil
}
