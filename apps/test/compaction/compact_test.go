package compaction_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

type summaryProvider func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error)

func (f summaryProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return f(ctx, req)
}

func summaryResponse(text string) *provider.ChatResponse {
	return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, text, ""), FinishReason: "stop"}}}
}

func conversationText(t *testing.T, req provider.ChatRequest) string {
	t.Helper()
	if req.MaxTokens != 8192 || len(req.Tools) != 0 || len(req.Messages) != 2 || req.Messages[0].Role != provider.RoleSystem || req.Messages[1].Role != provider.RoleUser {
		t.Fatalf("invalid summary request shape: messages=%d tools=%d max_tokens=%d", len(req.Messages), len(req.Tools), req.MaxTokens)
	}
	if !strings.Contains(req.Messages[0].Text(), "Do NOT continue the conversation") {
		t.Fatal("summary did not use pi system prompt")
	}
	text := strings.TrimPrefix(req.Messages[1].Text(), "<conversation>\n")
	text, _, ok := strings.Cut(text, "\n</conversation>\n\n")
	if !ok || !utf8.ValidString(text) {
		t.Fatal("invalid serialized conversation")
	}
	return text
}

func TestCompact_PreservesRecentMessagesAndFullSummaryInput(t *testing.T) {
	oldOutput := strings.Repeat("file contents\n", 1000) + "末尾的重要错误"
	arguments := `{"path":"output.go","content":"` + strings.Repeat("x", 5000) + `"}`
	call := provider.ToolCall{ID: "write", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "write", Arguments: arguments}}
	recent := provider.NewMessage(provider.RoleUser, strings.Repeat("r", 80000), "")
	messages := []provider.Message{
		provider.NewMessage(provider.RoleUser, "finish the original task", ""),
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}},
		provider.NewMessage(provider.RoleTool, oldOutput, call.ID),
		provider.NewMessage(provider.RoleAssistant, "old answer", ""),
		recent,
	}
	original := append([]provider.Message(nil), messages...)
	calls := 0
	model := summaryProvider(func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		text := conversationText(t, req)
		for _, want := range []string{"finish the original task", arguments, oldOutput, "old answer"} {
			if !strings.Contains(text, want) {
				t.Fatal("summary input omitted conversation content")
			}
		}
		if strings.Contains(text, recent.Text()) || strings.Contains(text, "workspace instructions") {
			t.Fatal("summary included retained messages or system prompt")
		}
		if !strings.Contains(req.Messages[1].Text(), "## Critical Context") {
			t.Fatal("missing pi summary format")
		}
		resp := summaryResponse("first checkpoint")
		resp.Usage = &provider.Usage{PromptTokens: 10, CompletionTokens: 2}
		return resp, nil
	})
	result, err := compaction.Compact(context.Background(), model, messages, nil)
	contextMessages := result.State.ContextMessages(messages)
	if err != nil || !result.Changed || calls != 1 || len(contextMessages) != 2 || result.State.FirstKeptMessageIndex != 4 || !strings.Contains(contextMessages[0].Text(), "first checkpoint") || !reflect.DeepEqual(contextMessages[1], recent) {
		t.Fatalf("compact: err=%v changed=%t calls=%d messages=%d", err, result.Changed, calls, len(contextMessages))
	}
	if !reflect.DeepEqual(messages, original) || result.Usage.TotalTokens != 12 || result.ModelCalls != 1 || result.UsageResponses != 1 {
		t.Fatal("compaction mutated raw history or usage accounting")
	}
	messages = append(messages, provider.NewMessage(provider.RoleAssistant, "new progress", ""), recent)
	original = append([]provider.Message(nil), messages...)
	model = summaryProvider(func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		text := conversationText(t, req)
		if !strings.Contains(text, recent.Text()) || !strings.Contains(text, "new progress") || strings.Contains(text, "old answer") {
			t.Fatal("repeated compaction did not summarize newly old messages")
		}
		if !strings.Contains(req.Messages[1].Text(), "<previous-summary>\nfirst checkpoint\n</previous-summary>") || !strings.Contains(req.Messages[1].Text(), "Update the existing structured summary") {
			t.Fatal("repeated compaction did not use pi update prompt")
		}
		return summaryResponse("updated checkpoint"), nil
	})
	updated, err := compaction.Compact(context.Background(), model, messages, result.State)
	contextMessages = updated.State.ContextMessages(messages)
	if err != nil || !updated.Changed || len(contextMessages) != 2 || updated.State.FirstKeptMessageIndex != 6 || !strings.Contains(contextMessages[0].Text(), "updated checkpoint") || !reflect.DeepEqual(messages, original) || result.State.FirstKeptMessageIndex != 4 {
		t.Fatalf("repeated compaction: err=%v changed=%t messages=%d", err, updated.Changed, len(contextMessages))
	}
	if err := sessionstore.Validate(sessionstore.Snapshot{Version: sessionstore.Version, Workspace: "/workspace", LastTurnStatus: "completed", Messages: messages, Compaction: updated.State}); err != nil {
		t.Fatalf("raw history and checkpoint cannot be stored: %v", err)
	}
}

