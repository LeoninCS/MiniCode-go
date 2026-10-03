package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestRun_OutputAndToolErrorRecovery(t *testing.T) {
	call := provider.ToolCall{
		ID: "call_1", Type: provider.ToolTypeFunction,
		Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf '**raw-output**'; exit 7"}`},
	}
	assistant := provider.NewMessage(provider.RoleAssistant, "**正在执行**", "")
	assistant.ToolCalls = []provider.ToolCall{call}
	replies := []provider.Message{assistant, provider.NewMessage(provider.RoleAssistant, "# 已收到错误", "")}
	requests := make(chan provider.ChatRequest, len(replies))
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		index := int(count.Add(1)) - 1
		if index >= len(replies) {
			t.Error("unexpected model request")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		requests <- req
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: replies[index]}}})
	}))
	defer srv.Close()

	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
	output := &runningRecordingOutput{}
	if err := newSession(t, client).Turn(context.Background(), "run command", output); err != nil {
		t.Fatalf("run: %v", err)
	}
	// 展示层收到原始 Markdown 和工具输出,不混入 CLI 标签、换行或 ANSI 样式。
	if output.String() != "**raw-output**" {
		t.Fatalf("tool output = %q", output.String())
	}
	wantEvents := []string{"begin-running", "message:**正在执行**", "call:call_1", "result:**raw-output**", "tool-error", "clear-running", "message:# 已收到错误"}
	if !reflect.DeepEqual(output.events, wantEvents) {
		t.Fatalf("events = %q, want %q", output.events, wantEvents)
	}
	if len(output.toolErrors) != 1 || !strings.Contains(output.toolErrors[0].Error(), "exit status 7") {
		t.Fatalf("tool errors = %v", output.toolErrors)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d", len(requests))
	}
	<-requests
	next := <-requests
	if len(next.Messages) != 4 || !reflect.DeepEqual(next.Messages[2], assistant) {
		t.Fatalf("messages = %+v", next.Messages)
	}
	result := next.Messages[3]
	wantResult := "**raw-output**\nerror: " + output.toolErrors[0].Error()
	if result.Role != provider.RoleTool || result.ToolCallID != call.ID || result.Text() != wantResult {
		t.Fatalf("tool result = %+v", result)
	}
}

