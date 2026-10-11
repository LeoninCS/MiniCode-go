package cli_test

import (
	"reflect"
	"strings"
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
