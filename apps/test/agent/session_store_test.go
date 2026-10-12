package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func TestSessionPersistence_RestoresHistory(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	replies := []provider.Message{
		provider.NewMessage(provider.RoleAssistant, "第一轮回答", ""),
		provider.NewMessage(provider.RoleAssistant, "第二轮回答", ""),
	}
	requests := make(chan provider.ChatRequest, len(replies))
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- request
		index := int(count.Add(1)) - 1
		writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: replies[index]}}})
	}))
	defer srv.Close()

	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "key", Model: "model"})
	first, err := agent.OpenSessionByID(client, nil, "")
	if err != nil {
		t.Fatalf("open new session: %v", err)
	}
	id := first.ID()
	path, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionstore.ValidateID(id); err != nil {
		t.Fatalf("new session ID: %v", err)
	}
	if err := first.Turn(context.Background(), "第一轮问题", &recordingOutput{}); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first session: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session permissions = %o, want 600", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if strings.Contains(string(content), "MiniCode，一个在本地工作区") {
		t.Fatal("session file must not persist the generated system prompt")
	}
	snapshot, err := sessionstore.Load(path)
	if err != nil || snapshot.ID != id {
		t.Fatalf("saved ID = %q, want %q, error = %v", snapshot.ID, id, err)
	}

	restored, err := agent.OpenSessionByID(client, nil, id)
	if err != nil {
		t.Fatalf("restore session: %v", err)
	}
	defer func() { _ = restored.Close() }()
	if restored.ID() != id {
		t.Fatalf("restored ID = %q, want %q", restored.ID(), id)
	}
	if err := restored.Turn(context.Background(), "第二轮问题", &recordingOutput{}); err != nil {
		t.Fatalf("second turn: %v", err)
	}

	firstRequest := <-requests
	secondRequest := <-requests
	if len(firstRequest.Messages) != 2 {
		t.Fatalf("first messages = %+v", firstRequest.Messages)
	}
	if len(secondRequest.Messages) != 4 {
		t.Fatalf("restored messages = %+v", secondRequest.Messages)
	}
	want := []string{"第一轮问题", "第一轮回答", "第二轮问题"}
	for index, text := range want {
		if got := secondRequest.Messages[index+1].Text(); got != text {
			t.Fatalf("restored message %d = %q, want %q", index, got, text)
		}
	}
	if secondRequest.Messages[0].Role != provider.RoleSystem {
		t.Fatalf("first restored role = %q", secondRequest.Messages[0].Role)
	}
}

func TestSessionPersistence_LegacyFileGetsStableID(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	const id = "11111111-1111-4111-8111-111111111111"
	legacy := map[string]any{
		"version": 1, "workspace": workspace, "last_turn_status": "failed",
		"messages": []provider.Message{provider.NewMessage(provider.RoleUser, "保留这条历史", "")},
	}
	path := writeSessionFile(t, workspace, id, legacy)
	client := provider.NewClient(provider.Config{BaseURL: "http://127.0.0.1", APIKey: "key", Model: "model"})
	first, err := agent.OpenSessionByID(client, nil, id)
	if err != nil {
		t.Fatalf("restore legacy session: %v", err)
	}
	defer func() { _ = first.Close() }()
	snapshot, err := sessionstore.Load(path)
	if err != nil || snapshot.ID != id || first.ID() != id || snapshot.LastTurnStatus != "failed" || len(snapshot.Messages) != 1 || snapshot.Messages[0].Text() != "保留这条历史" {
		t.Fatalf("migrated snapshot = %+v, error = %v", snapshot, err)
	}
	second, err := agent.OpenSessionByID(client, nil, id)
	if err != nil {
		t.Fatalf("restore migrated session: %v", err)
	}
	defer func() { _ = second.Close() }()
	if second.ID() != first.ID() {
		t.Fatalf("session ID changed: %q -> %q", first.ID(), second.ID())
	}
}

