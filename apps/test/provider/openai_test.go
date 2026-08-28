package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *provider.Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, provider.NewClient(provider.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
}

func TestClient_Chat_Success(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("unexpected content type: %s", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req provider.ChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("expected model test-model, got %s", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != provider.RoleUser || req.Messages[0].Content != "hi" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{
			ID:    "chatcmpl-1",
			Model: "test-model",
			Choices: []provider.Choice{{
				Index: 0,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: "hello back",
				},
				FinishReason: "stop",
			}},
			Usage: provider.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		})
	})

	resp, err := client.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if got := resp.FirstContent(); got != "hello back" {
		t.Errorf("unexpected content: %q", got)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestClient_Chat_RequestModelOverride(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Model != "override-model" {
			t.Errorf("expected model override, got %s", req.Model)
		}
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{
			Choices: []provider.Choice{{Message: provider.Message{Content: "ok"}}},
		})
	})

	if _, err := client.Chat(context.Background(), provider.ChatRequest{
		Model:    "override-model",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("chat: %v", err)
	}
}

func TestClient_Chat_APIError(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(provider.APIError{
			Code:    "invalid_api_key",
			Message: "Incorrect API key provided",
			Type:    "authentication_error",
		})
	})

	_, err := client.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", apiErr.StatusCode)
	}
	if apiErr.Code != "invalid_api_key" {
		t.Errorf("unexpected code: %s", apiErr.Code)
	}
}

// TestClient_Chat_APIError_OpenAIEnvelope 验证 OpenAI 实际错误格式
// (错误对象嵌套在 "error" 字段下)也能被解析为 APIError。
func TestClient_Chat_APIError_OpenAIEnvelope(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_api_key","message":"Incorrect API key provided","type":"authentication_error"}}`))
	})

	_, err := client.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "invalid_api_key" {
		t.Errorf("unexpected code: %s", apiErr.Code)
	}
	if apiErr.Type != "authentication_error" {
		t.Errorf("unexpected type: %s", apiErr.Type)
	}
}

func TestClient_Chat_NonJSONError(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	})

	_, err := client.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("expected status 502 in error, got %v", err)
	}
}

func TestClient_Chat_ContextCanceled(t *testing.T) {
	// 客户端 50ms 后会因 ctx 超时断开连接;
	// server handler 用一个略长的 server-side 超时保证必然退出,
	// 避免 srv.Close() 等待活连接。
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := provider.NewClient(provider.Config{
		BaseURL: srv.URL,
		APIKey:  "k",
		Model:   "m",
	}).Chat(ctx, provider.ChatRequest{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("server handler did not exit")
	}
}

func TestClient_Chat_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     provider.Config
		req     provider.ChatRequest
		wantSub string
	}{
		{
			name:    "empty api key",
			cfg:     provider.Config{BaseURL: "http://example.com", Model: "m"},
			req:     provider.ChatRequest{Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}}},
			wantSub: "empty API key",
		},
		{
			name:    "empty messages",
			cfg:     provider.Config{BaseURL: "http://example.com", APIKey: "k", Model: "m"},
			req:     provider.ChatRequest{},
			wantSub: "empty messages",
		},
		{
			name:    "empty model",
			cfg:     provider.Config{BaseURL: "http://example.com", APIKey: "k"},
			req:     provider.ChatRequest{Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}}},
			wantSub: "empty model",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := provider.NewClient(tc.cfg).Chat(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("expected error to contain %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestClient_Chat_StripsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Content: "ok"}}}})
	}))
	defer srv.Close()

	client := provider.NewClient(provider.Config{BaseURL: srv.URL + "/", APIKey: "k", Model: "m"})
	if _, err := client.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("expected path /chat/completions, got %s", gotPath)
	}
}

func TestAPIError_Error(t *testing.T) {
	e := &provider.APIError{Code: "x", Message: "boom"}
	if got := e.Error(); !strings.Contains(got, "x") || !strings.Contains(got, "boom") {
		t.Errorf("unexpected error string: %s", got)
	}
	var nilErr *provider.APIError
	if got := nilErr.Error(); got != "" {
		t.Errorf("expected empty string for nil APIError, got %q", got)
	}
}

func TestChatResponse_FirstContent_Empty(t *testing.T) {
	if got := (*provider.ChatResponse)(nil).FirstContent(); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
	if got := (&provider.ChatResponse{}).FirstContent(); got != "" {
		t.Errorf("expected empty for no choices, got %q", got)
	}
}
