package minicode_test

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
	"testing"

	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestMiniCode_Day2Responses(t *testing.T) {
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
		assertBashSchema(t, req.Tools)
	})

	t.Run("tool call response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"go test ./...\"}"}}]},"finish_reason":"tool_calls"}]}`)
		}))
		defer srv.Close()

		stdout, stderr, exitCode := runMiniCode(t, binary, srv.URL)
		if exitCode != 0 {
			t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
		}
		want := "tool: bash\narguments: {\"command\":\"go test ./...\"}\n"
		if stdout != want || stderr != "" {
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

func buildMiniCode(t *testing.T) string {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	moduleRoot := filepath.Clean(filepath.Join(workingDir, "..", "..", ".."))
	binary := filepath.Join(t.TempDir(), "minicode")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/minicode")
	cmd.Dir = moduleRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build minicode: %v\n%s", err, output)
	}
	return binary
}

func runMiniCode(t *testing.T, binary, baseURL string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(
		binary,
		"-api-key", "test-key",
		"-base-url", baseURL,
		"-model", "test-model",
		"run tests",
	)
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

func assertBashSchema(t *testing.T, tools []provider.Tool) {
	t.Helper()
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", tools)
	}
	tool := tools[0]
	if tool.Type != provider.ToolTypeFunction || tool.Function.Name != "bash" {
		t.Fatalf("tool = %+v", tool)
	}
	properties, ok := tool.Function.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", tool.Function.Parameters["properties"])
	}
	command, ok := properties["command"].(map[string]any)
	if !ok || command["type"] != "string" {
		t.Fatalf("command schema = %#v", properties["command"])
	}
}
