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

	"github.com/MiniCode-go/minicode/internal/config"
	"github.com/MiniCode-go/minicode/internal/memory"
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
			if req.MaxTokens != 8192 {
				t.Errorf("max tokens limit = %d, want 8192", req.MaxTokens)
			}
			writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "loaded", ""), FinishReason: "stop"}}})
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
provider = "openai"
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
		{name: "no context window config", content: valid + "context_window = 262144\n", wantCode: 2, wantError: "cannot parse"},
		{name: "missing fields", content: "[model]\n", wantCode: 2, wantError: "model.api_key, model.base_url, model.name"},
		{name: "blank field", content: strings.Replace(valid, "'file-model'", "'  '", 1), wantCode: 2, wantError: "missing required config: model.name"},
		{name: "unknown provider", content: valid + "provider = 'unknown'\n", wantCode: 2, wantError: "unsupported provider"},
		{name: "invalid base URL", content: strings.Replace(valid, "'http://127.0.0.1'", "'/v1'", 1), wantCode: 2, wantError: "absolute HTTP or HTTPS URL"},
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
		{name: "no streaming flag", content: valid, args: []string{"--stream=false"}, wantCode: 2, wantError: "flag provided but not defined: -stream"},
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

func TestConfig_WorkspacePathAndLegacyFallback(t *testing.T) {
	valid := "[model]\napi_key = 'test-key'\nbase_url = 'http://127.0.0.1'\nname = 'workspace-model'\n"
	legacy := strings.Replace(valid, "workspace-model", "legacy-model", 1)
	for _, tc := range []struct {
		name      string
		primary   string
		legacy    string
		directory bool
		symlink   bool
		wantModel string
		wantError string
	}{
		{name: "workspace config", primary: valid, wantModel: "workspace-model"},
		{name: "legacy config", legacy: legacy, wantModel: "legacy-model"},
		{name: "workspace config wins", primary: valid, legacy: legacy, wantModel: "workspace-model"},
		{name: "invalid primary does not fall back", primary: "[model]\napi_key = \"do-not-print-this-secret\n", legacy: legacy, wantError: "cannot parse " + config.FilePath},
		{name: "missing field does not fall back", primary: "[model]\n", legacy: legacy, wantError: "missing required config"},
		{name: "directory does not fall back", directory: true, legacy: legacy, wantError: "read config"},
		{name: "broken link does not fall back", symlink: true, legacy: legacy, wantError: "read config"},
		{name: "both missing", wantError: "read config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			t.Chdir(workspace)
			if tc.primary != "" {
				writeConfigAtPath(t, workspace, config.FilePath, tc.primary)
			} else if tc.directory || tc.symlink {
				path := filepath.Join(workspace, config.FilePath)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if tc.directory {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink("missing.toml", path); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy != "" {
				writeConfigFile(t, workspace, tc.legacy)
			}
			cfg, err := config.Load()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "do-not-print-this-secret") {
					t.Fatalf("Load error = %v, want %q without secret", err, tc.wantError)
				}
			} else if err != nil || cfg.Model.Name != tc.wantModel {
				t.Fatalf("Load model = %q, error = %v, want %q", cfg.Model.Name, err, tc.wantModel)
			}
		})
	}
}

func TestMiniCode_AppsWorkspaceLoadsMemoryAndDetails(t *testing.T) {
	binary := buildMiniCode(t)
	repository := t.TempDir()
	workspace := filepath.Join(repository, "apps")
	path := memory.DetailDir + "/topic.md"
	index := "MODULE_RESIDENT_MEMORY\n- [专题](" + path + ")：分析模块时参考。\n"
	for file, content := range map[string]string{
		filepath.Join(repository, memory.FileName): "PARENT_RESIDENT_MEMORY",
		filepath.Join(repository, path):            "PARENT_DETAIL_MEMORY",
		filepath.Join(workspace, memory.FileName):  index,
		filepath.Join(workspace, path):             "MODULE_DETAIL_MEMORY",
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		for _, message := range req.Messages {
			if strings.Contains(message.Text(), "PARENT_RESIDENT_MEMORY") || strings.Contains(message.Text(), "PARENT_DETAIL_MEMORY") {
				t.Error("apps workspace read memory from its parent directory")
			}
		}
		if len(req.Messages) < 3 || req.Messages[0].Role != provider.RoleSystem || !strings.Contains(req.Messages[0].Text(), workspace) || req.Messages[1].Role != provider.RoleUser || !strings.Contains(req.Messages[1].Text(), "\n<project-memory>\n"+index+"\n</project-memory>") {
			t.Error("module memory or workspace was not injected correctly")
		}
		switch calls.Add(1) {
		case 1:
			for _, message := range req.Messages {
				if strings.Contains(message.Text(), "MODULE_DETAIL_MEMORY") {
					t.Error("detail was loaded before the model requested it")
				}
			}
			arguments, _ := json.Marshal(map[string]string{"path": path})
			call := provider.ToolCall{ID: "read-module-detail", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: string(arguments)}}
			writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}}, FinishReason: "tool_calls"}}})
		case 2:
			last := req.Messages[len(req.Messages)-1]
			if last.Role != provider.RoleTool || last.ToolCallID != "read-module-detail" || !strings.Contains(last.Text(), "MODULE_DETAIL_MEMORY") {
				t.Error("detail was not read relative to the apps workspace")
			}
			writeStreamResponse(w, provider.ChatResponse{Choices: []provider.Choice{{Message: provider.NewMessage(provider.RoleAssistant, "module memory loaded", ""), FinishReason: "stop"}}})
		default:
			t.Error("unexpected model request")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	writeConfigAtPath(t, workspace, config.FilePath, fmt.Sprintf("[model]\napi_key = 'test-key'\nbase_url = %q\nname = 'module-model'\n", srv.URL))
	cmd := exec.Command(binary, "分析模块")
	cmd.Dir = workspace
	cmd.Stdin = strings.NewReader("/exit\n")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "module memory loaded") || calls.Load() != 2 {
		t.Fatalf("apps startup = %v, calls=%d output=%q", err, calls.Load(), output)
	}
	if entries, err := os.ReadDir(filepath.Join(workspace, ".minicode", "sessions")); err != nil || len(entries) != 1 {
		t.Fatalf("apps session files=%v, error=%v", entries, err)
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
	writeConfigAtPath(t, dir, "apps/config/config.toml", content)
}

func writeConfigAtPath(t *testing.T, dir, path, content string) {
	t.Helper()
	file := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
