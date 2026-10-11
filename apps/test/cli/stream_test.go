package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func TestMiniCode_DefaultStreamingPreservesToolsUsageAndSession(t *testing.T) {
	binary := buildMiniCode(t)
	for _, kind := range []string{"text", "tool fragments", "interrupted"} {
		t.Run(kind, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					provider.ChatRequest
					Stream bool `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Stream {
					t.Errorf("default request = %+v, error = %v", req, err)
				}
				index := count.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if kind == "tool fragments" && index == 1 {
					_, _ = io.WriteString(w, "data: "+`{"choices":[{"index":0,"delta":{"content":"中间过程","tool_calls":[{"index":0,"id":"call-read","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`+"\n\n")
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, "data: "+`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
					_, _ = io.WriteString(w, "data: "+`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`+"\n\ndata: [DONE]\n\n")
					return
				}
				if kind == "tool fragments" {
					if index != 2 || len(req.Messages) != 4 || req.Messages[3].Role != provider.RoleTool || req.Messages[3].ToolCallID != "call-read" || !strings.Contains(req.Messages[3].Text(), "fixture-data") {
						t.Errorf("tool history = %+v", req.Messages)
					}
				}
				text := "完整回复"
				if kind == "interrupted" {
					text = "partial-response"
				}
				body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": "stop"}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
				w.(http.Flusher).Flush()
				if kind == "interrupted" {
					return
				}
				_, _ = io.WriteString(w, "data: "+`{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`+"\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			workspace := t.TempDir()
			writeModelConfig(t, workspace, srv.URL)
			if err := os.WriteFile(filepath.Join(workspace, "main.txt"), []byte("fixture-data"), 0o600); err != nil {
				t.Fatal(err)
			}
			// 无需配置流式参数，普通启动即发送 SSE 请求。
			cmd := exec.Command(binary, "hello")
			cmd.Dir = workspace
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if cmd.ProcessState == nil {
				t.Fatalf("start: %v", err)
			}
			wantCode, wantMessages, wantStatus := 0, 2, "completed"
			wantOutput := "完整回复\n" + promptMarker + "\n"
			if kind == "tool fragments" {
				wantMessages = 4
			}
			if kind == "interrupted" {
				wantCode, wantMessages, wantStatus = 1, 1, "failed"
				wantOutput = promptMarker + "\n"
			}
			if cmd.ProcessState.ExitCode() != wantCode || stdout.String() != wantOutput {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", cmd.ProcessState.ExitCode(), stdout.String(), stderr.String())
			}
			if kind == "interrupted" {
				if !strings.Contains(stderr.String(), "unexpected EOF") {
					t.Fatalf("error = %q", stderr.String())
				}
			} else {
				wantTokens := "Token 6（输入 4 / 输出 2）"
				if kind == "tool fragments" {
					wantTokens = "Token 9（输入 6 / 输出 3）"
				}
				if stripStats(stderr.String()) != "" || !strings.Contains(stderr.String(), wantTokens) {
					t.Fatalf("stderr = %q", stderr.String())
				}
			}
			id := sessionIDFromOutput(t, stderr.String())
			path, err := sessionstore.Path(workspace, id)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := sessionstore.Load(path)
			if err != nil || len(saved.Messages) != wantMessages || saved.LastTurnStatus != wantStatus {
				t.Fatalf("snapshot = %+v, error = %v", saved, err)
			}
			if kind != "interrupted" && saved.Messages[len(saved.Messages)-1].Text() != "完整回复" {
				t.Fatalf("final response was not saved: %+v", saved.Messages)
			}
		})
	}
}

// writeStreamResponse 将完整测试回复作为 SSE 事件返回，覆盖正常启动路径。
func writeStreamResponse(w http.ResponseWriter, response provider.ChatResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, choice := range response.Choices {
		calls := make([]map[string]any, 0, len(choice.Message.ToolCalls))
		for i, call := range choice.Message.ToolCalls {
			calls = append(calls, map[string]any{"index": i, "id": call.ID, "type": call.Type, "function": call.Function})
		}
		finish := choice.FinishReason
		if finish == "" {
			finish = "stop"
			if len(calls) > 0 {
				finish = "tool_calls"
			}
		}
		_, _ = io.WriteString(w, "data: ")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"index":         choice.Index,
				"delta":         map[string]any{"content": choice.Message.Content, "tool_calls": calls},
				"finish_reason": finish,
			}},
		})
		_, _ = io.WriteString(w, "\n")
	}
	if response.Usage != nil {
		_, _ = io.WriteString(w, "data: ")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}, "usage": response.Usage})
		_, _ = io.WriteString(w, "\n")
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}