func TestCompact_PreservesCompleteRecentToolBatch(t *testing.T) {
	calls := []provider.ToolCall{
		{ID: "a", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"a"}`}},
		{ID: "b", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"b"}`}},
	}
	messages := []provider.Message{
		provider.NewMessage(provider.RoleUser, "read both files", ""),
		{Role: provider.RoleAssistant, ToolCalls: calls},
		provider.NewMessage(provider.RoleTool, strings.Repeat("a", 60000), "a"),
		provider.NewMessage(provider.RoleTool, strings.Repeat("b", 60000), "b"),
	}
	model := summaryProvider(func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		if got := conversationText(t, req); got != "[User]: read both files" {
			t.Fatal("recent tool results entered summary input")
		}
		return summaryResponse("user requested both files"), nil
	})
	result, err := compaction.Compact(context.Background(), model, messages, nil)
	if err != nil || !result.Changed || !reflect.DeepEqual(result.State.ContextMessages(messages)[1:], messages[1:]) {
		t.Fatalf("tool batch changed: err=%v changed=%t", err, result.Changed)
	}
	if err := sessionstore.Validate(sessionstore.Snapshot{Version: sessionstore.Version, Workspace: "/workspace", LastTurnStatus: "completed", Messages: messages, Compaction: result.State}); err != nil {
		t.Fatal(err)
	}
}

func TestCompact_ChunksOversizedInputWithoutDroppingText(t *testing.T) {
	for _, recoverOverflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("provider-overflow=%t", recoverOverflow), func(t *testing.T) {
			old := strings.Repeat("中", 140000) + "THE END"
			messages := []provider.Message{provider.NewMessage(provider.RoleUser, old, ""), provider.NewMessage(provider.RoleUser, strings.Repeat("r", 80000), "")}
			var accepted strings.Builder
			requests, successes := 0, 0
			model := summaryProvider(func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
				requests++
				text := conversationText(t, req)
				if recoverOverflow && utf8.RuneCountInString(text) > 70000 {
					return nil, &provider.APIError{StatusCode: 400, Code: "context_length_exceeded"}
				}
				if successes > 0 && !strings.Contains(req.Messages[1].Text(), fmt.Sprintf("<previous-summary>\ncheckpoint %d\n</previous-summary>", successes)) {
					t.Fatal("chunk did not incorporate previous checkpoint")
				}
				accepted.WriteString(text)
				successes++
				return summaryResponse(fmt.Sprintf("checkpoint %d", successes)), nil
			})
			result, err := compaction.Compact(context.Background(), model, messages, nil)
			if err != nil || !result.Changed || successes < 2 || result.ModelCalls != requests || accepted.String() != "[User]: "+old {
				t.Fatalf("chunking: err=%v changed=%t requests=%d successful=%d accepted_length=%d", err, result.Changed, requests, successes, accepted.Len())
			}
		})
	}
}

func TestCompact_FailureKeepsOriginalHistory(t *testing.T) {
	wantErr := errors.New("network failure")
	for _, tc := range []struct {
		name string
		resp *provider.ChatResponse
		err  error
	}{
		{"request failure", nil, wantErr},
		{"nil response", nil, nil},
		{"empty response", summaryResponse("  "), nil},
		{"no choices", &provider.ChatResponse{}, nil},
		{"length", &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "partial", ""), FinishReason: "length"}}}, nil},
		{"filtered", &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "partial", ""), FinishReason: "content_filter"}}}, nil},
		{"tool call", &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "unexpected"}}}}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []provider.Message{provider.NewMessage(provider.RoleUser, "old", ""), provider.NewMessage(provider.RoleUser, strings.Repeat("r", 80000), "")}
			original := append([]provider.Message(nil), messages...)
			previous := &compaction.State{Summary: "previous checkpoint"}
			model := summaryProvider(func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) { return tc.resp, tc.err })
			result, err := compaction.Compact(context.Background(), model, messages, previous)
			if err == nil || result.Changed || result.State != previous || !reflect.DeepEqual(messages, original) || previous.Summary != "previous checkpoint" || previous.FirstKeptMessageIndex != 0 || result.ModelCalls != 1 || (tc.err != nil && !errors.Is(err, tc.err)) {
				t.Fatalf("failed compact: err=%v changed=%t calls=%d", err, result.Changed, result.ModelCalls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	messages := []provider.Message{provider.NewMessage(provider.RoleUser, "old", ""), provider.NewMessage(provider.RoleUser, strings.Repeat("r", 80000), "")}
	model := summaryProvider(func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
		cancel()
		return summaryResponse("cancelled checkpoint"), nil
	})
	result, err := compaction.Compact(ctx, model, messages, nil)
	if !errors.Is(err, context.Canceled) || result.Changed || result.State != nil {
		t.Fatalf("cancelled compaction: err=%v changed=%t", err, result.Changed)
	}
}

func TestCompact_SkipsWhenThereIsNoOlderHistory(t *testing.T) {
	for _, messages := range [][]provider.Message{
		nil,
		{provider.NewMessage(provider.RoleUser, "small", "")},
		{provider.NewMessage(provider.RoleUser, strings.Repeat("x", 1000000), "")},
	} {
		model := summaryProvider(func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
			t.Fatal("no older history should not call model")
			return nil, nil
		})
		result, err := compaction.Compact(context.Background(), model, messages, nil)
		if err != nil || result.Changed || result.State != nil || result.ModelCalls != 0 {
			t.Fatalf("no-op compact: err=%v changed=%t", err, result.Changed)
		}
	}
}
