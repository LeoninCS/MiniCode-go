package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestMiniCode_ManualCompact(t *testing.T) {
	binary := buildMiniCode(t)

	t.Run("empty history and repeated commands", func(t *testing.T) {
		srv, requests := conversationServer(t)
		stdout, stderr, code := runMiniCodeSession(t, binary, srv.URL, "/compact\n \t/compact \t\n/exit\n")
		if code != 0 || stderr != "" || strings.Count(stdout, "压缩策略尚未实现") != 2 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if len(requests) != 0 {
			t.Fatal("manual compaction sent a model request")
		}
	})

	t.Run("preserves history and continues conversation", func(t *testing.T) {
		srv, requests := conversationServer(t,
			provider.NewMessage(provider.RoleAssistant, "第一轮回答", ""),
			provider.NewMessage(provider.RoleAssistant, "第二轮回答", ""),
		)
		stdout, stderr, code := runMiniCodeSession(t, binary, srv.URL, "第一轮问题\n/compact\n第二轮问题\n/exit\n")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "压缩策略尚未实现") || !strings.Contains(stdout, "第二轮回答") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		first := readRequest(t, requests)
		second := readRequest(t, requests)
		if len(first.Messages) != 2 || len(second.Messages) != 4 || !reflect.DeepEqual(second.Messages[:2], first.Messages) || second.Messages[2].Text() != "第一轮回答" || second.Messages[3].Text() != "第二轮问题" {
			t.Fatalf("history after manual compaction = %+v", second.Messages)
		}
		if len(requests) != 0 {
			t.Fatal("manual compaction generated an extra model request")
		}
	})

	t.Run("initial command argument", func(t *testing.T) {
		srv, requests := conversationServer(t, provider.NewMessage(provider.RoleAssistant, "回答", ""))
		stdout, stderr, code := runMiniCodeRaw(t, binary, srv.URL, "/compact", "问题\n/exit\n")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "压缩策略尚未实现") || !strings.Contains(stdout, "回答") {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		request := readRequest(t, requests)
		if len(request.Messages) != 2 || request.Messages[1].Text() != "问题" || len(requests) != 0 {
			t.Fatalf("manual command entered conversation history: %+v", request.Messages)
		}
	})
}

func TestMiniCode_AutomaticCompaction(t *testing.T) {
	binary := buildMiniCode(t)
	for _, tc := range []struct {
		name string
		used int
		want int
	}{
		{"fixed window triggers", 245761, 1},
		{"latest usage is not cumulative", 130000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req provider.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				index := int(calls.Add(1))
				if index > 3 || len(req.Messages) != index*2 || req.Messages[len(req.Messages)-1].Text() != "问题" {
					t.Errorf("unexpected request history: %+v", req.Messages)
				}
				used := tc.used
				if tc.want > 0 && index > 1 {
					// 第二次回复重新校准到较低占用，第三次请求应不再触发。
					used = 1000
				}
				writeStreamResponse(w, provider.ChatResponse{
					Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "回答", ""), FinishReason: "stop"}},
					Usage:   &provider.Usage{PromptTokens: used - 1, CompletionTokens: 1, TotalTokens: used},
				})
			}))
			defer srv.Close()
			cmd := exec.Command(binary)
			cmd.Dir = t.TempDir()
			writeModelConfig(t, cmd.Dir, srv.URL)
			cmd.Stdin = strings.NewReader("问题\n问题\n问题\n/exit\n")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v, stderr=%q", err, stderr.String())
			}
			if calls.Load() != 3 || strings.Count(stdout.String(), "已自动触发压缩") != tc.want || strings.Count(stdout.String(), "回答") != 3 {
				t.Fatalf("calls=%d stdout=%q stderr=%q", calls.Load(), stdout.String(), stderr.String())
			}
		})
	}
}
