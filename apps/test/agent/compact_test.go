package agent_test

import (
	"context"
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
