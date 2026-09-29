package tools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_CreatesAndOverwrites(t *testing.T) {
	files, workspace := newFileTools(t)
	ctx := context.Background()
	for _, content := range []string{"中文\n'$HOME' `literal`\n", "shorter", ""} {
		result, err := files.WriteFile(ctx, "nested/unused/../new/file.txt", content)
		if err != nil || !strings.Contains(result, "nested/new/file.txt") {
			t.Fatalf("write = %q, %v", result, err)
		}
		assertFile(t, workspace, "nested/new/file.txt", content)
	}
	info, err := os.Stat(filepath.Join(workspace, "nested/new/file.txt"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("new file permissions: %v, %v", info, err)
	}
}

func TestWriteFile_RejectsInvalidContentAndTargets(t *testing.T) {
	files, workspace := newFileTools(t)
	putFile(t, workspace, "keep.txt", "original")
	ctx := context.Background()
	for _, content := range []string{"\x00", "\xff", strings.Repeat("a", (1<<20)+1)} {
		if _, err := files.WriteFile(ctx, "keep.txt", content); err == nil {
			t.Fatal("invalid content accepted")
		}
		assertFile(t, workspace, "keep.txt", "original")
	}
	if err := os.Mkdir(filepath.Join(workspace, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"directory", "keep.txt/child"} {
		if _, err := files.WriteFile(ctx, path, "changed"); err == nil {
			t.Fatalf("write accepted invalid target %s", path)
		}
	}
	assertFile(t, workspace, "keep.txt", "original")
}

func TestWriteFile_UpdatesVersionAfterSuccess(t *testing.T) {
	files, workspace := newFileTools(t)
	ctx := context.Background()
	if _, err := files.WriteFile(ctx, "./input.txt", "original"); err != nil {
		t.Fatal(err)
	}
	// 新建文件后可直接编辑,无需额外调用 ReadFile。
	if _, err := files.EditFile(ctx, "input.txt", "original", "edited"); err != nil {
		t.Fatalf("edit newly written file: %v", err)
	}
	assertFile(t, workspace, "input.txt", "edited")

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := files.WriteFile(canceled, "input.txt", "canceled write"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	// 写入失败必须保留已有版本,后续仍能基于原内容编辑。
	if _, err := files.EditFile(ctx, "input.txt", "edited", "current"); err != nil {
		t.Fatalf("edit after failed write: %v", err)
	}
	assertFile(t, workspace, "input.txt", "current")

	if _, err := files.WriteFile(ctx, "input.txt", "written"); err != nil {
		t.Fatal(err)
	}
	putFile(t, workspace, "input.txt", "written externally")
	if _, err := files.EditFile(ctx, "input.txt", "written", "changed"); err == nil || !strings.Contains(err.Error(), "changed since last read or write") {
		t.Fatalf("edit should reject external changes after write: %v", err)
	}
	assertFile(t, workspace, "input.txt", "written externally")
}
