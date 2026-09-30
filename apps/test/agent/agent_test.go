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
	output := &recordingOutput{}
	if err := agent.Run(context.Background(), client, "run command", output); err != nil {
		t.Fatalf("run: %v", err)
	}
	// 展示层收到原始 Markdown 和工具输出,不混入 CLI 标签、换行或 ANSI 样式。
	if output.String() != "**raw-output**" {
		t.Fatalf("tool output = %q", output.String())
	}
	wantEvents := []string{"message:**正在执行**", "call:call_1", "result:**raw-output**", "tool-error", "message:# 已收到错误"}
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
	err := agent.Run(context.Background(), client, "hello", output)
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
		if err := agent.Run(ctx, client, "hello", &recordingOutput{}); !errors.Is(err, context.Canceled) {
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
			err := agent.Run(ctx, client, "run tests", output)
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
	if err := agent.Run(context.Background(), client, "分析项目结构", &recordingOutput{}); err != nil {
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
