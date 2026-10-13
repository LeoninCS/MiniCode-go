package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/memory"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func putProjectMemory(t *testing.T, workspace, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, memory.FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertProjectMemory 检查记忆作为单独的背景消息发送，未替换系统 Prompt 或用户输入。
func assertProjectMemory(t *testing.T, req provider.ChatRequest, want string) {
	t.Helper()
	if err := provider.ValidateMessages(req.Messages); err != nil {
		t.Fatalf("invalid request with project memory: %v", err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != provider.RoleSystem || !strings.Contains(req.Messages[0].Text(), "你是 MiniCode") {
		t.Fatal("project memory replaced the system prompt")
	}
	count := 0
	for index, message := range req.Messages {
		if strings.Contains(message.Text(), "\n<project-memory>\n") {
			count++
			if want == "" || index != 1 || message.Role != provider.RoleUser || !strings.HasSuffix(message.Text(), "\n<project-memory>\n"+want+"\n</project-memory>") {
				t.Fatalf("unexpected project memory at message %d", index)
			}
			if !strings.Contains(message.Text(), "MiniCode.md") || !strings.Contains(message.Text(), "以系统规则和用户本次要求为准") {
				t.Fatal("project memory source or precedence is missing")
			}
		}
	}
	wantCount := 0
	if want != "" {
		wantCount = 1
	}
	if count != wantCount {
		t.Fatalf("project memory messages = %d, want %d", count, wantCount)
	}
}

func memoryAnswer() *provider.ChatResponse {
	return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", ""), FinishReason: "stop"}}}
}

func TestSession_ProjectMemoryReloadedBeforeEachTurn(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	var requests []provider.ChatRequest
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		requests = append(requests, req)
		return memoryAnswer(), nil
	}}
	session, err := agent.OpenSessionByID(model, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	path, err := sessionstore.Path(workspace, session.ID())
	if err != nil {
		t.Fatal(err)
	}
	// 切换进程目录后，Session 仍读取创建时的工作区。
	other := t.TempDir()
	putProjectMemory(t, other, "other project memory")
	t.Chdir(other)
	var history []provider.Message
	for index, tc := range []struct {
		name    string
		content string
		remove  bool
	}{
		{name: "missing", remove: true},
		{name: "created", content: "# 项目约定\r\n使用 Go 🙂。\r\n"},
		{name: "unchanged", content: "# 项目约定\r\n使用 Go 🙂。\r\n"},
		{name: "updated", content: "UPDATED MEMORY: 新的约定"},
		{name: "empty"},
		{name: "deleted", remove: true},
		{name: "recreated", content: "RECREATED MEMORY"},
	} {
		if tc.remove {
			if err := os.Remove(filepath.Join(workspace, memory.FileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		} else {
			putProjectMemory(t, workspace, tc.content)
		}
		question := provider.NewMessage(provider.RoleUser, tc.name, "")
		history = append(history, question)
		if err := session.Turn(context.Background(), tc.name, &recordingOutput{}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(requests) != index+1 {
			t.Fatalf("%s: requests = %d", tc.name, len(requests))
		}
		req := requests[index]
		assertProjectMemory(t, req, tc.content)
		start := 1
		if tc.content != "" {
			start++
		}
		if !reflect.DeepEqual(req.Messages[start:], history) {
			t.Fatalf("%s: raw conversation was changed by memory injection", tc.name)
		}
		history = append(history, provider.NewMessage(provider.RoleAssistant, "done", ""))
		snapshot, err := sessionstore.Load(path)
		if err != nil || !reflect.DeepEqual(snapshot.Messages, history) || snapshot.Compaction != nil {
			t.Fatalf("%s: memory leaked into the snapshot: %v", tc.name, err)
		}
	}
}

func TestSession_ProjectMemoryAcrossNewAndRestoredSessions(t *testing.T) {
	for _, latest := range []string{"LATEST MEMORY", ""} {
		t.Run(fmt.Sprintf("latest=%q", latest), func(t *testing.T) {
			workspace := t.TempDir()
			t.Chdir(workspace)
			putProjectMemory(t, workspace, "ORIGINAL MEMORY")
			var requests []provider.ChatRequest
			model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
				requests = append(requests, req)
				return memoryAnswer(), nil
			}}
			first, err := agent.OpenSessionByID(model, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			id := first.ID()
			if err := first.Turn(context.Background(), "initial", &recordingOutput{}); err != nil {
				t.Fatal(err)
			}
			assertProjectMemory(t, requests[0], "ORIGINAL MEMORY")
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if latest == "" {
				if err := os.Remove(filepath.Join(workspace, memory.FileName)); err != nil {
					t.Fatal(err)
				}
			} else {
				putProjectMemory(t, workspace, latest)
			}
			fresh := newSession(t, model)
			if err := fresh.Turn(context.Background(), "fresh", &recordingOutput{}); err != nil {
				t.Fatal(err)
			}
			assertProjectMemory(t, requests[1], latest)
			restored, err := agent.OpenSessionByID(model, nil, id)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restored.Close() })
			if err := restored.Turn(context.Background(), "continue", &recordingOutput{}); err != nil {
				t.Fatal(err)
			}
			req := requests[2]
			assertProjectMemory(t, req, latest)
			start := 1
			if latest != "" {
				start++
			}
			want := []provider.Message{
				provider.NewMessage(provider.RoleUser, "initial", ""),
				provider.NewMessage(provider.RoleAssistant, "done", ""),
				provider.NewMessage(provider.RoleUser, "continue", ""),
			}
			if !reflect.DeepEqual(req.Messages[start:], want) {
				t.Fatal("restoring memory changed raw conversation history")
			}
		})
	}
}

func TestSession_InvalidProjectMemoryBlocksCreationAndRestore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		message string
	}{
		{name: "invalid UTF-8", content: "\xff", message: "UTF-8"},
		{name: "over budget", content: strings.Repeat("a", memory.DefaultTokenBudget*4+1), message: "token budget"},
		{name: "over byte limit", content: strings.Repeat("a", memory.MaxFileBytes+1), message: "byte limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			t.Chdir(workspace)
			const id = "11111111-1111-4111-8111-111111111111"
			writeSessionFile(t, workspace, id, sessionstore.Snapshot{
				Version: sessionstore.Version, ID: id, Workspace: workspace, LastTurnStatus: "completed",
				Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "saved history", "")},
			})
			putProjectMemory(t, workspace, tc.content)
			model := &fakeProvider{chat: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
				t.Fatal("invalid memory reached the model")
				return nil, nil
			}}
			for _, open := range []func() (*agent.Session, error){
				func() (*agent.Session, error) { return agent.NewSession(model, nil) },
				func() (*agent.Session, error) { return agent.OpenSessionByID(model, nil, "") },
				func() (*agent.Session, error) { return agent.OpenSessionByID(model, nil, id) },
			} {
				session, err := open()
				if session != nil {
					_ = session.Close()
					t.Fatal("invalid memory created a session")
				}
				if err == nil || !strings.Contains(err.Error(), "load project memory") || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("open error = %v, want memory %s error", err, tc.message)
				}
			}
		})
	}
}

