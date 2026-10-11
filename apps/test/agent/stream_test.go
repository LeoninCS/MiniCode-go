package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

type fakeStreamingProvider struct {
	fakeProvider
	stream func(context.Context, provider.ChatRequest) (provider.Stream, error)
}

func (f *fakeStreamingProvider) ChatStream(ctx context.Context, req provider.ChatRequest) (provider.Stream, error) {
	return f.stream(ctx, req)
}

type fakeStream struct {
	events     []provider.StreamEvent
	err        error
	beforeRecv func()
	closed     bool
}

func (s *fakeStream) Recv() (provider.StreamEvent, error) {
	if s.beforeRecv != nil {
		s.beforeRecv()
	}
	if len(s.events) == 0 {
		if s.err != nil {
			return provider.StreamEvent{}, s.err
		}
		return provider.StreamEvent{}, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *fakeStream) Close() error { s.closed = true; return nil }

type recordingStreamOutput struct {
	recordingOutput
	deltas []provider.StreamEvent
	done   int
}

func (o *recordingStreamOutput) StreamEvent(event provider.StreamEvent) {
	o.deltas = append(o.deltas, event)
}
func (o *recordingStreamOutput) StreamDone() { o.done++ }

func TestSession_StreamsTextAndAccumulatesInterleavedToolsBeforeExecution(t *testing.T) {
	t.Chdir(t.TempDir())
	first := &fakeStream{events: []provider.StreamEvent{
		{Content: "先写文件"},
		{ToolCalls: []provider.ToolCallDelta{{Index: 1, ID: "write-b", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "wr", Arguments: `{"path":"b.txt",`}}}},
		{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "write-a", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "write", Arguments: `{"path":"a.txt",`}}}},
		{ToolCalls: []provider.ToolCallDelta{
			{Index: 1, Function: provider.FunctionCall{Name: "ite", Arguments: `"content":"B"}`}},
			{Index: 0, Function: provider.FunctionCall{Arguments: `"content":"A"}`}},
		}, FinishReason: "tool_calls"},
		{Usage: &provider.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}},
	}}
	first.beforeRecv = func() {
		for _, path := range []string{"a.txt", "b.txt"} {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("tool executed before stream ended: %s, %v", path, err)
			}
		}
	}
	last := &fakeStream{events: []provider.StreamEvent{
		{Content: "完成"}, {Content: "了", FinishReason: "stop"},
		{Usage: &provider.Usage{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5}},
	}}
	requests := 0
	model := &fakeStreamingProvider{stream: func(ctx context.Context, req provider.ChatRequest) (provider.Stream, error) {
		requests++
		if requests == 1 {
			return first, nil
		}
		if requests != 2 || !first.closed || len(req.Messages) != 5 {
			t.Fatalf("request %d = %+v, closed = %v", requests, req, first.closed)
		}
		assistant := req.Messages[2]
		if assistant.Text() != "先写文件" || len(assistant.ToolCalls) != 2 || assistant.ToolCalls[0].ID != "write-a" || assistant.ToolCalls[1].ID != "write-b" {
			t.Fatalf("assistant = %+v", assistant)
		}
		for i, id := range []string{"write-a", "write-b"} {
			if msg := req.Messages[3+i]; msg.Role != provider.RoleTool || msg.ToolCallID != id {
				t.Fatalf("tool message = %+v", msg)
			}
		}
		return last, nil
	}}
	session := newSession(t, model)
	output := &recordingStreamOutput{}
	if err := session.Turn(context.Background(), "写两个文件", output); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"a.txt": "A", "b.txt": "B"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, error = %v", path, got, err)
		}
	}
	if !last.closed || output.done != 2 || len(output.deltas) != 8 || !reflect.DeepEqual(output.events, []string{"message:完成了"}) {
		t.Fatalf("output = %+v, closed = %v", output, last.closed)
	}
	if got := output.processEvents[len(output.processEvents)-1]; got != "stats:2:2:10" {
		t.Fatalf("stats = %s", got)
	}
}

func TestSession_StreamFailureDiscardsPartialAssistantAndTools(t *testing.T) {
	t.Chdir(t.TempDir())
	broken := &fakeStream{events: []provider.StreamEvent{
		{Content: "partial", ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "write", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "write", Arguments: `{"path":"never.txt","content":"x"}`}}}, FinishReason: "tool_calls"},
	}, err: io.ErrUnexpectedEOF}
	requests := 0
	model := &fakeStreamingProvider{stream: func(ctx context.Context, req provider.ChatRequest) (provider.Stream, error) {
		requests++
		if requests == 1 {
			return broken, nil
		}
		if len(req.Messages) != 3 || req.Messages[1].Role != provider.RoleUser || req.Messages[2].Text() != "重试" {
			t.Fatalf("partial assistant leaked into history: %+v", req.Messages)
		}
		return &fakeStream{events: []provider.StreamEvent{{Content: "恢复", FinishReason: "stop"}}}, nil
	}}
	session := newSession(t, model)
	output := &recordingStreamOutput{}
	if err := session.Turn(context.Background(), "开始", output); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v", err)
	}
	if !broken.closed || output.done != 1 || len(output.events) != 0 {
		t.Fatalf("output = %+v", output)
	}
	if _, err := os.Stat("never.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial stream executed tool: %v", err)
	}
	if err := session.Turn(context.Background(), "重试", output); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output.events, []string{"message:恢复"}) {
		t.Fatalf("messages = %q", output.events)
	}
}

