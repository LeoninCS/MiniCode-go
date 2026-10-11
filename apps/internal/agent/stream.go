package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
)

// chat 自动使用 Provider 的流式能力；完整结束前只交付预览事件，
// 不向历史写入或执行任何工具。
func (s *Session) chat(ctx context.Context, req provider.ChatRequest, output Output) (*provider.ChatResponse, error) {
	client, ok := s.client.(provider.StreamingProvider)
	if !ok {
		return s.client.Chat(ctx, req)
	}
	stream, err := client.ChatStream(ctx, req)
	if errors.Is(err, provider.ErrStreamingUnsupported) {
		return s.client.Chat(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, errors.New("agent: provider returned a nil stream")
	}
	defer stream.Close()
	preview, ok := output.(StreamOutput)
	if ok {
		defer preview.StreamDone()
	}
	var content strings.Builder
	calls := make(map[int]provider.ToolCall)
	var finish string
	var usage *provider.Usage
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if finish != "" && (event.Content != "" || len(event.ToolCalls) > 0 || event.FinishReason != "") {
			return nil, errors.New("agent: received response delta after finish reason")
		}
		content.WriteString(event.Content)
		for _, delta := range event.ToolCalls {
			if delta.Index < 0 {
				return nil, errors.New("agent: invalid streamed tool call index")
			}
			call := calls[delta.Index]
			if delta.ID != "" {
				if call.ID != "" && call.ID != delta.ID {
					return nil, fmt.Errorf("agent: streamed tool call %d changed ID", delta.Index)
				}
				call.ID = delta.ID
			}
			if delta.Type != "" {
				if call.Type != "" && call.Type != delta.Type {
					return nil, fmt.Errorf("agent: streamed tool call %d changed type", delta.Index)
				}
				call.Type = delta.Type
			}
			call.Function.Name += delta.Function.Name
			call.Function.Arguments += delta.Function.Arguments
			calls[delta.Index] = call
		}
		if event.FinishReason != "" {
			finish = event.FinishReason
		}
		if event.Usage != nil {
			copy := *event.Usage
			usage = &copy
		}
		if ok {
			preview.StreamEvent(event)
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if finish == "" {
		return nil, errors.New("agent: stream ended without a finish reason")
	}
	message := provider.Message{Role: provider.RoleAssistant}
	if content.Len() > 0 {
		text := content.String()
		message.Content = &text
	}
	for i := 0; i < len(calls); i++ {
		call, ok := calls[i]
		if !ok {
			return nil, errors.New("agent: streamed tool call indices must be contiguous")
		}
		message.ToolCalls = append(message.ToolCalls, call)
	}
	return &provider.ChatResponse{Choices: []provider.Choice{{Index: 0, Message: message, FinishReason: finish}}, Usage: usage}, nil
}
