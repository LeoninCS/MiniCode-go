package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const maxLogBodyBytes = 512

// Config 是创建 Provider 所需的模型与连接配置。
type Config struct {
	// Provider 指定协议实现，为空时使用 OpenAI 兼容协议。
	Provider string
	// BaseURL 是模型服务的根地址,例如 https://api.openai.com/v1
	// 或 https://api.deepseek.com/v1。末尾斜杠会被自动去掉。
	BaseURL string
	// APIKey 用于在 Authorization 头中鉴权。
	APIKey string
	// Model 是默认使用的模型名,例如 "gpt-4o-mini"、"deepseek-chat"。
	Model string
	// HTTPClient 可选,允许调用方注入自定义 transport(例如测试或代理)。
	HTTPClient *http.Client
	// Tracer 可选，用于把完整模型输入、输出、token 和错误上报到 Langfuse。
	Tracer trace.Tracer
}

// Client 是对 OpenAI 兼容 /chat/completions 端点的轻量封装。
//
// ChatStream 当前返回 ErrStreamingUnsupported，SSE 解析在后续实现。
type Client struct {
	cfg  Config
	http *http.Client
}

var _ StreamingProvider = (*Client)(nil)

// NewClient 基于 Config 构造一个 Client。
// 缺省的 HTTPClient 会被设为 1 小时超时,
// 以保证在用户没有显式注入时仍能稳定取消请求。
func NewClient(cfg Config) *Client {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: time.Hour}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: cfg.HTTPClient}
}

// Tracer 返回模型调用使用的 tracer；未配置时返回 no-op tracer。
func (c *Client) Tracer() trace.Tracer {
	if c == nil || c.cfg.Tracer == nil {
		return trace.NewNoopTracerProvider().Tracer("minicode")
	}
	return c.cfg.Tracer
}

// DefaultModel 返回 Client 构造时配置的默认模型名。
func (c *Client) DefaultModel() string {
	return c.cfg.Model
}

// ChatStream 预留统一流式入口；当前实现尚不发送 SSE 请求。
func (c *Client) ChatStream(context.Context, ChatRequest) (Stream, error) {
	return nil, ErrStreamingUnsupported
}

// Chat 发送一次非流式对话请求并解析响应。
//
// 调用方通过 req.Model == "" 指定使用默认模型;
// 否则使用 req.Model 覆盖,便于 Day 10 起的多模型切换。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c == nil {
		return nil, errors.New("provider: nil client")
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return nil, errors.New("provider: empty API key")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("provider: empty messages")
	}
	model := req.Model
	if model == "" {
		model = c.cfg.Model
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("provider: empty model")
	}
	req.Model = model

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("provider: marshal request: %w", err)
	}

	ctx, span := startGenerationSpan(ctx, c.cfg.Tracer, model, body)
	defer span.End()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		recordSpanError(span, err)
		return nil, fmt.Errorf("provider: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		recordSpanError(span, err)
		return nil, fmt.Errorf("provider: http call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		recordSpanError(span, err)
		return nil, fmt.Errorf("provider: read response: %w", err)
	}
	span.SetAttributes(
		attribute.String("gen_ai.response.raw", string(raw)),
		attribute.String("langfuse.observation.output", string(raw)),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := parseErrorResponse(resp.StatusCode, raw)
		recordSpanError(span, err)
		return nil, err
	}

	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		recordSpanError(span, err)
		return nil, fmt.Errorf("provider: decode response: %w (body=%q)", err, truncateForLog(raw))
	}
	span.SetAttributes(
		attribute.String("gen_ai.response.id", out.ID),
		attribute.String("gen_ai.response.model", out.Model),
	)
	if out.Usage != nil {
		span.SetAttributes(
			attribute.Int("gen_ai.usage.input_tokens", out.Usage.PromptTokens),
			attribute.Int("gen_ai.usage.output_tokens", out.Usage.CompletionTokens),
			attribute.Int("gen_ai.usage.total_tokens", out.Usage.TotalTokens),
		)
	}
	return &out, nil
}

func startGenerationSpan(ctx context.Context, tracer trace.Tracer, model string, request []byte) (context.Context, trace.Span) {
	if tracer == nil {
		tracer = trace.NewNoopTracerProvider().Tracer("minicode/provider")
	}
	return tracer.Start(ctx, "chat "+model,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("langfuse.observation.type", "generation"),
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.provider.name", "openai-compatible"),
			attribute.String("gen_ai.request.model", model),
			attribute.String("gen_ai.request.raw", string(request)),
			attribute.String("langfuse.observation.input", string(request)),
		),
	)
}

func recordSpanError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// parseErrorResponse 尝试将非 2xx 响应解析为 APIError,
// 非 JSON 响应也转换为 APIError，保留状态码与截断后的响应文本。
//
// 同时兼容两种常见错误格式:
//   - OpenAI 风格:`{"error": {"code": ..., "message": ..., "type": ...}}`
//   - 平铺风格:`{"code": ..., "message": ..., "type": ...}`
func parseErrorResponse(status int, raw []byte) error {
	if apiErr := decodeAPIError(raw); apiErr != nil && apiErr.Message != "" {
		apiErr.StatusCode = status
		return apiErr
	}
	return &APIError{StatusCode: status, Message: fmt.Sprintf("http %d: %s", status, truncateForLog(raw))}
}

func decodeAPIError(raw []byte) *APIError {
	var direct APIError
	if err := json.Unmarshal(raw, &direct); err == nil && direct.Message != "" {
		return &direct
	}
	var wrapped struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Error.Message != "" {
		return &wrapped.Error
	}
	return nil
}

func truncateForLog(b []byte) string {
	if len(b) <= maxLogBodyBytes {
		return string(b)
	}
	return string(b[:maxLogBodyBytes]) + "...(truncated)"
}
