package memory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MiniCode-go/minicode/internal/memory"
)

func writeMemory(t *testing.T, workspace, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, memory.FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_PreservesOriginalText(t *testing.T) {
	workspace := t.TempDir()
	content := "\ufeff# 项目记忆\r\n\r\n使用 Go，保留 emoji 🙂。\r\n"
	writeMemory(t, workspace, content)
	doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil || doc.Content != content {
		t.Fatalf("Load = %+v, want unchanged original text", doc)
	}
	if doc.EstimatedTokens <= 0 {
		t.Fatalf("EstimatedTokens = %d, want positive estimate", doc.EstimatedTokens)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	parent := t.TempDir()
	writeMemory(t, parent, "parent memory")
	workspace := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeMemory(t, filepath.Join(workspace, "nested"), "nested memory")
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("other instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := memory.Load(context.Background(), workspace, 0)
	if err != nil || doc != nil {
		t.Fatalf("missing memory = %+v, %v, want nil, nil", doc, err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, memory.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load created a memory file: %v", err)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	workspace := t.TempDir()
	writeMemory(t, workspace, "")
	doc, err := memory.Load(context.Background(), workspace, 0)
	if err != nil || doc == nil || doc.Content != "" || doc.EstimatedTokens != 0 {
		t.Fatalf("empty memory = %+v, %v, want empty Document", doc, err)
	}
}

func TestLoad_TokenBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		budget  int
		tokens  int
		wantErr bool
	}{
		{name: "ASCII exact", content: "abcdefghijklmnop", budget: 4, tokens: 4},
		{name: "ASCII rounds up", content: "abcdefghijklmnopq", budget: 5, tokens: 5},
		{name: "ASCII over", content: "abcdefghijklmnopq", budget: 4, wantErr: true},
		{name: "Chinese exact", content: "中文", budget: 4, tokens: 4},
		{name: "Chinese over", content: "中文", budget: 3, wantErr: true},
		{name: "mixed exact", content: "abcd中🙂", budget: 5, tokens: 5},
		{name: "mixed over", content: "abcd中🙂", budget: 4, wantErr: true},
		{name: "zero budget", content: "a", budget: 0, wantErr: true},
		{name: "negative budget", budget: -1, wantErr: true},
		{name: "default exact", content: strings.Repeat("a", memory.DefaultTokenBudget*4), budget: memory.DefaultTokenBudget, tokens: memory.DefaultTokenBudget},
		{name: "default over", content: strings.Repeat("a", memory.DefaultTokenBudget*4+1), budget: memory.DefaultTokenBudget, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeMemory(t, workspace, tc.content)
			doc, err := memory.Load(context.Background(), workspace, tc.budget)
			if tc.wantErr {
				if err == nil || doc != nil {
					t.Fatalf("over-budget memory = %+v, %v, want nil, error", doc, err)
				}
				if tc.budget >= 0 && !strings.Contains(err.Error(), "token budget") {
					t.Fatalf("expected budget error, got %v", err)
				}
				return
			}
			if err != nil || doc == nil || doc.Content != tc.content || doc.EstimatedTokens != tc.tokens {
				t.Fatalf("Load = %+v, %v, want original content and %d tokens", doc, err, tc.tokens)
			}
		})
	}
}

func TestLoad_FileSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "exact byte limit", content: strings.Repeat("a", memory.MaxFileBytes)},
		{name: "one byte over", content: strings.Repeat("a", memory.MaxFileBytes+1), wantErr: true},
		{name: "multibyte over", content: strings.Repeat("中", memory.MaxFileBytes/3+1), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeMemory(t, workspace, tc.content)
			// 提高 token 预算，单独验证字节上限。
			doc, err := memory.Load(context.Background(), workspace, memory.MaxFileBytes)
			if tc.wantErr {
				if err == nil || doc != nil || !strings.Contains(err.Error(), "byte limit") {
					t.Fatalf("oversized memory = %+v, %v, want nil, size error", doc, err)
				}
				return
			}
			if err != nil || doc == nil || doc.Content != tc.content {
				t.Fatalf("memory at byte limit failed: %v", err)
			}
		})
	}
}

