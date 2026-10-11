package agent_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/provider"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// fakeProvider 直接返回测试安排的回复，不涉及 HTTP、认证或追踪实现。
type fakeProvider struct {
	chat func(context.Context, provider.ChatRequest) (*provider.ChatResponse, error)
}

func (f *fakeProvider) Chat(ctx context.Context, request provider.ChatRequest) (*provider.ChatResponse, error) {
	return f.chat(ctx, request)
}

var _ provider.Provider = (*fakeProvider)(nil)

type tracedFakeProvider struct {
	fakeProvider
	tracer trace.Tracer
}

func (f *tracedFakeProvider) Tracer() trace.Tracer { return f.tracer }

func TestSession_UsesOptionalProviderTracer(t *testing.T) {
	tracerProvider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	model := &tracedFakeProvider{
		tracer: tracerProvider.Tracer("fake"),
		fakeProvider: fakeProvider{chat: func(ctx context.Context, request provider.ChatRequest) (*provider.ChatResponse, error) {
			if !trace.SpanFromContext(ctx).SpanContext().IsValid() {
				t.Fatal("provider did not receive the traced turn context")
			}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "done", "")}}}, nil
		}},
	}
	if err := newSession(t, model).Turn(context.Background(), "hello", &recordingOutput{}); err != nil {
		t.Fatal(err)
	}
}

func TestSession_FakeProviderToolCallsAndHistory(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	if err := os.WriteFile("main.go", []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := provider.ToolCall{
		ID: "read-main", Type: provider.ToolTypeFunction,
		Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"main.go"}`},
	}
	assistant := provider.NewMessage(provider.RoleAssistant, "先读取文件。", "")
	assistant.ToolCalls = []provider.ToolCall{call}
	calls := 0
	model := &fakeProvider{chat: func(ctx context.Context, request provider.ChatRequest) (*provider.ChatResponse, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		calls++
		message := provider.NewMessage(provider.RoleAssistant, "文件已读取", "")
		switch calls {
		case 1:
			if len(request.Messages) != 2 || request.Messages[0].Role != provider.RoleSystem || !strings.Contains(request.Messages[0].Text(), workspace) || request.Messages[1].Text() != "读取文件" || len(request.Tools) != 4 {
				t.Fatalf("initial request = %+v", request)
			}
			message = assistant
		case 2:
			if len(request.Messages) != 4 || !reflect.DeepEqual(request.Messages[2], assistant) {
				t.Fatalf("tool request history = %+v", request.Messages)
			}
			result := request.Messages[3]
			if result.Role != provider.RoleTool || result.ToolCallID != call.ID || !strings.Contains(result.Text(), "package main") {
				t.Fatalf("tool result = %+v", result)
			}
		case 3:
			if len(request.Messages) != 6 || request.Messages[4].Text() != "文件已读取" || request.Messages[5].Text() != "继续" {
				t.Fatalf("continued history = %+v", request.Messages)
			}
			message = provider.NewMessage(provider.RoleAssistant, "继续处理", "")
		default:
			t.Fatalf("unexpected model call %d", calls)
		}
		return &provider.ChatResponse{
			Choices: []provider.Choice{{Message: message}},
			Usage:   &provider.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
		}, nil
	}}
	session := newSession(t, model)
	output := &recordingOutput{}
	if err := session.Turn(context.Background(), "读取文件", output); err != nil {
		t.Fatal(err)
	}
	if err := session.Turn(context.Background(), "继续", output); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || !reflect.DeepEqual(output.events, []string{"message:文件已读取", "message:继续处理"}) {
		t.Fatalf("calls = %d, output = %+v", calls, output.events)
	}
	wantProcess := []string{
		"model-start:1", "tool-start:1:read", "tool-done:read:completed", "model-start:2", "stats:2:1:6",
		"model-start:1", "stats:1:0:3",
	}
	if !reflect.DeepEqual(output.processEvents, wantProcess) {
		t.Fatalf("process events = %q, want %q", output.processEvents, wantProcess)
	}
}

func TestSession_FakeProviderErrorsAndRecovery(t *testing.T) {
	for _, cause := range []error{
		&provider.APIError{StatusCode: 429, Code: "rate_limit", Message: "try later"},
		fmt.Errorf("fake request: %w", context.Canceled),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			calls := 0
			model := &fakeProvider{chat: func(ctx context.Context, request provider.ChatRequest) (*provider.ChatResponse, error) {
				calls++
				if calls == 1 {
					return nil, cause
				}
				if len(request.Messages) != 3 || request.Messages[1].Text() != "第一次" || request.Messages[2].Text() != "再试一次" {
					t.Fatalf("history after failed call = %+v", request.Messages)
				}
				return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "成功", "")}}}, nil
			}}
			session := newSession(t, model)
			output := &recordingOutput{}
			if err := session.Turn(context.Background(), "第一次", output); !errors.Is(err, cause) {
				t.Fatalf("error = %v, want %v", err, cause)
			}
			if len(output.events) != 0 {
				t.Fatalf("failed provider generated output: %+v", output.events)
			}
			if err := session.Turn(context.Background(), "再试一次", output); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(output.events, []string{"message:成功"}) {
				t.Fatalf("recovery output = %+v", output.events)
			}
		})
	}
}

func TestSession_FakeProviderTurnLimitSummary(t *testing.T) {
	calls := 0
	model := &fakeProvider{chat: func(ctx context.Context, request provider.ChatRequest) (*provider.ChatResponse, error) {
		if request.MaxTokens != 8192 {
			t.Fatalf("max tokens = %d, want 8192", request.MaxTokens)
		}
		calls++
		if calls <= 500 {
			call := provider.ToolCall{
				ID: "repeat", Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{Name: "unknown", Arguments: `{}`},
			}
			return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}}}}, nil
		}
		if len(request.Tools) != 0 || len(request.Messages) != 1003 {
			t.Fatalf("summary request = %+v", request)
		}
		if last := request.Messages[len(request.Messages)-1]; last.Role != provider.RoleSystem || !strings.Contains(last.Text(), "停止调用工具") {
			t.Fatalf("summary instruction = %+v", last)
		}
		return &provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "已停止执行", "")}}}, nil
	}}
	output := &recordingOutput{}
	err := newSession(t, model).Turn(context.Background(), "执行", output)
	if err == nil || !strings.Contains(err.Error(), "maximum model turns (500)") || calls != 501 || !reflect.DeepEqual(output.events, []string{"message:已停止执行"}) {
		t.Fatalf("error = %v, calls = %d, output = %+v", err, calls, output.events)
	}
}

func TestNewSession_RejectsNilProvider(t *testing.T) {
	for _, model := range []provider.Provider{nil, (*fakeProvider)(nil), (*provider.Client)(nil)} {
		session, err := agent.NewSession(model, nil)
		if session != nil {
			_ = session.Close()
			t.Fatal("nil provider created a session")
		}
		if err == nil || !strings.Contains(err.Error(), "nil provider") {
			t.Fatalf("error = %v, want nil provider", err)
		}
	}
}
