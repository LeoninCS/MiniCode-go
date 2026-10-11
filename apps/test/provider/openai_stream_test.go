package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/provider"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func streamRequest() provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "hi", "")}}
}

func TestChatStream_DeliversBeforeResponseEndsAndKeepsFinalUsage(t *testing.T) {
	release := make(chan struct{})
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("unexpected request: %s %s %v", r.Method, r.URL, r.Header)
		}
		var req struct {
			provider.ChatRequest
			Stream  bool `json:"stream"`
			Options struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Stream || !req.Options.IncludeUsage || req.Model != "override" || req.MaxTokens != 128 || len(req.Tools) != 1 || len(req.Messages) != 1 {
			t.Errorf("request = %+v, error = %v", req, err)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, ": keepalive\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"世界\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := streamRequest()
	req.Model, req.MaxTokens = "override", 128
	req.Tools = []provider.Tool{{Type: provider.ToolTypeFunction, Function: provider.FunctionDefinition{Name: "read"}}}
	stream, err := client.ChatStream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil || first.Content != "你好" {
		t.Fatalf("first = %+v, error = %v", first, err)
	}
	close(release) // 未允许服务结束前已读到首个增量。
	second, err := stream.Recv()
	if err != nil || second.Content != "世界" || second.FinishReason != "stop" {
		t.Fatalf("second = %+v, error = %v", second, err)
	}
	usage, err := stream.Recv()
	if err != nil || usage.Usage == nil || usage.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, error = %v", usage, err)
	}
	for range 2 {
		if _, err := stream.Recv(); err != io.EOF {
			t.Fatalf("end = %v", err)
		}
	}
}

func TestChatStream_SSEFramingAndLargeEvent(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run("line-ending="+ending, func(t *testing.T) {
			text := strings.Repeat("中", 30000) // 超过 Scanner 默认的 64 KiB 限制。
			encoded, _ := json.Marshal(text)
			payload := "\ufeff: comment\n\nid: ignored\nretry: 100\nevent: message\ndata: {\"choices\":\ndata: [{\"index\":0,\"delta\":{\"content\":" + string(encoded) + "}}]}\n\ndata:\n\nevent: ping\ndata: heartbeat\n\ndata: [DONE]\n\n"
			payload = strings.ReplaceAll(payload, "\n", ending)
			_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < len(payload); i += 511 {
					end := min(i+511, len(payload))
					_, _ = io.WriteString(w, payload[i:end])
					w.(http.Flusher).Flush()
				}
			})
			stream, err := client.ChatStream(context.Background(), streamRequest())
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			event, err := stream.Recv()
			if err != nil || event.Content != text {
				t.Fatalf("content length = %d, error = %v", len(event.Content), err)
			}
			if _, err := stream.Recv(); err != io.EOF {
				t.Fatalf("end = %v", err)
			}
		})
	}
}

func TestChatStream_DeliversToolFragments(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
	})
	stream, err := client.ChatStream(context.Background(), streamRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Recv()
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call-1" || first.ToolCalls[0].Function.Arguments != `{"path":` {
		t.Fatalf("first = %+v, error = %v", first, err)
	}
	second, err := stream.Recv()
	if err != nil || second.FinishReason != "tool_calls" || len(second.ToolCalls) != 1 || second.ToolCalls[0].Function.Arguments != `"main.go"}` {
		t.Fatalf("second = %+v, error = %v", second, err)
	}
}

func TestChatStream_RejectsInvalidOrInterruptedEvents(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		api              bool
		unexpectedEOF    bool
	}{
		{name: "wrapped error", body: `data: {"error":{"code":"overloaded","message":"retry later"}}` + "\n\n", want: "overloaded", api: true},
		{name: "named error", body: "event: error\ndata: server failed\n\n", want: "server failed", api: true},
		{name: "invalid JSON", body: "data: {bad}\n\n", want: "decode stream event"},
		{name: "missing DONE", body: `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n", unexpectedEOF: true},
		{name: "partial event", body: `data: {"choices":`, unexpectedEOF: true},
		{name: "multiple choices", body: `data: {"choices":[{"index":0},{"index":1}]}` + "\n\n", want: "single choice"},
		{name: "missing tool index", body: `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"function":{}}]}}]}` + "\n\n", want: "tool call index"},
		{name: "oversized event", body: "data: " + strings.Repeat("x", 4*1024*1024) + "\n\n", want: "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.body)
			})
			stream, err := client.ChatStream(context.Background(), streamRequest())
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for err == nil {
				_, err = stream.Recv()
			}
			if err == io.EOF || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
			if tc.unexpectedEOF && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("expected unexpected EOF: %v", err)
			}
			var apiErr *provider.APIError
			if tc.api && !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError: %v", err)
			}
			if _, next := stream.Recv(); next != err {
				t.Fatalf("terminal error changed: %v -> %v", err, next)
			}
		})
	}
}

func TestChatStream_RequestErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType, want string
		status                        int
	}{
		{name: "HTTP error", status: http.StatusUnauthorized, body: `{"error":{"code":"bad_key","message":"invalid key"}}`, contentType: "application/json", want: "bad_key"},
		{name: "proxy error", status: http.StatusBadGateway, body: "bad gateway", want: "502"},
		{name: "JSON instead of SSE", status: http.StatusOK, body: "{}", contentType: "application/json", want: "text/event-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			stream, err := client.ChatStream(context.Background(), streamRequest())
			if stream != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("stream = %v, error = %v", stream, err)
			}
			if tc.status != http.StatusOK {
				var apiErr *provider.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
					t.Fatalf("API error = %v", err)
				}
			}
		})
	}
}

func TestChatStream_CancelAndCloseUnblockRecv(t *testing.T) {
	for _, action := range []string{"cancel", "deadline", "close"} {
		t.Run(action, func(t *testing.T) {
			canceled := make(chan struct{})
			_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(canceled)
			})
			ctx, cancel := context.WithCancel(context.Background())
			if action == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			}
			defer cancel()
			stream, err := client.ChatStream(ctx, streamRequest())
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			received := make(chan error, 1)
			go func() { _, err := stream.Recv(); received <- err }()
			want := error(context.Canceled)
			switch action {
			case "cancel":
				cancel()
			case "deadline":
				want = context.DeadlineExceeded
			case "close":
				want = io.ErrClosedPipe
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-received:
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Recv remained blocked")
			}
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("request was not canceled")
			}
		})
	}
}

func TestChatStream_RecordsGenerationThroughUsageAndDone(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
	srv, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"reply-id","model":"returned-model","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`+"\n\ndata: [DONE]\n\n")
	})
	client := provider.NewClient(provider.Config{BaseURL: srv.URL, APIKey: "key", Model: "model", Tracer: tracer.Tracer("test")})
	stream, err := client.ChatStream(context.Background(), streamRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if len(recorder.Ended()) != 0 {
		t.Fatal("generation ended before reading")
	}
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Status().Code == codes.Error {
		t.Fatalf("spans = %+v", spans)
	}
	attrs := make(map[string]string)
	for _, attr := range spans[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}
	if attrs["gen_ai.response.id"] != "reply-id" || attrs["gen_ai.usage.total_tokens"] != "5" || !strings.Contains(attrs["langfuse.observation.output"], "done") {
		t.Fatalf("attributes = %v", attrs)
	}
}