func TestNewSession_UniqueIDs(t *testing.T) {
	client := provider.NewClient(provider.Config{BaseURL: "http://127.0.0.1", APIKey: "key", Model: "model"})
	first, err := agent.NewSession(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := agent.NewSession(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if err := sessionstore.ValidateID(first.ID()); err != nil {
		t.Fatal(err)
	}
	if err := sessionstore.ValidateID(second.ID()); err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() {
		t.Fatalf("different sessions share ID %q", first.ID())
	}
}

func TestSessionPersistence_RejectsInvalidFiles(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	const id = "11111111-1111-4111-8111-111111111111"
	client := provider.NewClient(provider.Config{BaseURL: "http://127.0.0.1", APIKey: "key", Model: "model"})
	call := provider.ToolCall{ID: "call_1", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"README.md"}`}}

	cases := []struct {
		name string
		body any
		want string
	}{
		{name: "invalid ID", body: map[string]any{"version": 1, "id": "../outside", "workspace": workspace, "last_turn_status": "completed", "messages": []any{}}, want: "invalid session ID"},
		{name: "unsupported version", body: map[string]any{"version": 3, "workspace": workspace, "last_turn_status": "completed", "messages": []any{}}, want: "unsupported version"},
		{name: "workspace mismatch", body: map[string]any{"version": 1, "workspace": workspace + "-other", "last_turn_status": "completed", "messages": []any{}}, want: "workspace mismatch"},
		{name: "incomplete tool call", body: map[string]any{"version": 1, "workspace": workspace, "last_turn_status": "completed", "messages": []provider.Message{{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}}}, want: "missing results"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeSessionFile(t, workspace, id, tc.body)
			session, err := agent.OpenSessionByID(client, nil, id)
			if session != nil {
				_ = session.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("malformed JSON", func(t *testing.T) {
		path := writeSessionFile(t, workspace, id, nil)
		if err := os.WriteFile(path, []byte(`{"version":`), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		session, err := agent.OpenSessionByID(client, nil, id)
		if session != nil {
			_ = session.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "decode JSON") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestSessionPersistence_SaveFailureIsReported(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", "")}}})
	}))
	defer srv.Close()
	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "key", Model: "model"})
	session, err := agent.OpenSessionByID(client, nil, "")
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer func() { _ = session.Close() }()
	path, err := sessionstore.Path(workspace, session.ID())
	if err != nil {
		t.Fatal(err)
	}
	// 模拟会话运行期间存储目录被移除，后续保存必须报告失败。
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := session.Turn(context.Background(), "hello", &recordingOutput{}); err == nil || !strings.Contains(err.Error(), "save session") {
		t.Fatalf("error = %v, want save session error", err)
	}
}

func writeSessionFile(t *testing.T, workspace, id string, snapshot any) string {
	t.Helper()
	path, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSessionPersistence_RestoresLargeArchiveWithoutResummarizing(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	const id = "11111111-1111-4111-8111-111111111111"
	archived := "ARCHIVED REQUEST:" + strings.Repeat("x", 1200000)
	history := []provider.Message{
		provider.NewMessage(provider.RoleUser, archived, ""),
		provider.NewMessage(provider.RoleAssistant, "archived answer", ""),
		provider.NewMessage(provider.RoleUser, strings.Repeat("r", 80000), ""),
		provider.NewMessage(provider.RoleAssistant, "recent answer", ""),
	}
	checkpoint := &compaction.State{Summary: "persisted checkpoint", FirstKeptMessageIndex: 2}
	path := writeSessionFile(t, workspace, id, sessionstore.Snapshot{
		Version: sessionstore.Version, ID: id, Workspace: workspace, LastTurnStatus: "completed", Messages: history, Compaction: checkpoint,
	})
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if len(req.Tools) == 0 || !strings.Contains(req.Messages[1].Text(), checkpoint.Summary) {
			t.Fatal("restoring a checkpoint regenerated a summary")
		}
		for _, message := range req.Messages {
			if strings.Contains(message.Text(), "ARCHIVED REQUEST:") || message.Text() == "archived answer" {
				t.Fatal("archived messages leaked into model context")
			}
		}
		if calls == 1 {
			// 恢复时原始历史比模型上下文长，超限重试不能按原始历史长度切片。
			return nil, &provider.APIError{StatusCode: 400, Code: "context_length_exceeded"}
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "continued", "")}}}, nil
	}}
	session, err := agent.OpenSessionByID(model, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	output := &recordingOutput{}
	if err := session.Turn(context.Background(), "continue", output); err != nil {
		_ = session.Close()
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || hasAutoCompact(output) {
		t.Fatalf("restore used archive for context budget: calls=%d automatic=%t", calls, hasAutoCompact(output))
	}
	saved, err := sessionstore.Load(path)
	if err != nil || len(saved.Messages) != 6 || !reflect.DeepEqual(saved.Messages[:4], history) || !reflect.DeepEqual(saved.Compaction, checkpoint) {
		t.Fatalf("archive changed after recovery: err=%v messages=%d", err, len(saved.Messages))
	}
	restored, err := agent.OpenSessionByID(model, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if err := restored.Turn(context.Background(), "continue again", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("reopening checkpoint made extra model calls: %d", calls)
	}
	savedAgain, err := sessionstore.Load(path)
	if err != nil || len(savedAgain.Messages) != 8 || !reflect.DeepEqual(savedAgain.Messages[:6], saved.Messages) || !reflect.DeepEqual(savedAgain.Compaction, checkpoint) {
		t.Fatal("reopening lost original archive or checkpoint")
	}
}

func TestSessionPersistence_RestoresV1SummaryAsCheckpoint(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	const id = "11111111-1111-4111-8111-111111111111"
	history := []provider.Message{provider.NewMessage(provider.RoleUser, "recent request", ""), provider.NewMessage(provider.RoleAssistant, "recent answer", "")}
	checkpoint := &compaction.State{Summary: "legacy checkpoint"}
	legacyMessages := checkpoint.ContextMessages(history)
	path := writeSessionFile(t, workspace, id, sessionstore.Snapshot{
		Version: 1, ID: id, Workspace: workspace, LastTurnStatus: "completed", Messages: legacyMessages,
	})
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if len(req.Tools) == 0 || len(req.Messages) != 5 || !reflect.DeepEqual(req.Messages[1:4], legacyMessages) || req.Messages[4].Text() != "continue" {
			t.Fatal("legacy restore changed the previous model context")
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "continued", "")}}}, nil
	}}
	session, err := agent.OpenSessionByID(model, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Turn(context.Background(), "continue", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	saved, err := sessionstore.Load(path)
	if err != nil || calls != 1 || saved.Version != 2 || len(saved.Messages) != 4 || !reflect.DeepEqual(saved.Messages[:2], history) || !reflect.DeepEqual(saved.Compaction, checkpoint) {
		t.Fatalf("legacy checkpoint was not persisted as v2: calls=%d err=%v", calls, err)
	}
}
