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
)

const maxLogBodyBytes = 512

// Config 是构造 Client 所需的最小配置。
type Config struct {
	// BaseURL 是模型服务的根地址,例如 https://api.openai.com/v1
	// 或 https://api.deepseek.com/v1。末尾斜杠会被自动去掉。
	BaseURL string
	// APIKey 用于在 Authorization 头中鉴权。
	APIKey string
	// Model 是默认使用的模型名,例如 "gpt-4o-mini"、"deepseek-chat"。
	Model string
	// HTTPClient 可选,允许调用方注入自定义 transport(例如测试或代理)。
	HTTPClient *http.Client
}

// Client 是对 OpenAI 兼容 /chat/completions 端点的轻量封装。
//
// Day 1 只暴露非流式 Chat 方法;流式入口会在 Day 11 补齐。
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient 基于 Config 构造一个 Client。
// 缺省的 HTTPClient 会被设为 60 秒超时,
// 以保证在用户没有显式注入时仍能稳定取消请求。
func NewClient(cfg Config) *Client {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: cfg.HTTPClient}
}

// DefaultModel 返回 Client 构造时配置的默认模型名。
func (c *Client) DefaultModel() string {
	return c.cfg.Model
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider: http call: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("provider: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseErrorResponse(resp.StatusCode, raw)
	}

	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("provider: decode response: %w (body=%q)", err, truncateForLog(raw))
	}
	return &out, nil
}

// parseErrorResponse 尝试将非 2xx 响应解析为 APIError,
// 解析失败时退化为带状态码的通用错误。
//
// 同时兼容两种常见错误格式:
//   - OpenAI 风格:`{"error": {"code": ..., "message": ..., "type": ...}}`
//   - 平铺风格:`{"code": ..., "message": ..., "type": ...}`
func parseErrorResponse(status int, raw []byte) error {
	if apiErr := decodeAPIError(raw); apiErr != nil && apiErr.Message != "" {
		apiErr.StatusCode = status
		return apiErr
	}
	return fmt.Errorf("provider: http %d: %s", status, truncateForLog(raw))
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
