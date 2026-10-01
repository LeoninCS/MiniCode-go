package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
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
)

func TestMiniCode_Responses(t *testing.T) {
	binary := buildMiniCode(t)

	t.Run("text response", func(t *testing.T) {
		requestCh := make(chan provider.ChatRequest, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req provider.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
			}
			requestCh <- req
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
		}))
		defer srv.Close()

		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 0 {
			t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
		}
		if stdout != "hello\n" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
		}

		req := <-requestCh
		assertBuiltinSchemas(t, req.Tools)
	})

	t.Run("markdown stays plain in pipe", func(t *testing.T) {
		content := "# Go 示例\n\n**加粗**\n\n- 列表\n\n```go\nfmt.Println(\"hello\")\n```"
		srv, _ := conversationServer(t, provider.NewMessage(provider.RoleAssistant, content, ""))
		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 0 || stdout != content+"\n" || stderr != "" {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
	})

	for _, tc := range []struct {
		name    string
		content *string
	}{
		{name: "tool call response"},
		{name: "tool call with empty text", content: provider.NewMessage(provider.RoleAssistant, "", "").Content},
		{name: "tool call with text response", content: provider.NewMessage(provider.RoleAssistant, "我会先执行命令。", "").Content},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := provider.ToolCall{
				ID: "call_1", Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf 'shell-result\\n'"}`},
			}
			assistant := provider.Message{Role: provider.RoleAssistant, Content: tc.content, ToolCalls: []provider.ToolCall{call}}
			srv, requests := conversationServer(t, assistant, provider.NewMessage(provider.RoleAssistant, "命令执行完成。", ""))
			stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
			want := "tool: bash\narguments: " + call.Function.Arguments + "\nshell-result\n命令执行完成。\n"
			if tc.content != nil && *tc.content != "" {
				want = *tc.content + "\n" + want
			}
			if exitCode != 0 || stdout != want || stderr != "" {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
			}
			readRequest(t, requests)
			req := readRequest(t, requests)
			if len(req.Messages) != 4 {
				t.Fatalf("messages = %+v", req.Messages)
			}
			message := req.Messages[2]
			if message.Role != provider.RoleAssistant || len(message.ToolCalls) != 1 || message.ToolCalls[0] != call || message.Text() != assistant.Text() || (message.Content == nil) != (tc.content == nil) {
				t.Fatalf("assistant message was not preserved: %+v", message)
			}
			result := req.Messages[3]
			if result.Role != provider.RoleTool || result.ToolCallID != call.ID || result.Text() != "shell-result\n" {
				t.Fatalf("tool result = %+v", result)
			}
		})
	}

	t.Run("multiple calls and rounds", func(t *testing.T) {
		first := provider.ToolCall{ID: "first", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf first"}`}}
		second := provider.ToolCall{ID: "second", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf second"}`}}
		third := provider.ToolCall{ID: "third", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf third"}`}}
		srv, requests := conversationServer(t,
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{first, second}},
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{third}},
			provider.NewMessage(provider.RoleAssistant, "done", ""),
		)
		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 0 || stderr != "" || !strings.HasSuffix(stdout, "third\ndone\n") {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		readRequest(t, requests)
		middle := readRequest(t, requests)
		if len(middle.Messages) != 5 {
			t.Fatalf("expected both tool results before next request, got %+v", middle.Messages)
		}
		last := readRequest(t, requests)
		if len(last.Messages) != 7 {
			t.Fatalf("messages = %+v", last.Messages)
		}
		for i, index := range []int{3, 4, 6} {
			want := []string{"first", "second", "third"}[i]
			result := last.Messages[index]
			if result.Role != provider.RoleTool || result.ToolCallID != want || result.Text() != want {
				t.Fatalf("tool result = %+v, want %q", result, want)
			}
		}
	})

	for _, tc := range []struct {
		name      string
		tool      string
		arguments string
		wantError string
	}{
		{name: "command failed", tool: "bash", arguments: `{"command":"printf failed; exit 7"}`, wantError: "exit status 7"},
		{name: "unknown tool", tool: "unknown", arguments: `{}`, wantError: "unknown tool"},
		{name: "missing command", tool: "bash", arguments: `{}`, wantError: "command must be a string"},
		{name: "invalid command type", tool: "bash", arguments: `{"command":42}`, wantError: "command must be a string"},
		{name: "empty command", tool: "bash", arguments: `{"command":" "}`, wantError: "command must not be empty"},
		{name: "unknown parameter", tool: "bash", arguments: `{"command":"printf should-not-run","extra":true}`, wantError: "only the command parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := provider.ToolCall{ID: "failed_call", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: tc.tool, Arguments: tc.arguments}}
			srv, requests := conversationServer(t,
				provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}},
				provider.NewMessage(provider.RoleAssistant, "已收到工具错误。", ""),
			)
			stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
			if exitCode != 0 || !strings.HasSuffix(stdout, "已收到工具错误。\n") || !strings.Contains(stderr, tc.wantError) {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
			}
			readRequest(t, requests)
			req := readRequest(t, requests)
			if len(req.Messages) != 4 || req.Messages[3].ToolCallID != call.ID || !strings.Contains(req.Messages[3].Text(), tc.wantError) {
				t.Fatalf("error was not returned to model: %+v", req.Messages)
			}
		})
	}

	for _, tc := range []struct {
		name      string
		summary   provider.Message
		wantText  string
		wantError string
	}{
		{
			name:     "round limit summary",
			summary:  provider.NewMessage(provider.RoleAssistant, "已达到轮数上限，完成了检查，修改尚未完成。", ""),
			wantText: "已达到轮数上限，完成了检查，修改尚未完成。",
		},
		{
			name:     "round limit empty summary",
			summary:  provider.NewMessage(provider.RoleAssistant, " \n", ""),
			wantText: "已达到最大执行轮数（500）", wantError: "empty summary response",
		},
		{
			name: "round limit refuses more tools",
			summary: provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
				ID: "extra", Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf should-not-run"}`},
			}}},
			wantText: "已达到最大执行轮数（500）", wantError: "summary response requested tools",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := provider.ToolCall{ID: "again", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":":"}`}}
			replies := make([]provider.Message, 500)
			for i := range replies {
				replies[i] = provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}
			}
			replies = append(replies, tc.summary)
			srv, requests := conversationServer(t, replies...)
			stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
			if exitCode != 1 || !strings.Contains(stdout, tc.wantText) || !strings.Contains(stderr, "maximum model turns (500)") || !strings.Contains(stderr, tc.wantError) || len(requests) != 501 {
				t.Fatalf("exit = %d, requests = %d, stdout = %q, stderr = %q", exitCode, len(requests), stdout, stderr)
			}
			if strings.Count(stdout, "tool: bash\n") != 500 || strings.Contains(stdout, "should-not-run") {
				t.Fatalf("unexpected tool execution after limit: %q", stdout)
			}
			for i := 0; i < 500; i++ {
				readRequest(t, requests)
			}
			request := readRequest(t, requests)
			if len(request.Tools) != 0 || len(request.Messages) != 1003 {
				t.Fatalf("summary request = %+v", request)
			}
			lastResult := request.Messages[1001]
			instruction := request.Messages[1002]
			if lastResult.Role != provider.RoleTool || lastResult.ToolCallID != call.ID || instruction.Role != provider.RoleSystem || !strings.Contains(instruction.Text(), "停止调用工具") {
				t.Fatalf("incomplete summary history: %+v", request.Messages)
			}
		})
	}

	t.Run("final answer on last allowed turn", func(t *testing.T) {
		call := provider.ToolCall{ID: "again", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":":"}`}}
		replies := make([]provider.Message, 499)
		for i := range replies {
			replies[i] = provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}
		}
		replies = append(replies, provider.NewMessage(provider.RoleAssistant, "done", ""))
		srv, requests := conversationServer(t, replies...)
		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 0 || !strings.HasSuffix(stdout, "done\n") || stderr != "" || len(requests) != 500 {
			t.Fatalf("exit = %d, requests = %d, stdout = %q, stderr = %q", exitCode, len(requests), stdout, stderr)
		}
	})

	t.Run("command timeout", func(t *testing.T) {
		call := provider.ToolCall{ID: "slow", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf started; sleep 10"}`}}
		srv, requests := conversationServer(t, provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}})
		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL, "-timeout", "500ms")
		if exitCode != 1 || !strings.Contains(stdout, "\nstarted\n") || !strings.Contains(stderr, "context deadline exceeded") || len(requests) != 1 {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`)
		}))
		defer srv.Close()

		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 1 {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		if stdout != "" || !strings.Contains(stderr, "empty response from model") {
			t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
		}
	})

	t.Run("invalid tool arguments", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"not-json"}}]},"finish_reason":"tool_calls"}]}`)
		}))
		defer srv.Close()

		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 1 {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		if stdout != "" || !strings.Contains(stderr, "arguments must be a JSON object") {
			t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
		}
	})

	t.Run("api error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"bad key","type":"authentication_error"}}`)
		}))
		defer srv.Close()

		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 1 {
			t.Fatalf("exit code = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		if stdout != "" || !strings.Contains(stderr, "invalid_api_key") {
			t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
		}
	})
}

