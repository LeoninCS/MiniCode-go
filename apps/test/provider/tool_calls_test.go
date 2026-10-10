package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestClient_Chat_SendsFunctionToolSchema(t *testing.T) {
	type observedRequest struct {
		method string
		path   string
		body   []byte
		err    error
	}
	observed := make(chan observedRequest, 1)

	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		observed <- observedRequest{
			method: r.Method,
			path:   r.URL.Path,
			body:   body,
			err:    err,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	})

	_, err := client.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{
			provider.NewMessage(provider.RoleUser, "请查询上海天气", ""),
		},
		Tools: []provider.Tool{
			{
				Type: provider.ToolTypeFunction,
				Function: provider.FunctionDefinition{
					Name:        "get_weather",
					Description: "查询指定城市的天气",
					Parameters: provider.JSONSchema{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{
								"type":        "string",
								"description": "城市名称",
							},
						},
						"required":             []string{"city"},
						"additionalProperties": false,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	got := <-observed
	if got.err != nil {
		t.Fatalf("read request body: %v", got.err)
	}
	if got.method != http.MethodPost {
		t.Errorf("expected POST, got %s", got.method)
	}
	if got.path != "/chat/completions" {
		t.Errorf("expected /chat/completions, got %s", got.path)
	}
	assertJSONDocumentEqual(t, `{
		"model": "test-model",
		"messages": [
			{"role": "user", "content": "请查询上海天气"}
		],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "get_weather",
					"description": "查询指定城市的天气",
					"parameters": {
						"type": "object",
						"properties": {
							"city": {
								"type": "string",
								"description": "城市名称"
							}
						},
						"required": ["city"],
						"additionalProperties": false
					}
				}
			}
		]
	}`, got.body)
}

func TestClient_Chat_ParsesToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     []provider.ToolCall
	}{
		{
			name: "single",
			response: `{
				"id":"chatcmpl-single",
				"model":"test-model",
				"choices":[{
					"index":0,
					"message":{
						"role":"assistant",
						"content":null,
						"tool_calls":[{
							"id":"call_weather",
							"type":"function",
							"function":{"name":"get_weather","arguments":"{\"city\":\"上海\"}"}
						}]
					},
					"finish_reason":"tool_calls"
				}]
			}`,
			want: []provider.ToolCall{
				{
					ID:   "call_weather",
					Type: provider.ToolTypeFunction,
					Function: provider.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"city":"上海"}`,
					},
				},
			},
		},
		{
			name: "multiple",
			response: `{
				"id":"chatcmpl-multiple",
				"model":"test-model",
				"choices":[{
					"index":0,
					"message":{
						"role":"assistant",
						"content":null,
						"tool_calls":[
							{
								"id":"call_weather",
								"type":"function",
								"function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}
							},
							{
								"id":"call_calculator",
								"type":"function",
								"function":{"name":"calculate","arguments":"{\"expression\":\"2+2\"}"}
							}
						]
					},
					"finish_reason":"tool_calls"
				}]
			}`,
			want: []provider.ToolCall{
				{
					ID:   "call_weather",
					Type: provider.ToolTypeFunction,
					Function: provider.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"city":"北京"}`,
					},
				},
				{
					ID:   "call_calculator",
					Type: provider.ToolTypeFunction,
					Function: provider.FunctionCall{
						Name:      "calculate",
						Arguments: `{"expression":"2+2"}`,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			})

			resp, err := client.Chat(context.Background(), provider.ChatRequest{
				Messages: []provider.Message{
					provider.NewMessage(provider.RoleUser, "执行工具", ""),
				},
			})
			if err != nil {
				t.Fatalf("chat: %v", err)
			}
			if resp.Choices[0].Message.Content != nil {
				t.Errorf("expected content:null to remain nil, got %q", resp.Choices[0].Message.Text())
			}
			messageJSON, err := json.Marshal(resp.Choices[0].Message)
			if err != nil {
				t.Fatalf("marshal assistant message: %v", err)
			}
			var messageObject map[string]json.RawMessage
			if err := json.Unmarshal(messageJSON, &messageObject); err != nil {
				t.Fatalf("decode marshaled assistant message: %v", err)
			}
			if got := string(messageObject["content"]); got != "null" {
				t.Errorf("expected content to round-trip as null, got %s", got)
			}

			got, err := resp.ToolCalls()
			if err != nil {
				t.Fatalf("tool calls: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("unexpected tool calls:\nwant: %#v\n got: %#v", tc.want, got)
			}
		})
	}
}

