package provider_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestIsContextOverflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"wrapped context code", fmt.Errorf("request: %w", &provider.APIError{StatusCode: 400, Code: "context_length_exceeded"}), true},
		{"stream context code", &provider.APIError{Code: "model_context_window_exceeded"}, true},
		{"request too large with context code", &provider.APIError{StatusCode: 413, Code: "context_length_exceeded"}, true},
		{"input exceeds window", &provider.APIError{StatusCode: 400, Message: "Your input exceeds the context window of this model."}, true},
		{"minimax context message", &provider.APIError{StatusCode: 400, Message: "invalid params, context window exceeds limit"}, true},
		{"combined input and output exceed window", &provider.APIError{StatusCode: 400, Message: "This model's maximum context length is 262144 tokens. However, you requested 270000 tokens (260000 in the messages, 10000 in the completion)."}, true},
		{"nil", nil, false},
		{"nil API error", (*provider.APIError)(nil), false},
		{"non API error", errors.New("input exceeds the context window"), false},
		{"output limit", &provider.APIError{StatusCode: 400, Code: "invalid_parameter", Param: "max_tokens", Message: "max_tokens is 9216 but the maximum output length is 8192 tokens"}, false},
		{"output limit mentioning context", &provider.APIError{StatusCode: 400, Message: "max_tokens cannot exceed the maximum context length"}, false},
		{"rate limit", &provider.APIError{StatusCode: 429, Code: "context_length_exceeded"}, false},
		{"rate limit code", &provider.APIError{Code: "rate_limit_exceeded", Message: "input exceeds the context window"}, false},
		{"rate limit type", &provider.APIError{Type: "rate_limit_error", Message: "input exceeds the context window"}, false},
		{"authentication", &provider.APIError{StatusCode: 401, Message: "input exceeds the context window"}, false},
		{"service error", &provider.APIError{StatusCode: 503, Message: "input exceeds the context window"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := provider.IsContextOverflow(tc.err); got != tc.want {
				t.Fatalf("IsContextOverflow(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