// TestMiniCode_InteractiveSession 覆盖交互循环:提示符、退出命令,
// 以及"每轮独立、不累积历史"这一设计。
func TestMiniCode_InteractiveSession(t *testing.T) {
	binary := buildMiniCode(t)

	t.Run("multiple rounds then /exit", func(t *testing.T) {
		srv, requests := conversationServer(t,
			provider.NewMessage(provider.RoleAssistant, "第一轮", ""),
			provider.NewMessage(provider.RoleAssistant, "第二轮", ""),
		)
		stdout, stderr, exitCode := runMiniCodeSession(t, binary, srv.URL, "第一个问题\n第二个问题\n/exit\n")
		if exitCode != 0 || stderr != "" {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		// 两轮任务加一次 /exit,提示符打印三次。
		if got := strings.Count(stdout, promptMarker); got != 3 {
			t.Fatalf("prompt count = %d, stdout = %q", got, stdout)
		}
		if !strings.Contains(stdout, "第一轮") || !strings.Contains(stdout, "第二轮") {
			t.Fatalf("stdout = %q", stdout)
		}
		// 同一个循环共用一个会话,第二轮要带上第一轮的问答,模型才记得住前面说过什么。
		first := readRequest(t, requests)
		second := readRequest(t, requests)
		if len(first.Messages) != 2 || first.Messages[0].Role != provider.RoleSystem {
			t.Fatalf("first request = %+v", first.Messages)
		}
		if len(second.Messages) != 4 {
			t.Fatalf("second request = %+v", second.Messages)
		}
		want := []string{"第一个问题", "第一轮", "第二个问题"}
		if first.Messages[0].Role != provider.RoleSystem || second.Messages[0].Role != provider.RoleSystem {
			t.Fatalf("system prompt was not sent every round")
		}
		for i, text := range want {
			if got := second.Messages[1+i].Text(); got != text {
				t.Fatalf("second.Messages[%d] = %q, want %q", 1+i, got, text)
			}
		}
	})

	t.Run("/quit exits the same way", func(t *testing.T) {
		srv, _ := conversationServer(t, provider.NewMessage(provider.RoleAssistant, "回答", ""))
		stdout, stderr, exitCode := runMiniCodeSession(t, binary, srv.URL, "一个问题\n/quit\n")
		if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "回答") {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		if got := strings.Count(stdout, promptMarker); got != 2 {
			t.Fatalf("prompt count = %d, stdout = %q", got, stdout)
		}
	})

	t.Run("eof without exit command", func(t *testing.T) {
		srv, requests := conversationServer(t, provider.NewMessage(provider.RoleAssistant, "回答", ""))
		stdout, stderr, exitCode := runMiniCodeSession(t, binary, srv.URL, "一个问题\n")
		if exitCode != 0 || stderr != "" {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		readRequest(t, requests)
	})

	t.Run("empty lines are skipped", func(t *testing.T) {
		// 没有任何回复可用,一旦空行被当成任务发出就会让服务端报错。
		srv, _ := conversationServer(t)
		stdout, stderr, exitCode := runMiniCodeSession(t, binary, srv.URL, "\n\n/exit\n")
		if exitCode != 0 || stderr != "" {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
		if got := strings.Count(stdout, promptMarker); got != 3 {
			t.Fatalf("prompt count = %d, stdout = %q", got, stdout)
		}
	})

	t.Run("empty stdin exits cleanly", func(t *testing.T) {
		// 默认就是循环,没有输入时按 EOF 干净退出,不再报"no prompt provided"。
		srv, _ := conversationServer(t)
		stdout, stderr, exitCode := runMiniCodeSession(t, binary, srv.URL, "")
		if exitCode != 0 || stderr != "" || stdout != promptMarker+"\n" {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
	})

	t.Run("prompt argument seeds the first round", func(t *testing.T) {
		// 位置参数只是循环的第一行输入,跑完仍然回到提示符等下一轮。
		srv, requests := conversationServer(t,
			provider.NewMessage(provider.RoleAssistant, "回答", ""),
			provider.NewMessage(provider.RoleAssistant, "再答", ""),
		)
		_, stderr, _ := runMiniCodeRaw(t, binary, srv.URL, "命令行任务", "第二个任务\n")
		if stderr != "" {
			t.Fatalf("stderr = %q", stderr)
		}
		first := readRequest(t, requests)
		second := readRequest(t, requests)
		if first.Messages[1].Text() != "命令行任务" {
			t.Fatalf("first request = %+v", first.Messages)
		}
		// 第二轮请求带着上一轮的历史,新输入排在最后。
		if len(second.Messages) != 4 || second.Messages[3].Text() != "第二个任务" {
			t.Fatalf("second request = %+v", second.Messages)
		}
	})

	t.Run("failed round keeps the session", func(t *testing.T) {
		var count atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if count.Add(1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"bad key"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{
				{Message: provider.NewMessage(provider.RoleAssistant, "恢复了", "")},
			}})
		}))
		defer srv.Close()

		stdout, stderr, exitCode := runMiniCodeSession(t, binary, srv.URL, "第一个问题\n第二个问题\n/exit\n")
		// 第一轮 401、第二轮恢复,会话继续到 /exit;但有轮次失败,退出码仍是 1,
		// 这样 `echo "任务" | minicode` 依然能反映任务成败。
		if exitCode != 1 || !strings.Contains(stderr, "invalid_api_key") || !strings.Contains(stdout, "恢复了") {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout, stderr)
		}
	})
}