func TestSession_ValidatesStreamBeforeTools(t *testing.T) {
	valid := provider.ToolCallDelta{Index: 0, ID: "write", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "write", Arguments: `{"path":"never.txt","content":"x"}`}}
	for _, tc := range []struct {
		name   string
		events []provider.StreamEvent
		want   string
	}{
		{name: "missing finish", events: []provider.StreamEvent{{Content: "partial"}}, want: "finish reason"},
		{name: "incomplete arguments", events: []provider.StreamEvent{{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "write", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "write", Arguments: `{"path":`}}}, FinishReason: "tool_calls"}}, want: "JSON object"},
		{name: "truncated tool response", events: []provider.StreamEvent{{ToolCalls: []provider.ToolCallDelta{valid}, FinishReason: "length"}}, want: "finish reason"},
		{name: "ID changed", events: []provider.StreamEvent{{ToolCalls: []provider.ToolCallDelta{valid}}, {ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "changed"}}, FinishReason: "tool_calls"}}, want: "changed ID"},
		{name: "index gap", events: []provider.StreamEvent{{ToolCalls: []provider.ToolCallDelta{{Index: 1, ID: "write", Type: provider.ToolTypeFunction, Function: valid.Function}}, FinishReason: "tool_calls"}}, want: "contiguous"},
		{name: "delta after finish", events: []provider.StreamEvent{{Content: "done", FinishReason: "stop"}, {ToolCalls: []provider.ToolCallDelta{valid}}}, want: "after finish"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			stream := &fakeStream{events: tc.events}
			model := &fakeStreamingProvider{stream: func(context.Context, provider.ChatRequest) (provider.Stream, error) { return stream, nil }}
			session := newSession(t, model)
			output := &recordingStreamOutput{}
			if err := session.Turn(context.Background(), "write", output); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
			if !stream.closed || output.done != 1 || len(output.events) != 0 {
				t.Fatalf("output = %+v", output)
			}
			if _, err := os.Stat("never.txt"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid stream executed tool: %v", err)
			}
		})
	}
}

func TestSession_StreamingFallbackOnlyForUnsupportedProviders(t *testing.T) {
	for _, kind := range []string{"non-streaming provider", "unsupported", "API error"} {
		t.Run(kind, func(t *testing.T) {
			chatCalls := 0
			chat := fakeProvider{chat: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
				chatCalls++
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "fallback", "")}}}, nil
			}}
			var model provider.Provider = &chat
			if kind != "non-streaming provider" {
				model = &fakeStreamingProvider{fakeProvider: chat, stream: func(context.Context, provider.ChatRequest) (provider.Stream, error) {
					if kind == "unsupported" {
						return nil, provider.ErrStreamingUnsupported
					}
					return nil, &provider.APIError{Message: "no quota"}
				}}
			}
			session := newSession(t, model)
			err := session.Turn(context.Background(), "hello", &recordingOutput{})
			if kind == "API error" {
				if err == nil || chatCalls != 0 {
					t.Fatalf("error = %v, fallback calls = %d", err, chatCalls)
				}
			} else if err != nil || chatCalls != 1 {
				t.Fatalf("error = %v, fallback calls = %d", err, chatCalls)
			}
		})
	}
}

func TestSession_StreamsTurnLimitSummaryWithoutTools(t *testing.T) {
	requests := 0
	model := &fakeStreamingProvider{stream: func(ctx context.Context, req provider.ChatRequest) (provider.Stream, error) {
		requests++
		if requests <= 500 {
			return &fakeStream{events: []provider.StreamEvent{{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "read", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"missing-file"}`}}}, FinishReason: "tool_calls"}}}, nil
		}
		if requests != 501 || len(req.Tools) != 0 || req.Messages[len(req.Messages)-1].Role != provider.RoleSystem {
			t.Fatalf("summary request = %+v", req)
		}
		return &fakeStream{events: []provider.StreamEvent{{Content: "停止执行", FinishReason: "stop"}}}, nil
	}}
	session := newSession(t, model)
	output := &recordingStreamOutput{}
	if err := session.Turn(context.Background(), "repeat", output); err == nil || !strings.Contains(err.Error(), "maximum model turns") {
		t.Fatalf("error = %v", err)
	}
	if output.done != 501 || !reflect.DeepEqual(output.events, []string{"message:停止执行"}) {
		t.Fatalf("output = %+v", output)
	}
}

// writeStreamResponse 将完整测试回复转换为模型服务的 SSE 响应。
func writeStreamResponse(w http.ResponseWriter, response provider.ChatResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, choice := range response.Choices {
		calls := make([]map[string]any, 0, len(choice.Message.ToolCalls))
		for i, call := range choice.Message.ToolCalls {
			calls = append(calls, map[string]any{"index": i, "id": call.ID, "type": call.Type, "function": call.Function})
		}
		finish := choice.FinishReason
		if finish == "" {
			finish = "stop"
			if len(calls) > 0 {
				finish = "tool_calls"
			}
		}
		_, _ = io.WriteString(w, "data: ")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"index":         choice.Index,
				"delta":         map[string]any{"content": choice.Message.Content, "tool_calls": calls},
				"finish_reason": finish,
			}},
		})
		_, _ = io.WriteString(w, "\n")
	}
	if response.Usage != nil {
		_, _ = io.WriteString(w, "data: ")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}, "usage": response.Usage})
		_, _ = io.WriteString(w, "\n")
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}
