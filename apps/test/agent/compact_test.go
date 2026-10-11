package agent_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestSession_AutomaticCompactionBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"small input", "hello", false},
		{"large first input without usage", strings.Repeat("x", 983044), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := &recordingOutput{}
			calls := 0
			model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
				calls++
				if got := hasAutoCompact(output); got != tc.want {
					t.Fatalf("trigger before model request = %t, want %t", got, tc.want)
				}
				if len(req.Messages) != 2 || req.Messages[1].Text() != tc.input {
					t.Fatal("placeholder compaction changed the conversation")
				}
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", "")}}}, nil
			}}
			session := newSession(t, model)
			if err := session.Turn(context.Background(), tc.input, output); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("model calls = %d, want 1", calls)
			}
		})
	}
}

func TestSession_AutomaticCompactionAfterCompleteToolResults(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("large.txt", []byte(strings.Repeat("x", 8000)), 0600); err != nil {
		t.Fatal(err)
	}
	output := &recordingOutput{}
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if calls == 1 {
			if hasAutoCompact(output) {
				t.Fatal("small initial request triggered compaction")
			}
			message := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
				{ID: "read-large", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"large.txt"}`}},
				{ID: "read-again", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"large.txt"}`}},
			}}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: message}}, Usage: &provider.Usage{PromptTokens: 244760, CompletionTokens: 1, TotalTokens: 244761}}, nil
		}
		if calls != 2 || !hasAutoCompact(output) {
			t.Fatalf("calls = %d, output = %q", calls, output.events)
		}
		if len(req.Messages) != 5 || len(req.Messages[2].ToolCalls) != 2 || req.Messages[3].ToolCallID != "read-large" || req.Messages[4].ToolCallID != "read-again" {
			t.Fatalf("compaction ran before the complete tool batch: %+v", req.Messages)
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", "")}}}, nil
	}}
	if err := newSession(t, model).Turn(context.Background(), "read twice", output); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("model calls = %d", calls)
	}
}

func TestSession_AutomaticCompactionAfterRestore(t *testing.T) {
	t.Chdir(t.TempDir())
	largeInput := strings.Repeat("x", 983044)
	output := &recordingOutput{}
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if calls == 2 && (!hasAutoCompact(output) || len(req.Messages) != 4 || req.Messages[1].Text() != largeInput) {
			t.Fatal("restored history was not included in the context budget before the request")
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", "")}}}, nil
	}}
	first, err := agent.OpenSessionByID(model, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	id := first.ID()
	if err := first.Turn(context.Background(), largeInput, &recordingOutput{}); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := agent.OpenSessionByID(model, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if err := restored.Turn(context.Background(), "continue", output); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("model calls = %d", calls)
	}
}

func hasAutoCompact(output *recordingOutput) bool {
	for _, event := range output.events {
		if strings.Contains(event, "已自动触发压缩") {
			return true
		}
	}
	return false
}

func TestSession_ContextOverflowRetriesOnlyCurrentRequest(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if req.MaxTokens != 8192 {
			t.Fatalf("max tokens = %d", req.MaxTokens)
		}
		if calls == 1 {
			message := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
				ID: "append-once", Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf x >> executed.txt"}`},
			}}}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: message}}}, nil
		}
		if calls > 3 || len(req.Messages) != 4 || req.Messages[1].Text() != "执行一次" || req.Messages[3].ToolCallID != "append-once" {
			t.Fatalf("retry replayed or lost history: calls=%d, messages=%+v", calls, req.Messages)
		}
		if calls == 2 {
			return nil, fmt.Errorf("request: %w", &provider.APIError{StatusCode: 400, Code: "context_length_exceeded"})
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "完成", "")}},
			Usage:   &provider.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		}, nil
	}}
	output := &recordingOutput{}
	if err := newSession(t, model).Turn(context.Background(), "执行一次", output); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile("executed.txt"); err != nil || string(got) != "x" {
		t.Fatalf("tool was replayed: contents=%q, error=%v", got, err)
	}
	if calls != 3 || len(output.events) != 2 || !strings.Contains(output.events[0], "重试当前模型请求一次") || output.events[1] != "message:完成" {
		t.Fatalf("calls=%d, output=%q", calls, output.events)
	}
	if got := output.processEvents[len(output.processEvents)-1]; got != "stats:3:1:2" {
		t.Fatalf("retry stats = %s", got)
	}
}

func TestSession_ContextRecoveryIsBoundedAndSpecific(t *testing.T) {
	overflow := &provider.APIError{StatusCode: 400, Code: "context_length_exceeded"}
	outputLimit := &provider.APIError{StatusCode: 400, Param: "max_tokens", Message: "max_tokens is 9216 but the maximum output length is 8192 tokens"}
	rateLimit := &provider.APIError{StatusCode: 429, Code: "rate_limit_exceeded"}
	for _, tc := range []struct {
		name        string
		first, last error
		calls       int
	}{
		{"persistent overflow", overflow, overflow, 2},
		{"retry fails differently", overflow, rateLimit, 2},
		{"output limit", outputLimit, outputLimit, 1},
		{"rate limit", rateLimit, rateLimit, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
				calls++
				if calls > tc.calls || len(req.Messages) != 2 || req.Messages[1].Text() != "任务" {
					t.Fatalf("unexpected retry: calls=%d, messages=%+v", calls, req.Messages)
				}
				if calls == 1 {
					return nil, tc.first
				}
				return nil, tc.last
			}}
			output := &recordingOutput{}
			err := newSession(t, model).Turn(context.Background(), "任务", output)
			if !errors.Is(err, tc.last) || calls != tc.calls || len(output.events) != tc.calls-1 {
				t.Fatalf("error=%v, calls=%d, output=%q", err, calls, output.events)
			}
			if got := output.processEvents[len(output.processEvents)-1]; got != fmt.Sprintf("stats:%d:0:0", tc.calls) {
				t.Fatalf("stats = %s", got)
			}
		})
	}
}

func TestSession_ContextOverflowDoesNotRetryAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	model := &fakeProvider{chat: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		cancel()
		return nil, &provider.APIError{StatusCode: 400, Code: "context_length_exceeded"}
	}}
	output := &recordingOutput{}
	if err := newSession(t, model).Turn(ctx, "任务", output); !errors.Is(err, context.Canceled) || calls != 1 || len(output.events) != 0 {
		t.Fatalf("error=%v, calls=%d, output=%q", err, calls, output.events)
	}
}
