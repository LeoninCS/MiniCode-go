package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const maxSSEEventBytes = 4 * 1024 * 1024

// ChatStream 建立 SSE 请求，按事件交付增量；调用方必须关闭返回的流。
func (c *Client) ChatStream(ctx context.Context, req ChatRequest) (Stream, error) {
	req, err := c.prepareRequest(req)
	if err != nil {
		return nil, err
	}
	payload := struct {
		ChatRequest
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}{ChatRequest: req, Stream: true}
	payload.StreamOptions.IncludeUsage = true
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("provider: marshal stream request: %w", err)
	}
	ctx, span := startGenerationSpan(ctx, c.cfg.Tracer, req.Model, body)
	ctx, cancel := context.WithCancel(ctx)
	fail := func(err error) (Stream, error) {
		cancel()
		recordSpanError(span, err)
		span.End()
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fail(fmt.Errorf("provider: build stream request: %w", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fail(fmt.Errorf("provider: stream HTTP call: %w", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return fail(fmt.Errorf("provider: read stream error: %w", err))
		}
		span.SetAttributes(
			attribute.String("gen_ai.response.raw", string(raw)),
			attribute.String("langfuse.observation.output", string(raw)),
		)
		return fail(parseErrorResponse(resp.StatusCode, raw))
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		_ = resp.Body.Close()
		return fail(errors.New("provider: expected text/event-stream response"))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), maxSSEEventBytes)
	scanner.Split(splitSSELines)
	return &openAIStream{ctx: ctx, cancel: cancel, body: resp.Body, scanner: scanner, span: span, firstLine: true}, nil
}

// openAIStream 的 Recv 顺序使用，Close 可以并发调用以中断阻塞读取。
type openAIStream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	body      io.ReadCloser
	scanner   *bufio.Scanner
	span      trace.Span
	firstLine bool
	recvMu    sync.Mutex
	mu        sync.Mutex
	terminal  error
	raw       strings.Builder
	closeOnce sync.Once
	closeErr  error
}

func (s *openAIStream) Recv() (StreamEvent, error) {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	for {
		if err := s.state(); err != nil {
			return StreamEvent{}, err
		}
		if err := s.ctx.Err(); err != nil {
			return StreamEvent{}, s.finish(fmt.Errorf("provider: receive stream: %w", err))
		}
		name, data, err := s.readEvent()
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
			return StreamEvent{}, s.finish(fmt.Errorf("provider: receive stream: %w", err))
		}
		if name == "ping" || data == "" {
			continue
		}
		s.mu.Lock()
		if s.terminal != nil {
			err := s.terminal
			s.mu.Unlock()
			return StreamEvent{}, err
		}
		s.raw.WriteString("data: " + data + "\n\n")
		s.mu.Unlock()
		if strings.TrimSpace(data) == "[DONE]" {
			return StreamEvent{}, s.finish(io.EOF)
		}
		if apiErr := decodeAPIError([]byte(data)); apiErr != nil {
			return StreamEvent{}, s.finish(apiErr)
		}
		if name == "error" {
			return StreamEvent{}, s.finish(&APIError{Message: truncateForLog([]byte(data))})
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Usage   *Usage `json:"usage"`
			Choices []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    *int         `json:"index"`
						ID       string       `json:"id"`
						Type     ToolType     `json:"type"`
						Function FunctionCall `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return StreamEvent{}, s.finish(fmt.Errorf("provider: decode stream event: %w", err))
		}
		if len(chunk.Choices) > 1 || (len(chunk.Choices) == 1 && chunk.Choices[0].Index != 0) {
			return StreamEvent{}, s.finish(errors.New("provider: stream must contain a single choice at index 0"))
		}
		event := StreamEvent{Usage: chunk.Usage}
		if len(chunk.Choices) == 1 {
			choice := chunk.Choices[0]
			event.Content, event.FinishReason = choice.Delta.Content, choice.FinishReason
			for _, call := range choice.Delta.ToolCalls {
				if call.Index == nil || *call.Index < 0 {
					return StreamEvent{}, s.finish(errors.New("provider: invalid streamed tool call index"))
				}
				event.ToolCalls = append(event.ToolCalls, ToolCallDelta{Index: *call.Index, ID: call.ID, Type: call.Type, Function: call.Function})
			}
		}
		s.mu.Lock()
		if s.terminal != nil {
			err := s.terminal
			s.mu.Unlock()
			return StreamEvent{}, err
		}
		if chunk.ID != "" {
			s.span.SetAttributes(attribute.String("gen_ai.response.id", chunk.ID))
		}
		if chunk.Model != "" {
			s.span.SetAttributes(attribute.String("gen_ai.response.model", chunk.Model))
		}
		if chunk.Usage != nil {
			s.span.SetAttributes(
				attribute.Int("gen_ai.usage.input_tokens", chunk.Usage.PromptTokens),
				attribute.Int("gen_ai.usage.output_tokens", chunk.Usage.CompletionTokens),
				attribute.Int("gen_ai.usage.total_tokens", chunk.Usage.TotalTokens),
			)
		}
		s.mu.Unlock()
		if event.Content != "" || len(event.ToolCalls) > 0 || event.FinishReason != "" || event.Usage != nil {
			return event, nil
		}
	}
}

func (s *openAIStream) state() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal
}

func (s *openAIStream) finish(err error) error {
	s.mu.Lock()
	if s.terminal == nil {
		s.terminal = err
		s.span.SetAttributes(attribute.String("gen_ai.response.raw", s.raw.String()), attribute.String("langfuse.observation.output", s.raw.String()))
		if !errors.Is(err, io.EOF) {
			recordSpanError(s.span, err)
		}
		s.span.End()
	}
	err = s.terminal
	s.mu.Unlock()
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = s.body.Close()
	})
	return err
}

func (s *openAIStream) Close() error {
	s.finish(io.ErrClosedPipe)
	return s.closeErr
}

// readEvent 跳过注释及未知字段，按空行分隔事件并合并多行 data。
func (s *openAIStream) readEvent() (string, string, error) {
	var name string
	var data []string
	size := 0
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if s.firstLine {
			line = strings.TrimPrefix(line, "\ufeff")
			s.firstLine = false
		}
		if line == "" {
			if len(data) > 0 {
				return name, strings.Join(data, "\n"), nil
			}
			name = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			size += len(value) + 1
			if size > maxSSEEventBytes {
				return "", "", errors.New("SSE event exceeds size limit")
			}
			data = append(data, value)
		}
	}
	if err := s.scanner.Err(); err != nil {
		return "", "", err
	}
	return "", "", io.ErrUnexpectedEOF
}

// splitSSELines 支持 LF、CRLF 和独立 CR，并保留网络分片末尾的 CR。
func splitSSELines(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i], nil
		}
		if b == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			advance := i + 1
			if advance < len(data) && data[advance] == '\n' {
				advance++
			}
			return advance, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