// promptMarker 与 CLI 里的交互提示符保持一致。
const promptMarker = "> "

func buildMiniCode(t *testing.T) string {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	moduleRoot := filepath.Clean(filepath.Join(workingDir, "..", ".."))
	binary := filepath.Join(t.TempDir(), "minicode")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/minicode")
	cmd.Dir = moduleRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build minicode: %v\n%s", err, output)
	}
	return binary
}

// runMiniCodeRaw 启动 minicode 并原样返回输出。
// prompt 非空时作为位置参数(即循环的第一轮输入),stdin 提供之后读到的内容。
func runMiniCodeRaw(t *testing.T, binary, baseURL, prompt, stdin string, flags ...string) (string, string, int) {
	t.Helper()
	args := []string{"-api-key", "test-key", "-base-url", baseURL, "-model", "test-model"}
	args = append(args, flags...)
	if prompt != "" {
		args = append(args, prompt)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run minicode: %v", err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

// runMiniCode 跑一轮任务并剥掉交互循环的提示符。
// 位置参数作为第一轮输入、stdin 为空,所以 stdout 形如 "> " + 本轮输出 + "> \n"。
// 提示符属于 CLI 装饰,这些用例关注 Agent 行为,统一在这里剥掉;
// 提示符本身由 TestMiniCode_InteractiveSession 覆盖。
func runMiniCode(t *testing.T, binary, baseURL string, flags ...string) (string, string, int) {
	t.Helper()
	stdout, stderr, code := runMiniCodeRaw(t, binary, baseURL, "run tests", "", flags...)
	stdout = strings.TrimPrefix(strings.TrimSuffix(stdout, promptMarker+"\n"), promptMarker)
	return stdout, stderr, code
}

// runMiniCodeSession 不传位置参数启动 minicode,stdin 提供多行输入。
// 默认行为就是交互循环,所以这里不需要任何额外开关,输出原样返回。
func runMiniCodeSession(t *testing.T, binary, baseURL, stdin string, flags ...string) (string, string, int) {
	t.Helper()
	return runMiniCodeRaw(t, binary, baseURL, "", stdin, flags...)
}

// assertBuiltinSchemas 校验首次请求带上四个内置工具,名称、顺序和关键参数类型都要正确。
func assertBuiltinSchemas(t *testing.T, tools []provider.Tool) {
	t.Helper()
	want := []struct {
		name       string
		properties map[string]string
	}{
		{"read", map[string]string{"path": "string", "offset": "integer", "limit": "integer"}},
		{"bash", map[string]string{"command": "string"}},
		{"edit", map[string]string{"path": "string", "old_text": "string", "new_text": "string", "replace_all": "boolean"}},
		{"write", map[string]string{"path": "string", "content": "string"}},
	}
	if len(tools) != len(want) {
		t.Fatalf("tools = %+v", tools)
	}
	for i, expected := range want {
		tool := tools[i]
		if tool.Type != provider.ToolTypeFunction || tool.Function.Name != expected.name || tool.Function.Description == "" {
			t.Fatalf("tool = %+v, want %s", tool, expected.name)
		}
		if tool.Function.Parameters["type"] != "object" || tool.Function.Parameters["additionalProperties"] != false {
			t.Fatalf("%s schema = %+v", expected.name, tool.Function.Parameters)
		}
		properties, ok := tool.Function.Parameters["properties"].(map[string]any)
		if !ok || len(properties) != len(expected.properties) {
			t.Fatalf("%s properties = %#v", expected.name, tool.Function.Parameters["properties"])
		}
		for name, kind := range expected.properties {
			property, ok := properties[name].(map[string]any)
			if !ok || property["type"] != kind {
				t.Fatalf("%s.%s schema = %#v", expected.name, name, properties[name])
			}
		}
	}
}

// conversationServer 按顺序返回预设回复,记录每轮实际发出的请求。
func conversationServer(t *testing.T, replies ...provider.Message) (*httptest.Server, <-chan provider.ChatRequest) {
	t.Helper()
	requests := make(chan provider.ChatRequest, len(replies)+1)
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		requests <- req
		index := int(count.Add(1)) - 1
		if index >= len(replies) {
			t.Errorf("unexpected model request %d", index+1)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		finishReason := "stop"
		if len(replies[index].ToolCalls) > 0 {
			finishReason = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(provider.ChatResponse{Choices: []provider.Choice{{Message: replies[index], FinishReason: finishReason}}})
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

func readRequest(t *testing.T, requests <-chan provider.ChatRequest) provider.ChatRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	default:
		t.Fatal("expected another model request")
		return provider.ChatRequest{}
	}
}
