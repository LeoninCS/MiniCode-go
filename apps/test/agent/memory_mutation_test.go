package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/memory"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func memoryToolCall(t *testing.T, id, name string, arguments map[string]any) provider.ToolCall {
	t.Helper()
	data, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return provider.ToolCall{ID: id, Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: name, Arguments: string(data)}}
}

func memoryToolResponse(calls ...provider.ToolCall) *provider.ChatResponse {
	return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: provider.RoleAssistant, ToolCalls: calls}, FinishReason: "tool_calls"}}}
}

func TestSession_ProjectMemoryWriteUpdateDeleteAndClear(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	original := "# 项目记忆\n\n- 代码使用 gofmt。\n- 测试保留在 apps/test/。\n"
	updated := strings.Replace(original, "gofmt", "gofmt 和 go vet", 1)
	remaining := strings.Replace(updated, "- 代码使用 gofmt 和 go vet。\n", "", 1)
	read := func(id string) provider.ToolCall {
		return memoryToolCall(t, id, "read", map[string]any{"path": memory.FileName})
	}
	create := memoryToolCall(t, "create", "write", map[string]any{"path": memory.FileName, "content": original})
	update := memoryToolCall(t, "update", "edit", map[string]any{"path": memory.FileName, "old_text": "gofmt", "new_text": "gofmt 和 go vet"})
	deleteEntry := memoryToolCall(t, "delete", "edit", map[string]any{"path": memory.FileName, "old_text": "- 代码使用 gofmt 和 go vet。\n", "new_text": ""})
	clear := memoryToolCall(t, "clear", "write", map[string]any{"path": memory.FileName, "content": ""})
	plan := []struct {
		memory string
		call   *provider.ToolCall
	}{
		{memory: "", call: ptrMemoryCall(read("read-create"))},
		{memory: "", call: &create},
		{memory: original},
		{memory: original, call: ptrMemoryCall(read("read-update"))},
		{memory: original, call: &update},
		{memory: updated},
		{memory: updated, call: ptrMemoryCall(read("read-delete"))},
		{memory: updated, call: &deleteEntry},
		{memory: remaining},
		{memory: remaining, call: ptrMemoryCall(read("read-clear"))},
		{memory: remaining, call: &clear},
		{memory: ""},
		{memory: ""},
	}
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		if calls >= len(plan) {
			t.Fatal("unexpected memory model request")
		}
		step := plan[calls]
		calls++
		assertProjectMemory(t, req, step.memory)
		if step.call != nil {
			return memoryToolResponse(*step.call), nil
		}
		last := req.Messages[len(req.Messages)-1]
		if calls <= 12 && (last.Role != provider.RoleTool || strings.Contains(last.Text(), "error:")) {
			t.Fatalf("memory mutation failed: %+v", last)
		}
		return memoryAnswer(), nil
	}}
	var approvals []string
	approver := agent.ToolApproverFunc(func(_ context.Context, _ string, call provider.ToolCall) (bool, error) {
		approvals = append(approvals, call.ID)
		return true, nil
	})
	first, err := agent.OpenSessionByID(model, approver, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	id := first.ID()
	if err := first.Turn(context.Background(), "记住代码使用 gofmt，测试保留在 apps/test/", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := agent.OpenSessionByID(model, approver, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	for _, step := range []struct {
		input string
		want  string
	}{
		{input: "更新记忆，代码验证还要运行 go vet", want: updated},
		{input: "忘记代码验证这条记忆，保留测试目录约定", want: remaining},
		{input: "清空全部项目记忆", want: ""},
	} {
		if err := restored.Turn(context.Background(), step.input, &recordingOutput{}); err != nil {
			t.Fatal(err)
		}
		doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
		if err != nil || doc == nil || doc.Content != step.want {
			t.Fatalf("saved memory = %+v, %v, want %q", doc, err, step.want)
		}
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	afterClear, err := agent.OpenSessionByID(model, approver, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = afterClear.Close() })
	if err := afterClear.Turn(context.Background(), "继续工作", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if calls != len(plan) || !reflect.DeepEqual(approvals, []string{"create", "update", "delete", "clear"}) {
		t.Fatalf("requests=%d approvals=%v", calls, approvals)
	}
	path, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sessionstore.Load(path)
	if err != nil || len(snapshot.Messages) != 26 {
		t.Fatalf("memory tools left invalid history: messages=%d err=%v", len(snapshot.Messages), err)
	}
	for _, message := range snapshot.Messages {
		if strings.Contains(message.Text(), "<project-memory>") {
			t.Fatal("injected memory message was stored as raw conversation")
		}
	}
}

func ptrMemoryCall(call provider.ToolCall) *provider.ToolCall { return &call }

func TestSession_ProjectMemoryMutationApprovalAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing string
		tool     string
		args     map[string]any
		approve  bool
		result   string
	}{
		{name: "denied creation", tool: "write", args: map[string]any{"content": "new memory"}, result: "denied by user"},
		{name: "denied update", existing: "original memory", tool: "edit", args: map[string]any{"old_text": "original", "new_text": "updated"}, result: "denied by user"},
		{name: "denied deletion", existing: "original memory", tool: "edit", args: map[string]any{"old_text": "original memory", "new_text": ""}, result: "denied by user"},
		{name: "denied clear", existing: "original memory", tool: "write", args: map[string]any{"content": ""}, result: "denied by user"},
		{name: "write over budget", existing: "original memory", tool: "write", args: map[string]any{"content": strings.Repeat("a", memory.DefaultTokenBudget*4+1)}, approve: true, result: "token budget"},
		{name: "edit over size", existing: "original memory", tool: "edit", args: map[string]any{"old_text": "original", "new_text": strings.Repeat("a", memory.MaxFileBytes+1)}, approve: true, result: "byte limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			t.Chdir(workspace)
			if tc.existing != "" {
				putProjectMemory(t, workspace, tc.existing)
			}
			tc.args["path"] = memory.FileName
			mutation := memoryToolCall(t, "mutation", tc.tool, tc.args)
			calls, approvals := 0, 0
			model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
				calls++
				assertProjectMemory(t, req, tc.existing)
				switch calls {
				case 1:
					return memoryToolResponse(memoryToolCall(t, "read-memory", "read", map[string]any{"path": memory.FileName})), nil
				case 2:
					return memoryToolResponse(mutation), nil
				case 3:
					last := req.Messages[len(req.Messages)-1]
					if last.ToolCallID != mutation.ID || !strings.Contains(last.Text(), tc.result) {
						t.Fatalf("memory refusal not returned to the model: %+v", last)
					}
				default:
					t.Fatal("unexpected mutation request")
				}
				return memoryAnswer(), nil
			}}
			approver := agent.ToolApproverFunc(func(_ context.Context, _ string, got provider.ToolCall) (bool, error) {
				approvals++
				if got != mutation {
					t.Fatal("read requested approval or mutation bypassed approval")
				}
				return tc.approve, nil
			})
			session, err := agent.NewSession(model, approver)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			if err := session.Turn(context.Background(), "修改项目记忆", &recordingOutput{}); err != nil {
				t.Fatal(err)
			}
			if calls != 3 || approvals != 1 {
				t.Fatalf("requests=%d approvals=%d, want 3 and 1", calls, approvals)
			}
			content, err := os.ReadFile(filepath.Join(workspace, memory.FileName))
			if tc.existing == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("denied creation left a memory file: %v", err)
				}
			} else if err != nil || string(content) != tc.existing {
				t.Fatalf("failed mutation changed memory: %q, %v", content, err)
			}
		})
	}
}