func TestLoad_RejectsInvalidText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		message string
	}{
		{name: "invalid UTF-8", content: "a\xffb", message: "UTF-8"},
		{name: "incomplete UTF-8", content: "\xe4\xb8", message: "UTF-8"},
		{name: "NUL", content: "a\x00b", message: "NUL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeMemory(t, workspace, tc.content)
			doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
			if err == nil || doc != nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid text = %+v, %v, want nil, %s error", doc, err, tc.message)
			}
		})
	}
}

func TestLoad_WorkspaceIsolationAndReload(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeMemory(t, first, "first project")
	writeMemory(t, second, "second project")
	// 加载路径由 workspace 决定，不使用进程当前目录。
	t.Chdir(second)
	for _, tc := range []struct {
		workspace string
		want      string
	}{
		{first, "first project"},
		{second, "second project"},
	} {
		doc, err := memory.Load(context.Background(), tc.workspace, memory.DefaultTokenBudget)
		if err != nil || doc == nil || doc.Content != tc.want {
			t.Fatalf("workspace memory = %+v, %v, want %q", doc, err, tc.want)
		}
	}
	writeMemory(t, first, "updated project")
	doc, err := memory.Load(context.Background(), first, memory.DefaultTokenBudget)
	if err != nil || doc == nil || doc.Content != "updated project" {
		t.Fatalf("updated memory = %+v, %v", doc, err)
	}
	if err := os.Remove(filepath.Join(first, memory.FileName)); err != nil {
		t.Fatal(err)
	}
	doc, err = memory.Load(context.Background(), first, memory.DefaultTokenBudget)
	if err != nil || doc != nil {
		t.Fatalf("deleted memory = %+v, %v, want nil, nil", doc, err)
	}
}

func TestLoad_InvalidWorkspace(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "file")
	if err := os.WriteFile(file, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", " \t", filepath.Join(workspace, "missing"), file} {
		t.Run(path, func(t *testing.T) {
			doc, err := memory.Load(context.Background(), path, memory.DefaultTokenBudget)
			if err == nil || doc != nil {
				t.Fatalf("invalid workspace = %+v, %v, want nil, error", doc, err)
			}
		})
	}
	_, err := memory.Load(context.Background(), filepath.Join(workspace, "missing"), memory.DefaultTokenBudget)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace error cause lost: %v", err)
	}
}

func TestLoad_SymbolicLinks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "inside workspace", target: "notes.md"},
		{name: "outside workspace", target: "../notes.md", wantErr: true},
		{name: "dangling link", target: "missing.md", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			workspace := filepath.Join(parent, "workspace")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(parent, "notes.md"), filepath.Join(workspace, "notes.md")} {
				if err := os.WriteFile(path, []byte("notes"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(tc.target, filepath.Join(workspace, memory.FileName)); err != nil {
				t.Fatal(err)
			}
			doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
			if tc.wantErr {
				if err == nil || doc != nil {
					t.Fatalf("invalid symlink = %+v, %v, want nil, error", doc, err)
				}
				return
			}
			if err != nil || doc == nil || doc.Content != "notes" {
				t.Fatalf("inside-workspace link = %+v, %v", doc, err)
			}
		})
	}
}

func TestLoad_RejectsNonRegularFiles(t *testing.T) {
	for _, name := range []string{"directory", "FIFO"} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(workspace, memory.FileName)
			var err error
			if name == "directory" {
				err = os.Mkdir(path, 0o700)
			} else {
				err = syscall.Mkfifo(path, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
			if err == nil || doc != nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("non-regular memory = %+v, %v, want nil, regular-file error", doc, err)
			}
		})
	}
}

func TestLoad_ReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of permission bits")
	}
	workspace := t.TempDir()
	writeMemory(t, workspace, "private memory")
	if err := os.Chmod(filepath.Join(workspace, memory.FileName), 0); err != nil {
		t.Fatal(err)
	}
	doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
	if doc != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unreadable memory = %+v, %v, want nil, permission error", doc, err)
	}
}

func TestLoad_CancelledContext(t *testing.T) {
	workspace := t.TempDir()
	writeMemory(t, workspace, "memory")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doc, err := memory.Load(ctx, workspace, memory.DefaultTokenBudget)
	if doc != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled load = %+v, %v, want nil, context.Canceled", doc, err)
	}
}
