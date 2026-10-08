package cli_test

import (
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
)

func TestMiniCode_Config(t *testing.T) {
	binary := buildMiniCode(t)
	var modelCalls, traceCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			modelCalls.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer file#key" {
				t.Errorf("model authorization = %q", got)
			}
			var req provider.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode model request: %v", err)
			}
			if req.Model != "file-model" {
				t.Errorf("model = %q", req.Model)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"loaded"},"finish_reason":"stop"}]}`)
		case "/api/public/otel/v1/traces":
			traceCalls.Add(1)
			public, secret, ok := r.BasicAuth()
			if !ok || public != "file-public" || secret != "file-secret" {
				t.Error("unexpected Langfuse credentials")
			}
			if body, err := io.ReadAll(r.Body); err != nil || len(body) == 0 {
				t.Errorf("empty or unreadable trace request: %v", err)
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	cmd := exec.Command(binary, "check config")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), legacyConfigEnv()...)
	writeConfigFile(t, cmd.Dir, fmt.Sprintf(`[model]
api_key = 'file#key'
base_url = %q
name = "file-model" # inline comment

[langfuse]
public_key = "file-public"
secret_key = "file-secret"
host = %q
`, srv.URL, srv.URL))
	// 旧 .env 即使无法解析也不能影响 TOML 配置。
	if err := os.WriteFile(filepath.Join(cmd.Dir, ".env"), []byte("MINICODE_API_KEY=\"unterminated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "loaded") {
		t.Fatalf("run: %v, output = %q", err, output)
	}
	if modelCalls.Load() != 1 || traceCalls.Load() == 0 {
		t.Fatalf("model requests = %d, trace requests = %d", modelCalls.Load(), traceCalls.Load())
	}
}

func TestMiniCode_ConfigErrors(t *testing.T) {
	binary := buildMiniCode(t)
	valid := "[model]\napi_key = 'file-key'\nbase_url = 'http://127.0.0.1'\nname = 'file-model'\n"
	invalid := "[model]\napi_key = \"do-not-print-this-secret\n"
	for _, tc := range []struct {
		name      string
		content   string
		directory bool
		args      []string
		wantCode  int
		wantError string
	}{
		{name: "missing file does not use environment", wantCode: 2, wantError: "read config"},
		{name: "model only disables tracing", content: valid},
		{name: "missing fields", content: "[model]\n", wantCode: 2, wantError: "model.api_key, model.base_url, model.name"},
		{name: "blank field", content: strings.Replace(valid, "'file-model'", "'  '", 1), wantCode: 2, wantError: "missing required config: model.name"},
		{name: "invalid syntax", content: invalid, wantCode: 2, wantError: "cannot parse"},
		{name: "invalid type", content: strings.Replace(valid, "'file-model'", "123", 1), wantCode: 2, wantError: "cannot parse"},
		{name: "unknown field", content: valid + "[langfuse]\npublic_keey = 'typo'\n", wantCode: 2, wantError: "cannot parse"},
		{name: "incomplete Langfuse config", content: valid + "[langfuse]\npublic_key = 'pk'\n", wantCode: 2, wantError: "public_key and secret_key must both be set"},
		{name: "unreadable file", directory: true, wantCode: 2, wantError: "read config"},
		{name: "help ignores invalid file", content: invalid, args: []string{"-h"}},
		{name: "no api key flag", content: valid, args: []string{"-api-key", "override"}, wantCode: 2, wantError: "flag provided but not defined"},
		{name: "no base url flag", content: valid, args: []string{"-base-url", "override"}, wantCode: 2, wantError: "flag provided but not defined"},
		{name: "no model flag", content: valid, args: []string{"-model", "override"}, wantCode: 2, wantError: "flag provided but not defined"},
		{name: "no config path flag", content: valid, args: []string{"-config", "other.toml"}, wantCode: 2, wantError: "flag provided but not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), legacyConfigEnv()...)
			if tc.directory {
				if err := os.MkdirAll(filepath.Join(cmd.Dir, "apps", "config", "config.toml"), 0700); err != nil {
					t.Fatal(err)
				}
			} else if tc.content != "" {
				writeConfigFile(t, cmd.Dir, tc.content)
			}
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil {
				t.Fatalf("start minicode: %v", err)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.wantCode || !strings.Contains(string(output), tc.wantError) {
				t.Fatalf("exit = %d, output = %q", code, output)
			}
			if strings.Contains(string(output), "do-not-print-this-secret") {
				t.Fatal("config error exposed a secret")
			}
		})
	}
}

// legacyConfigEnv 故意设置旧配置，验证它们不会覆盖 TOML 或成为回退来源。
func legacyConfigEnv() []string {
	return []string{
		"MINICODE_API_KEY=legacy-key", "MINICODE_BASE_URL=http://127.0.0.1:1", "MINICODE_MODEL=legacy-model",
		"LANGFUSE_PUBLIC_KEY=legacy-public", "LANGFUSE_SECRET_KEY=", "LANGFUSE_HOST=http://127.0.0.1:1",
	}
}

func writeModelConfig(t *testing.T, dir, baseURL string) {
	t.Helper()
	writeConfigFile(t, dir, fmt.Sprintf("[model]\napi_key = 'test-key'\nbase_url = %q\nname = 'test-model'\n", baseURL))
}

func writeConfigFile(t *testing.T, dir, content string) {
	t.Helper()
	configDir := filepath.Join(dir, "apps", "config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