func TestSession_ProjectMemoryRefreshAfterCompleteToolBatch(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	created := "- 使用 Go。\n- 保留测试。\n"
	updated := "- 使用 Go。\n"
	calls := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if calls == 1 {
			assertProjectMemory(t, req, "")
			return memoryToolResponse(
				memoryToolCall(t, "read", "read", map[string]any{"path": memory.FileName}),
				memoryToolCall(t, "create", "write", map[string]any{"path": memory.FileName, "content": created}),
				memoryToolCall(t, "delete-entry", "edit", map[string]any{"path": memory.FileName, "old_text": "- 保留测试。\n", "new_text": ""}),
			), nil
		}
		if calls != 2 {
			t.Fatal("unexpected batch request")
		}
		assertProjectMemory(t, req, updated)
		if len(req.Messages) != 7 || req.Messages[4].ToolCallID != "read" || req.Messages[5].ToolCallID != "create" || req.Messages[6].ToolCallID != "delete-entry" {
			t.Fatal("memory refresh split the tool batch")
		}
		return memoryAnswer(), nil
	}}
	session := newSession(t, model)
	if err := session.Turn(context.Background(), "记住使用 Go，再删除测试这条约定", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("requests = %d, want 2", calls)
	}
}

func TestSession_ProjectMemoryRefreshFailurePreservesCompleteToolHistory(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	create := memoryToolCall(t, "create-memory", "write", map[string]any{"path": memory.FileName, "content": "valid memory"})
	other := memoryToolCall(t, "write-fixture", "write", map[string]any{"path": "fixture.txt", "content": "fixture"})
	calls := 0
	model := &fakeProvider{chat: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		if calls != 1 {
			t.Fatal("invalid refreshed memory reached the model")
		}
		return memoryToolResponse(create, other), nil
	}}
	approver := agent.ToolApproverFunc(func(_ context.Context, _ string, call provider.ToolCall) (bool, error) {
		if call.ID == other.ID {
			// 模拟完成首次写入后，外部程序在工具批次尚未结束时破坏了文件。
			putProjectMemory(t, workspace, "\xff")
		}
		return true, nil
	})
	session, err := agent.OpenSessionByID(model, approver, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Turn(context.Background(), "记住项目约定并写入 fixture", &recordingOutput{}); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("refresh error = %v", err)
	}
	path, err := sessionstore.Path(workspace, session.ID())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sessionstore.Load(path)
	if err != nil || snapshot.LastTurnStatus != "failed" || len(snapshot.Messages) != 4 || snapshot.Messages[2].ToolCallID != create.ID || snapshot.Messages[3].ToolCallID != other.ID {
		t.Fatalf("refresh failure left an incomplete tool batch: %+v, %v", snapshot, err)
	}
}
