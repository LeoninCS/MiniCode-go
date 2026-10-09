package cli_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func TestMiniCode_DefaultSessionResumesByID(t *testing.T) {
	binary := buildMiniCode(t)
	call := provider.ToolCall{
		ID: "saved_call", Type: provider.ToolTypeFunction,
		Function: provider.FunctionCall{Name: "bash", Arguments: `{"command":"printf saved-result"}`},
	}
	assistant := provider.NewMessage(provider.RoleAssistant, "先调用工具。", "")
	assistant.ToolCalls = []provider.ToolCall{call}
	srv, requests := conversationServer(t, assistant,
		provider.NewMessage(provider.RoleAssistant, "第一轮回答", ""),
		provider.NewMessage(provider.RoleAssistant, "第二轮回答", ""),
		provider.NewMessage(provider.RoleAssistant, "新会话回答", ""),
	)
	workspace := t.TempDir()
	writeModelConfig(t, workspace, srv.URL)
	run := func(args ...string) (string, string, int) {
		t.Helper()
		return runMiniCodeInWorkspace(t, binary, workspace, args...)
	}

	stdout, stderr, code := run("-yes", "第一轮问题")
	if code != 0 || stdout != "先调用工具。\ntool: bash\narguments: "+call.Function.Arguments+"\n第一轮回答\n"+promptMarker+"\n" || stripStats(stderr) != "" {
		t.Fatalf("first run: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	id := sessionIDFromOutput(t, stderr)
	path, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("session file permissions: info=%v error=%v", info, err)
	}
	directory, err := os.Stat(filepath.Dir(path))
	if err != nil || directory.Mode().Perm() != 0o700 {
		t.Fatalf("session directory permissions: info=%v error=%v", directory, err)
	}
	snapshot, err := sessionstore.Load(path)
	if err != nil || snapshot.ID != id || len(snapshot.Messages) != 4 {
		t.Fatalf("saved snapshot = %+v, error = %v", snapshot, err)
	}
	first := readRequest(t, requests)
	afterTool := readRequest(t, requests)
	if len(first.Messages) != 2 || !reflect.DeepEqual(afterTool.Messages[1:], snapshot.Messages[:3]) {
		t.Fatalf("saved history does not preserve the tool call and result: %+v", snapshot.Messages)
	}

	// 恢复命令仅指定 ID，后续问题和正常启动一样从输入循环读取。
	stdout, stderr, code = runMiniCodeInWorkspaceWithInput(t, binary, workspace, "第二轮问题\n/exit\n", "-yes", "--session", id)
	if code != 0 || stdout != promptMarker+"第二轮回答\n"+promptMarker || stripStats(stderr) != "" || sessionIDFromOutput(t, stderr) != id {
		t.Fatalf("resumed run: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	resumed := readRequest(t, requests)
	if len(resumed.Messages) != 6 || resumed.Messages[0].Role != provider.RoleSystem || !reflect.DeepEqual(resumed.Messages[1:5], snapshot.Messages) || resumed.Messages[5].Text() != "第二轮问题" {
		t.Fatalf("restored history = %+v", resumed.Messages)
	}
	updated, err := sessionstore.Load(path)
	if err != nil || updated.ID != id || len(updated.Messages) != 6 || updated.Messages[5].Text() != "第二轮回答" {
		t.Fatalf("continued snapshot = %+v, error = %v", updated, err)
	}

	stdout, stderr, code = run("新会话问题")
	if code != 0 || stdout != "新会话回答\n"+promptMarker+"\n" || stripStats(stderr) != "" || sessionIDFromOutput(t, stderr) == id {
		t.Fatalf("new run: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if request := readRequest(t, requests); len(request.Messages) != 2 {
		t.Fatalf("new session inherited old history: %+v", request.Messages)
	}
}

func TestMiniCode_EmptySessionCanResume(t *testing.T) {
	binary := buildMiniCode(t)
	workspace := t.TempDir()
	writeModelConfig(t, workspace, "http://127.0.0.1")
	stdout, stderr, code := runMiniCodeInWorkspaceWithInput(t, binary, workspace, "/exit\n")
	if code != 0 || stdout != promptMarker || stripStats(stderr) != "" {
		t.Fatalf("new empty session: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	id := sessionIDFromOutput(t, stderr)
	stdout, stderr, code = runMiniCodeInWorkspaceWithInput(t, binary, workspace, "/exit\n", "--session", id)
	if code != 0 || stdout != promptMarker || stripStats(stderr) != "" || sessionIDFromOutput(t, stderr) != id {
		t.Fatalf("resume empty session: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMiniCode_SessionIDErrors(t *testing.T) {
	binary := buildMiniCode(t)
	const id = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name string
		args []string
		want string
		code int
	}{
		{name: "invalid ID", args: []string{"--session", "../outside"}, want: "invalid session ID", code: 1},
		{name: "file path rejected", args: []string{"--session", "custom.json"}, want: "invalid session ID", code: 1},
		{name: "missing session", args: []string{"--session", id}, want: "resume session " + id, code: 1},
		{name: "empty ID", args: []string{"--session", ""}, want: "requires a non-empty ID", code: 2},
		{name: "removed resume flag", args: []string{"--resume", id}, want: "flag provided but not defined: -resume", code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeModelConfig(t, workspace, "http://127.0.0.1")
			stdout, stderr, code := runMiniCodeInWorkspace(t, binary, workspace, tc.args...)
			if code != tc.code || stdout != "" || !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "[会话]") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(filepath.Join(workspace, ".minicode")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed resume created a session directory: %v", err)
			}
		})
	}

	t.Run("stored ID mismatch", func(t *testing.T) {
		workspace := t.TempDir()
		writeModelConfig(t, workspace, "http://127.0.0.1")
		path, err := sessionstore.Path(workspace, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		snapshot := sessionstore.Snapshot{
			Version: sessionstore.Version, ID: "22222222-2222-4222-8222-222222222222",
			Workspace: workspace, LastTurnStatus: "completed",
		}
		if err := sessionstore.Save(path, snapshot); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runMiniCodeInWorkspace(t, binary, workspace, "--session", id)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "ID mismatch") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		unchanged, err := sessionstore.Load(path)
		if err != nil || !reflect.DeepEqual(unchanged, snapshot) {
			t.Fatalf("failed resume modified snapshot: %+v, error = %v", unchanged, err)
		}
	})

	t.Run("session directory cannot be created", func(t *testing.T) {
		workspace := t.TempDir()
		writeModelConfig(t, workspace, "http://127.0.0.1")
		if err := os.WriteFile(filepath.Join(workspace, ".minicode"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runMiniCodeInWorkspace(t, binary, workspace, "/exit")
		if code != 1 || stdout != "" || !strings.Contains(stderr, "create session directory") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
}

func sessionIDFromOutput(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if id, ok := strings.CutPrefix(line, "[会话] "); ok {
			if err := sessionstore.ValidateID(id); err != nil {
				t.Fatal(err)
			}
			return id
		}
	}
	t.Fatalf("no session ID in output: %q", output)
	return ""
}

func runMiniCodeInWorkspace(t *testing.T, binary, workspace string, args ...string) (string, string, int) {
	t.Helper()
	return runMiniCodeInWorkspaceWithInput(t, binary, workspace, "", args...)
}

func runMiniCodeInWorkspaceWithInput(t *testing.T, binary, workspace, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = workspace
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