func TestRun_ReturnsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"bad key"}}`)
	}))
	defer srv.Close()
	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
	output := &recordingOutput{}
	err := newSession(t, client).Turn(context.Background(), "hello", output)
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %v, want provider API error", err)
	}
	if len(output.events) != 0 || output.Len() != 0 {
		t.Fatalf("fatal error should be returned to caller, events = %q, output = %q", output.events, output.String())
	}

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := newSession(t, client).Turn(ctx, "hello", &recordingOutput{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestRun_TurnLimitSummaryFailure(t *testing.T) {
	for _, cancelSummary := range []bool{false, true} {
		name := "API failure"
		if cancelSummary {
			name = "context cancellation"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var count atomic.Int32
			call := provider.ToolCall{ID: "again", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":":"}`}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if count.Add(1) <= 500 {
					message := provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}
					_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: message}}})
					return
				}
				if cancelSummary {
					cancel()
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"code":"summary_unavailable","message":"try later"}}`)
			}))
			defer srv.Close()
			client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
			output := &recordingOutput{}
			err := newSession(t, client).Turn(ctx, "run tests", output)
			if err == nil || !strings.Contains(err.Error(), "maximum model turns (500)") || count.Load() != 501 {
				t.Fatalf("error = %v, requests = %d", err, count.Load())
			}
			if cancelSummary {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
			} else {
				var apiErr *provider.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("error = %v, want provider API error", err)
				}
			}
			if len(output.events) == 0 || !strings.HasPrefix(output.events[len(output.events)-1], "message:已达到最大执行轮数（500）") {
				t.Fatalf("missing fallback content: %q", output.events)
			}
		})
	}
}

func TestRun_SystemPromptDeclaresWorkspaceAndToolRules(t *testing.T) {
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	requests := make(chan provider.ChatRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		requests <- req
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", "")}}})
	}))
	defer srv.Close()

	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
	if err := newSession(t, client).Turn(context.Background(), "分析项目结构", &recordingOutput{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	req := <-requests
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	system := req.Messages[0]
	if system.Role != provider.RoleSystem {
		t.Fatalf("first message role = %q, want system", system.Role)
	}
	if user := req.Messages[1]; user.Role != provider.RoleUser || user.Text() != "分析项目结构" {
		t.Fatalf("user message = %+v", user)
	}
	// 工具规则要覆盖实际注册的四个工具,否则模型会漏用可用工具或尝试不存在的工具。
	content := system.Text()
	for _, name := range []string{"read", "write", "edit", "bash"} {
		if !strings.Contains(content, name) {
			t.Errorf("system prompt does not mention tool %q", name)
		}
	}
	if !strings.Contains(content, workspace) {
		t.Errorf("system prompt does not declare workspace %q", workspace)
	}
	// 轮数上限由宿主强制,Prompt 里的数值必须和 maxTurns 保持一致。
	if !strings.Contains(content, "500 轮") {
		t.Errorf("system prompt does not state the turn limit: %s", content)
	}
}

// TestSession_TurnsShareHistory 校验历史跨轮累积:
// 第二轮请求必须带上第一轮的问答,否则模型记不住用户前面说过什么。
func TestSession_TurnsShareHistory(t *testing.T) {
	replies := []provider.Message{
		provider.NewMessage(provider.RoleAssistant, "第一轮回答", ""),
		provider.NewMessage(provider.RoleAssistant, "第二轮回答", ""),
	}
	requests := make(chan provider.ChatRequest, len(replies))
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		index := int(count.Add(1)) - 1
		requests <- req
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: replies[index]}}})
	}))
	defer srv.Close()

	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
	session := newSession(t, client)
	if err := session.Turn(context.Background(), "我叫什么", &recordingOutput{}); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if err := session.Turn(context.Background(), "我是谁", &recordingOutput{}); err != nil {
		t.Fatalf("second turn: %v", err)
	}

	first := <-requests
	second := <-requests
	if len(first.Messages) != 2 || first.Messages[1].Text() != "我叫什么" {
		t.Fatalf("first request = %+v", first.Messages)
	}
	if !reflect.DeepEqual(second.Messages[0], first.Messages[0]) {
		t.Fatal("system prompt was not kept at the head of the history")
	}
	want := []string{"我叫什么", "第一轮回答", "我是谁"}
	if len(second.Messages) != len(want)+1 {
		t.Fatalf("second request = %+v", second.Messages)
	}
	for i, text := range want {
		if got := second.Messages[1+i].Text(); got != text {
			t.Fatalf("second.Messages[%d] = %q, want %q", 1+i, got, text)
		}
	}
}

func TestSession_ToolApproval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tool       string
		arguments  string
		approved   bool
		wantResult string
		wantOutput string
	}{
		{name: "approved bash executes", tool: "bash", arguments: `{"command":"printf approved"}`, approved: true, wantResult: "approved", wantOutput: "approved"},
		{name: "denied bash is returned to model", tool: "bash", arguments: `{"command":"printf approved"}`, approved: false, wantResult: "tool execution denied by user"},
		{name: "denied write is returned to model", tool: "write", arguments: `{"path":"must-not-exist","content":"no"}`, approved: false, wantResult: "tool execution denied by user"},
		{name: "denied edit is returned to model", tool: "edit", arguments: `{"path":"must-not-exist","old_text":"a","new_text":"b"}`, approved: false, wantResult: "tool execution denied by user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := provider.ToolCall{ID: "approval", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: tc.tool, Arguments: tc.arguments}}
			requests := make(chan provider.ChatRequest, 2)
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req provider.ChatRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				requests <- req
				message := provider.NewMessage(provider.RoleAssistant, "done", "")
				if count.Add(1) == 1 {
					message = provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}
				}
				_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: message}}})
			}))
			defer srv.Close()

			var approvals int
			approver := agent.ToolApproverFunc(func(ctx context.Context, got provider.ToolCall) (bool, error) {
				approvals++
				if got != call {
					t.Fatalf("approval call = %+v", got)
				}
				return tc.approved, nil
			})
			client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
			session, err := agent.NewSession(client, approver)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			output := &recordingOutput{}
			if err := session.Turn(context.Background(), "run", output); err != nil {
				t.Fatal(err)
			}
			if approvals != 1 || output.String() != tc.wantOutput {
				t.Fatalf("approvals = %d, output = %q", approvals, output.String())
			}
			<-requests
			next := <-requests
			if got := next.Messages[3].Text(); got != tc.wantResult {
				t.Fatalf("tool result = %q, want %q", got, tc.wantResult)
			}
		})
	}
}

func TestSession_ReadDoesNotRequireApproval(t *testing.T) {
	call := provider.ToolCall{ID: "read", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"missing"}`}}
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		message := provider.NewMessage(provider.RoleAssistant, "done", "")
		if count.Add(1) == 1 {
			message = provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}
		}
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: message}}})
	}))
	defer srv.Close()
	approver := agent.ToolApproverFunc(func(context.Context, provider.ToolCall) (bool, error) {
		t.Fatal("read unexpectedly requested approval")
		return false, nil
	})
	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "test-key", Model: "test-model"})
	session, err := agent.NewSession(client, approver)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if err := session.Turn(context.Background(), "read", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
}

// newSession 打开一个会话并在用例结束时释放工作区,让用例只关心单轮行为。
func newSession(t *testing.T, client *provider.Client) *agent.Session {
	t.Helper()
	session, err := agent.NewSession(client, nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type recordingOutput struct {
	bytes.Buffer
	events     []string
	toolErrors []error
}

func (o *recordingOutput) Message(content string) {
	o.events = append(o.events, "message:"+content)
}

func (o *recordingOutput) ToolCall(call provider.ToolCall) {
	o.events = append(o.events, "call:"+call.ID)
}

func (o *recordingOutput) ToolResult(result string) {
	o.events = append(o.events, "result:"+result)
}

func (o *recordingOutput) ToolError(err error) {
	o.events = append(o.events, "tool-error")
	o.toolErrors = append(o.toolErrors, err)
}

// runningRecordingOutput 验证支持生命周期接口的展示层会在最终回答前收到清理通知。
type runningRecordingOutput struct {
	recordingOutput
}

func (o *runningRecordingOutput) BeginRunning() {
	o.events = append(o.events, "begin-running")
}

func (o *runningRecordingOutput) ClearRunning() {
	o.events = append(o.events, "clear-running")
}
