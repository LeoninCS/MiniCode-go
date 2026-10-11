package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestNew_UsesConfiguredProviderAndModel(t *testing.T) {
	for _, name := range []string{"", provider.OpenAI} {
		t.Run("provider="+name, func(t *testing.T) {
			requests := make(chan provider.ChatRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var received provider.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				requests <- received
				_ = json.NewEncoder(w).Encode(provider.ChatResponse{
					Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "hello", "")}},
					Usage:   &provider.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
				})
			}))
			defer srv.Close()
			model, err := provider.New(provider.Config{
				Provider: name, BaseURL: srv.URL, APIKey: "key", Model: "configured-model",
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err := model.Chat(context.Background(), provider.ChatRequest{
				Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "hi", "")},
			})
			if err != nil || response.Content() != "hello" || response.Usage == nil || response.Usage.TotalTokens != 5 {
				t.Fatalf("response = %+v, error = %v", response, err)
			}
			received := <-requests
			if received.Model != "configured-model" || received.MaxTokens != 0 {
				t.Fatalf("configured request = %+v", received)
			}
		})
	}
}

func TestNew_RejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*provider.Config)
		want   string
	}{
		{name: "unknown provider", change: func(c *provider.Config) { c.Provider = "unknown" }, want: "unsupported provider"},
		{name: "missing key", change: func(c *provider.Config) { c.APIKey = " " }, want: "empty API key"},
		{name: "missing model", change: func(c *provider.Config) { c.Model = "" }, want: "empty model"},
		{name: "relative URL", change: func(c *provider.Config) { c.BaseURL = "/v1" }, want: "absolute HTTP or HTTPS URL"},
		{name: "invalid scheme", change: func(c *provider.Config) { c.BaseURL = "file:///tmp/model" }, want: "absolute HTTP or HTTPS URL"},
		{name: "missing host", change: func(c *provider.Config) { c.BaseURL = "http://" }, want: "absolute HTTP or HTTPS URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := provider.Config{BaseURL: "http://127.0.0.1", APIKey: "key", Model: "model"}
			tc.change(&cfg)
			model, err := provider.New(cfg)
			if model != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("provider = %T, error = %v", model, err)
			}
		})
	}
}

func TestNew_ProvidesOpenAIStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	model, err := provider.New(provider.Config{BaseURL: srv.URL, APIKey: "key", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	streaming, ok := model.(provider.StreamingProvider)
	if !ok {
		t.Fatal("OpenAI-compatible client does not expose the stream contract")
	}
	stream, err := streaming.ChatStream(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "hi", "")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("Recv error = %v, want io.EOF", err)
	}
}