func TestToolCall_DecodeArguments_Object(t *testing.T) {
	call := provider.ToolCall{
		ID:   "call_weather",
		Type: provider.ToolTypeFunction,
		Function: provider.FunctionCall{
			Name:      "get_weather",
			Arguments: `{"city":"上海","days":3,"metric":true}`,
		},
	}
	if err := call.Validate(); err != nil {
		t.Fatalf("validate legal tool call: %v", err)
	}

	var got struct {
		City   string `json:"city"`
		Days   int    `json:"days"`
		Metric bool   `json:"metric"`
	}
	if err := call.DecodeArguments(&got); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if got.City != "上海" || got.Days != 3 || !got.Metric {
		t.Errorf("unexpected decoded arguments: %+v", got)
	}

	call.Function.Arguments = `{}`
	var empty map[string]any
	if err := call.DecodeArguments(&empty); err != nil {
		t.Fatalf("decode empty object: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("expected an empty non-nil object, got %#v", empty)
	}

	call.Function.Arguments = `{"query":{"city":"杭州","options":{"metric":true}}}`
	var nested struct {
		Query struct {
			City    string `json:"city"`
			Options struct {
				Metric bool `json:"metric"`
			} `json:"options"`
		} `json:"query"`
	}
	if err := call.DecodeArguments(&nested); err != nil {
		t.Fatalf("decode nested object: %v", err)
	}
	if nested.Query.City != "杭州" || !nested.Query.Options.Metric {
		t.Errorf("unexpected nested arguments: %+v", nested)
	}
}

func TestToolCall_DecodeArguments_RejectsNonObjects(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		wantSub   string
	}{
		{name: "empty", arguments: "", wantSub: "empty arguments"},
		{name: "invalid JSON", arguments: `{`, wantSub: "JSON object"},
		{name: "array", arguments: `["上海"]`, wantSub: "JSON object"},
		{name: "null", arguments: `null`, wantSub: "JSON object"},
		{name: "string", arguments: `"上海"`, wantSub: "JSON object"},
		{name: "number", arguments: `42`, wantSub: "JSON object"},
		{name: "boolean", arguments: `true`, wantSub: "JSON object"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call := provider.ToolCall{
				ID:   "call_weather",
				Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{
					Name:      "get_weather",
					Arguments: tc.arguments,
				},
			}
			var dst map[string]any
			err := call.DecodeArguments(&dst)
			if err == nil {
				t.Fatalf("expected DecodeArguments to reject %q", tc.arguments)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("expected error to contain %q, got %v", tc.wantSub, err)
			}
			if err := call.Validate(); err == nil {
				t.Errorf("expected Validate to reject %q", tc.arguments)
			}
		})
	}
}

func TestToolCall_DecodeArguments_RejectsNilDestination(t *testing.T) {
	call := provider.ToolCall{
		Function: provider.FunctionCall{Name: "get_weather", Arguments: `{}`},
	}
	if err := call.DecodeArguments(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected nil destination error, got %v", err)
	}
}

