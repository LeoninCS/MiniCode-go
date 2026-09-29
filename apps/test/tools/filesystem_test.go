package tools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/tools"
)

func newFileTools(t *testing.T) (*tools.FileTools, string) {
	t.Helper()
	workspace := t.TempDir()
	files, err := tools.NewFileTools(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := files.Close(); err != nil {
			t.Error(err)
		}
	})
	return files, workspace
}

func putFile(t *testing.T, workspace, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, workspace, path, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(workspace, path))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("file %s = %q, want %q", path, got, want)
	}
}

func TestFileTools_InvalidWorkspace(t *testing.T) {
	if _, err := tools.NewFileTools(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want missing directory", err)
	}
}

func TestFileTools_RejectsInvalidPaths(t *testing.T) {
	files, workspace := newFileTools(t)
	putFile(t, workspace, "keep.txt", "original")
	for _, path := range []string{"", " ", ".", "..", "../keep.txt", "nested/../../keep.txt", filepath.Join(workspace, "keep.txt"), "bad\x00path"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			if _, err := files.ReadFile(ctx, path, 0, 0); err == nil {
				t.Fatal("read accepted invalid path")
			}
			if _, err := files.WriteFile(ctx, path, "changed"); err == nil {
				t.Fatal("write accepted invalid path")
			}
			if _, err := files.EditFile(ctx, path, "original", "changed"); err == nil {
				t.Fatal("edit accepted invalid path")
			}
		})
	}
	assertFile(t, workspace, "keep.txt", "original")
}

func TestFileTools_SymbolicLinks(t *testing.T) {
	files, workspace := newFileTools(t)
	outside := t.TempDir()
	putFile(t, outside, "keep.txt", "outside")
	if err := os.Symlink(outside, filepath.Join(workspace, "external")); err != nil {
		t.Fatal(err)
	}
	// 相对链接同样不得越过工作区边界。
	relativeOutside, err := filepath.Rel(workspace, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relativeOutside, filepath.Join(workspace, "relative-external")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, path := range []string{"external/keep.txt", "relative-external/keep.txt"} {
		if _, err := files.ReadFile(ctx, path, 0, 0); err == nil {
			t.Fatalf("read escaped through %s", path)
		}
		if _, err := files.WriteFile(ctx, path, "changed"); err == nil {
			t.Fatalf("write escaped through %s", path)
		}
		if _, err := files.EditFile(ctx, path, "outside", "changed"); err == nil {
			t.Fatalf("edit escaped through %s", path)
		}
	}
	if _, err := files.WriteFile(ctx, "external/new/file.txt", "changed"); err == nil {
		t.Fatal("write created directories outside workspace")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external directory changed: %v", err)
	}
	assertFile(t, outside, "keep.txt", "outside")

	if err := os.Mkdir(filepath.Join(workspace, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(workspace, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := files.WriteFile(ctx, "alias/inside.txt", "inside"); err != nil {
		t.Fatalf("write through internal directory link: %v", err)
	}
	if err := os.Symlink("real/inside.txt", filepath.Join(workspace, "file-link")); err != nil {
		t.Fatal(err)
	}
	if result, err := files.ReadFile(ctx, "file-link", 0, 0); err != nil || result != "1: inside\n" {
		t.Fatalf("read internal file link = %q, %v", result, err)
	}
	if _, err := files.WriteFile(ctx, "file-link", "changed"); err == nil {
		t.Fatal("write accepted a file symlink")
	}
	if _, err := files.EditFile(ctx, "file-link", "inside", "changed"); err == nil {
		t.Fatal("edit accepted a file symlink")
	}
	assertFile(t, workspace, "real/inside.txt", "inside")
}

func TestFileTools_Cancellation(t *testing.T) {
	files, workspace := newFileTools(t)
	putFile(t, workspace, "keep.txt", "original")
	if _, err := files.ReadFile(context.Background(), "keep.txt", 0, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, run := range []func() (string, error){
		func() (string, error) { return files.ReadFile(ctx, "keep.txt", 0, 0) },
		func() (string, error) { return files.WriteFile(ctx, "new/file.txt", "changed") },
		func() (string, error) { return files.WriteFile(ctx, "keep.txt", "changed") },
		func() (string, error) { return files.EditFile(ctx, "keep.txt", "original", "changed") },
	} {
		if _, err := run(); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	}
	assertFile(t, workspace, "keep.txt", "original")
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 1 {
		t.Fatalf("canceled operations left files: %v, %v", entries, err)
	}
}

func TestFileTools_AtomicReplacementPreservesPermissions(t *testing.T) {
	for _, operation := range []string{"write", "edit"} {
		t.Run(operation, func(t *testing.T) {
			files, workspace := newFileTools(t)
			putFile(t, workspace, "script.sh", "original")
			path := filepath.Join(workspace, "script.sh")
			if err := os.Chmod(path, 0o751); err != nil {
				t.Fatal(err)
			}
			// 若直接截断原文件,硬链接也会改变;原子替换应保留旧 inode 的内容。
			if err := os.Link(path, filepath.Join(workspace, "old-inode")); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := files.ReadFile(ctx, "script.sh", 0, 0); err != nil {
				t.Fatal(err)
			}
			var err error
			if operation == "write" {
				_, err = files.WriteFile(ctx, "script.sh", "changed")
			} else {
				_, err = files.EditFile(ctx, "script.sh", "original", "changed")
			}
			if err != nil {
				t.Fatal(err)
			}
			assertFile(t, workspace, "script.sh", "changed")
			assertFile(t, workspace, "old-inode", "original")
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o751 {
				t.Fatalf("file permissions not preserved: %v, %v", info, err)
			}
			entries, err := os.ReadDir(workspace)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".minicode-") {
					t.Fatalf("temporary file left behind: %s", entry.Name())
				}
			}
		})
	}
}
