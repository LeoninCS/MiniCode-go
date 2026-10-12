package provider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func validationToolCall(id string) provider.ToolCall {
	return provider.ToolCall{ID: id, Type: provider.ToolTypeFunction,
		Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"file.go"}`}}
}

func invalidMessageSequences() []struct {
	name     string
	messages []provider.Message
	want     string
} {
	a, b := validationToolCall("a"), validationToolCall("b")
	batch := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{a, b}}
	resultA := provider.NewMessage(provider.RoleTool, "A", "a")
	badJSON := a
	badJSON.Function.Arguments = `{"path":`
	arrayJSON := a
	arrayJSON.Function.Arguments = `[]`
	return []struct {
		name     string
		messages []provider.Message
		want     string
	}{
		{"unsupported role", []provider.Message{provider.NewMessage("unknown", "x", "")}, `message 0: unsupported role "unknown"`},
		{"null user", []provider.Message{{Role: provider.RoleUser}}, "message 0: invalid user message"},
		{"user with tool calls", []provider.Message{{Role: provider.RoleUser, Content: resultA.Content, ToolCalls: []provider.ToolCall{a}}}, "invalid user message"},
		{"user with tool ID", []provider.Message{provider.NewMessage(provider.RoleUser, "x", "a")}, "invalid user message"},
		{"null system", []provider.Message{{Role: provider.RoleSystem}}, "invalid system message"},
		{"system with tool calls", []provider.Message{{Role: provider.RoleSystem, Content: resultA.Content, ToolCalls: []provider.ToolCall{a}}}, "invalid system message"},
		{"system with tool ID", []provider.Message{provider.NewMessage(provider.RoleSystem, "x", "a")}, "invalid system message"},
		{"null assistant without calls", []provider.Message{{Role: provider.RoleAssistant}}, "invalid assistant message"},
		{"assistant with tool ID", []provider.Message{provider.NewMessage(provider.RoleAssistant, "x", "a")}, "invalid assistant message"},
		{"incomplete JSON", []provider.Message{{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{badJSON}}}, "message 0 tool call 0"},
		{"non-object JSON", []provider.Message{{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{arrayJSON}}}, "JSON object"},
		{"duplicate call ID", []provider.Message{{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{a, a}}}, `duplicate tool call ID "a"`},
		{"orphan result", []provider.Message{resultA}, `tool result ID "a" has no preceding call`},
		{"missing all results", []provider.Message{batch}, `missing results for IDs ["a" "b"]`},
		{"missing one result", []provider.Message{batch, resultA}, `missing results for IDs ["b"]`},
		{"wrong result ID", []provider.Message{batch, provider.NewMessage(provider.RoleTool, "C", "c")}, `message 1: unexpected or duplicate tool result ID "c"`},
		{"empty result ID", []provider.Message{batch, provider.NewMessage(provider.RoleTool, "x", "")}, `unexpected or duplicate tool result ID ""`},
		{"duplicate result", []provider.Message{batch, resultA, resultA}, `message 2: unexpected or duplicate tool result ID "a"`},
		{"null result", []provider.Message{batch, {Role: provider.RoleTool, ToolCallID: "a"}}, `invalid tool result for ID "a"`},
		{"result with tool calls", []provider.Message{batch, {Role: provider.RoleTool, ToolCallID: "a", Content: resultA.Content, ToolCalls: []provider.ToolCall{a}}}, "invalid tool result"},
		{"user interrupts batch", []provider.Message{batch, resultA, provider.NewMessage(provider.RoleUser, "continue", "")}, `message 2: assistant message 0 tool calls are missing results for IDs ["b"]`},
		{"assistant interrupts batch", []provider.Message{batch, provider.NewMessage(provider.RoleAssistant, "done", "")}, "message 1: assistant message 0"},
		{"system interrupts batch", []provider.Message{batch, provider.NewMessage(provider.RoleSystem, "summarize", "")}, "message 1: assistant message 0"},
	}
}

func TestValidateMessages_RejectsInvalidSequencesWithoutRepairing(t *testing.T) {
	for _, tc := range invalidMessageSequences() {
		t.Run(tc.name, func(t *testing.T) {
			before, err := json.Marshal(tc.messages)
			if err != nil {
				t.Fatal(err)
			}
			err = provider.ValidateMessages(tc.messages)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			after, err := json.Marshal(tc.messages)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("validation changed history: err=%v", err)
			}
		})
	}
}

func TestValidateMessages_AcceptsCompleteBatchesAndContextSummaries(t *testing.T) {
	a, b := validationToolCall("a"), validationToolCall("b")
	messages := []provider.Message{
		provider.NewMessage(provider.RoleSystem, "workspace rules", ""),
		provider.NewMessage(provider.RoleUser, "<context-summary>previous progress</context-summary>", ""),
		provider.NewMessage(provider.RoleUser, "continue", ""),
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{a, b}},
		provider.NewMessage(provider.RoleTool, "B", "b"),
		provider.NewMessage(provider.RoleTool, "", "a"),
		// 调用 ID 可以在后续独立批次复用，配对只针对当前批次。
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{a}},
		provider.NewMessage(provider.RoleTool, "A", "a"),
		provider.NewMessage(provider.RoleSystem, "turn-limit summary instruction", ""),
		provider.NewMessage(provider.RoleAssistant, "done", ""),
	}
	for name, history := range map[string][]provider.Message{"empty history": nil, "complete context": messages} {
		t.Run(name, func(t *testing.T) {
			if err := provider.ValidateMessages(history); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClient_InvalidMessagesNeverReachHTTP(t *testing.T) {
	var requests atomic.Int32
	_, client := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "invalid request reached server", http.StatusInternalServerError)
	})
	for _, tc := range invalidMessageSequences() {
		t.Run(tc.name, func(t *testing.T) {
			req := provider.ChatRequest{Messages: tc.messages}
			resp, err := client.Chat(context.Background(), req)
			if resp != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Chat: resp=%v err=%v", resp, err)
			}
			stream, err := client.ChatStream(context.Background(), req)
			if stream != nil {
				_ = stream.Close()
			}
			if stream != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ChatStream: stream=%v err=%v", stream, err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid messages sent %d HTTP requests", got)
	}
}