func TestToolCall_Validate_RejectsMissingFieldsAndUnsupportedType(t *testing.T) {
	tests := []struct {
		name    string
		call    provider.ToolCall
		wantSub string
	}{
		{
			name: "missing ID",
			call: provider.ToolCall{
				Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{
					Name:      "get_weather",
					Arguments: `{}`,
				},
			},
			wantSub: "empty ID",
		},
		{
			name: "missing type",
			call: provider.ToolCall{
				ID: "call_weather",
				Function: provider.FunctionCall{
					Name:      "get_weather",
					Arguments: `{}`,
				},
			},
			wantSub: "unsupported type",
		},
		{
			name: "missing function name",
			call: provider.ToolCall{
				ID:   "call_weather",
				Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{
					Arguments: `{}`,
				},
			},
			wantSub: "empty function name",
		},
		{
			name: "non-function type",
			call: provider.ToolCall{
				ID:   "call_weather",
				Type: provider.ToolType("computer"),
				Function: provider.FunctionCall{
					Name:      "get_weather",
					Arguments: `{}`,
				},
			},
			wantSub: "unsupported type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call.Validate()
			if err == nil {
				t.Fatal("expected Validate to reject tool call")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("expected error to contain %q, got %v", tc.wantSub, err)
			}

			resp := &provider.ChatResponse{
				Choices: []provider.Choice{
					{Message: provider.Message{ToolCalls: []provider.ToolCall{tc.call}}},
				},
			}
			if _, err := resp.ToolCalls(); err == nil {
				t.Error("expected ToolCalls to propagate validation error")
			}
		})
	}
}

func TestChatResponse_ToolCalls_Empty(t *testing.T) {
	tests := []struct {
		name string
		resp *provider.ChatResponse
	}{
		{name: "nil response", resp: nil},
		{name: "no choices", resp: &provider.ChatResponse{}},
		{
			name: "ordinary text",
			resp: &provider.ChatResponse{
				Choices: []provider.Choice{
					{Message: provider.NewMessage(provider.RoleAssistant, "普通文本回复", "")},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls, err := tc.resp.ToolCalls()
			if err != nil {
				t.Fatalf("tool calls: %v", err)
			}
			if len(calls) != 0 {
				t.Errorf("expected no tool calls, got %+v", calls)
			}
		})
	}
}

func TestChatResponse_ToolCalls_RejectsDuplicateIDs(t *testing.T) {
	call := provider.ToolCall{
		ID:       "call_duplicate",
		Type:     provider.ToolTypeFunction,
		Function: provider.FunctionCall{Name: "get_weather", Arguments: `{}`},
	}
	resp := &provider.ChatResponse{
		Choices: []provider.Choice{
			{Message: provider.Message{ToolCalls: []provider.ToolCall{call, call}}},
		},
	}
	if _, err := resp.ToolCalls(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate ID error, got %v", err)
	}
}

func TestChatResponse_ToolCalls_RejectsIncompleteFinish(t *testing.T) {
	call := provider.ToolCall{
		ID:       "call_truncated",
		Type:     provider.ToolTypeFunction,
		Function: provider.FunctionCall{Name: "get_weather", Arguments: `{}`},
	}
	resp := &provider.ChatResponse{
		Choices: []provider.Choice{
			{
				Message:      provider.Message{ToolCalls: []provider.ToolCall{call}},
				FinishReason: "length",
			},
		},
	}
	if _, err := resp.ToolCalls(); err == nil || !strings.Contains(err.Error(), "finish reason") {
		t.Fatalf("expected incomplete finish error, got %v", err)
	}
}

func TestMessage_ToolResultJSONShape(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "JSON result",
			content: `{"temperature":26}`,
			want:    `{"role":"tool","content":"{\"temperature\":26}","tool_call_id":"call_weather"}`,
		},
		{
			name:    "empty result",
			content: "",
			want:    `{"role":"tool","content":"","tool_call_id":"call_weather"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message := provider.NewMessage(provider.RoleTool, tc.content, "call_weather")
			got, err := json.Marshal(message)
			if err != nil {
				t.Fatalf("marshal tool result message: %v", err)
			}
			assertJSONDocumentEqual(t, tc.want, got)
		})
	}
}

func assertJSONDocumentEqual(t *testing.T, want string, got []byte) {
	t.Helper()
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("invalid expected JSON: %v", err)
	}
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("invalid actual JSON: %v; body=%q", err, got)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("unexpected JSON document:\nwant: %s\n got: %s", want, got)
	}
}
