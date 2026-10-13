package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/memory"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func putMemoryDetail(t *testing.T, workspace, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(workspace, filepath.Dir(path)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoDetailContent(t *testing.T, req provider.ChatRequest, markers ...string) {
	t.Helper()
	for _, message := range req.Messages {
		for _, marker := range markers {
			if strings.Contains(message.Text(), marker) {
				t.Fatalf("unread detail %q entered context", marker)
			}
		}
	}
}

func TestSession_OnDemandMemoryReadCompactionAndRestore(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	path := memory.DetailDir + "/session.md"
	index := "# 常驻约定\n使用 Go。\n\n## 按需目录\n" +
		"- [会话恢复](" + path + ")：修改 Session 保存或恢复时参考。\n" +
		"- [发布](" + memory.DetailDir + "/release.md)：发布服务时参考。\n" +
		"- [旧资料](" + memory.DetailDir + "/missing.md)：仅用于旧版本排查。\n"
	putProjectMemory(t, workspace, index)
	oldDetail := "SESSION_DETAIL_OLD\n" + strings.Repeat("a", memory.DefaultTokenBudget*4+1)
	newDetail := "SESSION_DETAIL_CURRENT\n当前 Session 使用独立压缩检查点。\n"
	putMemoryDetail(t, workspace, path, oldDetail)
	// 未选择的超限文件和缺失目录项不能阻断会话，也不能被整库加载。
	putMemoryDetail(t, workspace, memory.DetailDir+"/release.md", "UNSELECTED_RELEASE_DETAIL\n"+strings.Repeat("b", memory.MaxFileBytes+1))
	mainCalls, summaries := 0, 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		if len(req.Tools) == 0 {
			summaries++
			found := false
			for _, message := range req.Messages {
				found = found || strings.Contains(message.Text(), "SESSION_DETAIL_OLD")
				if strings.Contains(message.Text(), "<project-memory>") {
					t.Fatal("resident directory was included as raw history in compaction")
				}
			}
			if !found {
				t.Fatal("explicitly read detail was omitted from conversation summary input")
			}
			assertNoDetailContent(t, req, "UNSELECTED_RELEASE_DETAIL")
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "已读取会话资料；需要精确信息时重新读取当前详情。", ""), FinishReason: "stop"}}}, nil
		}
		mainCalls++
		assertProjectMemory(t, req, index)
		assertNoDetailContent(t, req, "UNSELECTED_RELEASE_DETAIL")
		if !strings.Contains(req.Messages[0].Text(), memory.DetailDir) {
			t.Fatal("system prompt did not tell the model where to read details")
		}
		switch mainCalls {
		case 1, 4:
			assertNoDetailContent(t, req, "SESSION_DETAIL_OLD", "SESSION_DETAIL_CURRENT")
			return memoryToolResponse(memoryToolCall(t, "read-detail", "read", map[string]any{"path": path})), nil
		case 2, 5:
			want := oldDetail
			if mainCalls == 5 {
				want = newDetail
			}
			last := req.Messages[len(req.Messages)-1]
			if last.Role != provider.RoleTool || last.ToolCallID != "read-detail" || !strings.Contains(last.Text(), strings.Split(want, "\n")[0]) || strings.Contains(last.Text(), "error:") {
				t.Fatalf("selected detail was not returned as a tool result: %+v", last)
			}
		case 3:
			// 已读取的正文正常留在同一会话历史中，并非永久注入项。
			found := false
			for _, message := range req.Messages {
				found = found || strings.Contains(message.Text(), "SESSION_DETAIL_OLD")
			}
			if !found {
				t.Fatal("read detail disappeared from raw conversation history")
			}
		case 6:
			assertNoDetailContent(t, req, "SESSION_DETAIL_OLD", "SESSION_DETAIL_CURRENT")
		default:
			t.Fatal("unexpected on-demand memory request")
		}
		return memoryAnswer(), nil
	}}
	approver := agent.ToolApproverFunc(func(context.Context, string, provider.ToolCall) (bool, error) {
		t.Fatal("reading a detail requested mutation approval")
		return false, nil
	})
	first, err := agent.OpenSessionByID(model, approver, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	id := first.ID()
	if err := first.Turn(context.Background(), strings.Repeat("o", 200000)+"分析会话恢复", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if err := first.Turn(context.Background(), strings.Repeat("r", 80000), &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	output := &recordingOutput{}
	if code := first.Run(context.Background(), output, nil, time.Second, &memoryCommandInput{lines: []string{"/compact", "/exit"}}); code != 0 || summaries != 1 {
		t.Fatalf("manual compaction: code=%d summaries=%d errors=%v", code, summaries, output.toolErrors)
	}
	storedPath, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sessionstore.Load(storedPath)
	if err != nil || snapshot.Compaction == nil || snapshot.Compaction.FirstKeptMessageIndex < 4 {
		t.Fatalf("read detail was not compacted with raw history: %+v, %v", snapshot.Compaction, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	putMemoryDetail(t, workspace, path, newDetail)
	restored, err := agent.OpenSessionByID(model, approver, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if err := restored.Turn(context.Background(), "重新核对当前会话恢复资料", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	fresh := newSession(t, model)
	if err := fresh.Turn(context.Background(), "你好", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if mainCalls != 6 || summaries != 1 {
		t.Fatalf("main requests=%d summaries=%d, want 6 and 1", mainCalls, summaries)
	}
}

func TestSession_OnDemandMemoryCreateUpdateAndForget(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	path := memory.DetailDir + "/testing.md"
	resident := "# 常驻约定\n使用 Go。\n"
	entry := "- [测试](" + path + ")：修改测试时参考。\n"
	updatedEntry := "- [测试](" + path + ")：修改测试或验证代码时参考。\n"
	index, updatedIndex := resident+entry, resident+updatedEntry
	tool := func(id, name, file string, args map[string]any) provider.ToolCall {
		args["path"] = file
		return memoryToolCall(t, id, name, args)
	}
	readRoot := tool("read-root", "read", memory.FileName, map[string]any{})
	readDetail := tool("read-detail", "read", path, map[string]any{})
	plan := []struct {
		memory string
		calls  []provider.ToolCall
	}{
		{calls: []provider.ToolCall{readRoot, readDetail}},
		{calls: []provider.ToolCall{tool("create-detail", "write", path, map[string]any{"content": "测试使用 go test。\n"})}},
		{calls: []provider.ToolCall{tool("create-index", "write", memory.FileName, map[string]any{"content": index})}},
		{memory: index},
		{memory: index, calls: []provider.ToolCall{readRoot, readDetail}},
		{memory: index, calls: []provider.ToolCall{tool("update-detail", "edit", path, map[string]any{"old_text": "go test", "new_text": "go test 和 go vet"})}},
		{memory: index, calls: []provider.ToolCall{tool("update-index", "edit", memory.FileName, map[string]any{"old_text": entry, "new_text": updatedEntry})}},
		{memory: updatedIndex},
		{memory: updatedIndex, calls: []provider.ToolCall{readRoot, readDetail}},
		{memory: updatedIndex, calls: []provider.ToolCall{tool("clear-detail", "write", path, map[string]any{"content": ""})}},
		{memory: updatedIndex, calls: []provider.ToolCall{tool("remove-index", "edit", memory.FileName, map[string]any{"old_text": updatedEntry, "new_text": ""})}},
		{memory: resident},
		{memory: resident},
	}
	requests := 0
	model := &fakeProvider{chat: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		if requests >= len(plan) {
			t.Fatal("unexpected detail mutation request")
		}
		step := plan[requests]
		requests++
		assertProjectMemory(t, req, step.memory)
		if requests > 2 && requests <= 12 {
			last := req.Messages[len(req.Messages)-1]
			if last.Role == provider.RoleTool && strings.Contains(last.Text(), "error:") {
				t.Fatalf("memory maintenance failed: %+v", last)
			}
		}
		if len(step.calls) > 0 {
			return memoryToolResponse(step.calls...), nil
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
	if err := first.Turn(context.Background(), "把测试方法保存为按需记忆", &recordingOutput{}); err != nil {
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
	if err := restored.Turn(context.Background(), "更新测试记忆，同时加入 go vet", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, path)); err != nil || string(got) != "测试使用 go test 和 go vet。\n" {
		t.Fatalf("updated detail = %q, %v", got, err)
	}
	if err := restored.Turn(context.Background(), "忘记测试专题，保留常驻约定", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, path)); err != nil || len(got) != 0 {
		t.Fatalf("forgotten detail = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, memory.FileName)); err != nil || string(got) != resident {
		t.Fatalf("remaining resident memory = %q, %v", got, err)
	}
	fresh := newSession(t, model)
	if err := fresh.Turn(context.Background(), "继续工作", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
	wantApprovals := []string{"create-detail", "create-index", "update-detail", "update-index", "clear-detail", "remove-index"}
	if requests != len(plan) || !reflect.DeepEqual(approvals, wantApprovals) {
		t.Fatalf("requests=%d approvals=%v", requests, approvals)
	}
}