func TestSession_ProjectMemoryReloadFailurePreservesHistory(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	putProjectMemory(t, workspace, "valid memory")
	var requests []provider.ChatRequest
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		requests = append(requests, req)
		return memoryAnswer(), nil
	}}
	session, err := agent.OpenSessionByID(model, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Turn(context.Background(), "accepted", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	path, err := sessionstore.Path(workspace, session.ID())
	if err != nil {
		t.Fatal(err)
	}
	before, err := sessionstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	putProjectMemory(t, workspace, "\xff")
	if err := session.Turn(context.Background(), "rejected", &recordingOutput{}); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid reload error = %v", err)
	}
	failed, err := sessionstore.Load(path)
	if err != nil || len(requests) != 1 || !reflect.DeepEqual(failed.Messages, before.Messages) || failed.LastTurnStatus != "failed" {
		t.Fatalf("reload failure sent a request or changed history: %v", err)
	}
	putProjectMemory(t, workspace, "fixed memory")
	if err := session.Turn(context.Background(), "retry", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	assertProjectMemory(t, requests[1], "fixed memory")
	want := append(append([]provider.Message(nil), before.Messages...), provider.NewMessage(provider.RoleUser, "retry", ""))
	if !reflect.DeepEqual(requests[1].Messages[2:], want) {
		t.Fatal("rejected input was appended to the conversation")
	}
}

func TestSession_ProjectMemoryInStreamingToolLoop(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	putProjectMemory(t, workspace, "current memory")
	if err := os.WriteFile("fixture.txt", []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := &fakeStreamingProvider{stream: func(_ context.Context, req provider.ChatRequest) (provider.Stream, error) {
		calls++
		want := "current memory"
		if calls == 3 {
			want = "updated memory"
		}
		assertProjectMemory(t, req, want)
		if calls == 1 {
			putProjectMemory(t, workspace, "updated memory")
			return &fakeStream{events: []provider.StreamEvent{{
				ToolCalls:    []provider.ToolCallDelta{{Index: 0, ID: "read-fixture", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"fixture.txt"}`}}},
				FinishReason: "tool_calls",
			}}}, nil
		}
		if calls == 2 && (len(req.Messages) != 5 || req.Messages[3].Role != provider.RoleAssistant || req.Messages[4].ToolCallID != "read-fixture") {
			t.Fatal("project memory interrupted the complete tool batch")
		}
		if calls > 3 {
			t.Fatal("unexpected streaming request")
		}
		return &fakeStream{events: []provider.StreamEvent{{Content: "done", FinishReason: "stop"}}}, nil
	}}
	session := newSession(t, model)
	output := &recordingStreamOutput{}
	if err := session.Turn(context.Background(), "read", output); err != nil {
		t.Fatal(err)
	}
	if err := session.Turn(context.Background(), "continue", output); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || output.done != 3 {
		t.Fatalf("stream requests=%d completed=%d, want 3", calls, output.done)
	}
}

func TestSession_ProjectMemoryCountsTowardContextBudget(t *testing.T) {
	for _, addMemory := range []bool{false, true} {
		t.Run(fmt.Sprintf("add memory=%t", addMemory), func(t *testing.T) {
			workspace := t.TempDir()
			t.Chdir(workspace)
			output := &recordingOutput{}
			calls := 0
			content := strings.Repeat("a", memory.DefaultTokenBudget*4)
			model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
				calls++
				resp := memoryAnswer()
				if calls == 1 {
					resp.Usage = &provider.Usage{TotalTokens: compaction.ContextWindow - compaction.ReservedTokens - 100}
				} else {
					if calls != 2 || hasAutoCompact(output) != addMemory {
						t.Fatalf("memory was omitted from calibrated context budget: calls=%d events=%q", calls, output.events)
					}
					want := ""
					if addMemory {
						want = content
					}
					assertProjectMemory(t, req, want)
				}
				return resp, nil
			}}
			session := newSession(t, model)
			if err := session.Turn(context.Background(), "seed", &recordingOutput{}); err != nil {
				t.Fatal(err)
			}
			if addMemory {
				putProjectMemory(t, workspace, content)
			}
			if err := session.Turn(context.Background(), "continue", output); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSession_ProjectMemorySurvivesCompactionAndRestore(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	putProjectMemory(t, workspace, "ORIGINAL PROJECT MEMORY")
	mainCalls, summaries := 0, 0
	wantMemory := "ORIGINAL PROJECT MEMORY"
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		if len(req.Tools) == 0 {
			summaries++
			for _, message := range req.Messages {
				if strings.Contains(message.Text(), "PROJECT MEMORY") || strings.Contains(message.Text(), "<project-memory>") {
					t.Fatal("project memory was sent as conversation history to summarize")
				}
			}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, fmt.Sprintf("checkpoint %d", summaries), ""), FinishReason: "stop"}}}, nil
		}
		mainCalls++
		assertProjectMemory(t, req, wantMemory)
		if mainCalls > 1 {
			index := 1
			if wantMemory != "" {
				index++
			}
			if !strings.Contains(req.Messages[index].Text(), fmt.Sprintf("checkpoint %d", summaries)) {
				t.Fatal("memory displaced the compression checkpoint")
			}
		}
		resp := memoryAnswer()
		if mainCalls <= 2 {
			resp.Usage = &provider.Usage{TotalTokens: compaction.ContextWindow - compaction.ReservedTokens + 1}
		}
		return resp, nil
	}}
	first, err := agent.OpenSessionByID(model, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	id := first.ID()
	var history []provider.Message
	for index, input := range []string{strings.Repeat("o", 200000), strings.Repeat("r", 80000), strings.Repeat("s", 80000)} {
		if index == 1 {
			wantMemory = "UPDATED PROJECT MEMORY"
			putProjectMemory(t, workspace, wantMemory)
		}
		if err := first.Turn(context.Background(), input, &recordingOutput{}); err != nil {
			t.Fatal(err)
		}
		history = append(history, provider.NewMessage(provider.RoleUser, input, ""), provider.NewMessage(provider.RoleAssistant, "done", ""))
	}
	if mainCalls != 3 || summaries != 2 {
		t.Fatalf("main requests=%d summaries=%d, want 3 and 2", mainCalls, summaries)
	}
	path, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sessionstore.Load(path)
	if err != nil || !reflect.DeepEqual(snapshot.Messages, history) || snapshot.Compaction == nil || snapshot.Compaction.FirstKeptMessageIndex != 4 {
		t.Fatalf("memory changed raw history or checkpoint indices: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	wantMemory = "RESTORED PROJECT MEMORY"
	putProjectMemory(t, workspace, wantMemory)
	restored, err := agent.OpenSessionByID(model, nil, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if err := restored.Turn(context.Background(), "continue", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	wantMemory = ""
	if err := os.Remove(filepath.Join(workspace, memory.FileName)); err != nil {
		t.Fatal(err)
	}
	if err := restored.Turn(context.Background(), "after delete", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if mainCalls != 5 || summaries != 2 {
		t.Fatalf("restore or deletion unexpectedly summarized memory: main=%d summaries=%d", mainCalls, summaries)
	}
}

type memoryCommandInput struct {
	lines []string
}

func (i *memoryCommandInput) Readline(string) (string, error) {
	if len(i.lines) == 0 {
		return "", io.EOF
	}
	line := i.lines[0]
	i.lines = i.lines[1:]
	return line, nil
}

func (i *memoryCommandInput) Close() error { return nil }

func TestSession_ManualCompactReloadsProjectMemory(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	putProjectMemory(t, workspace, "valid memory")
	calls := 0
	model := &fakeProvider{chat: func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		return memoryAnswer(), nil
	}}
	session := newSession(t, model)
	if err := session.Turn(context.Background(), strings.Repeat("o", 200000), &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if err := session.Turn(context.Background(), strings.Repeat("r", 80000), &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	putProjectMemory(t, workspace, "\xff")
	output := &recordingOutput{}
	code := session.Run(context.Background(), output, nil, time.Second, &memoryCommandInput{lines: []string{"/compact", "/exit"}})
	if code != 1 || calls != 2 || len(output.toolErrors) != 1 || !strings.Contains(output.toolErrors[0].Error(), "UTF-8") {
		t.Fatalf("manual compaction ignored invalid updated memory: code=%d calls=%d errors=%v", code, calls, output.toolErrors)
	}
}
