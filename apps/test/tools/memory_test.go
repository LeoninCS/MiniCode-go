package tools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/memory"
)

func TestProjectMemory_WriteValidatesCandidateBeforeChangingDisk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		message string
	}{
		{name: "invalid UTF-8", content: "\xff", message: "UTF-8"},
		{name: "NUL", content: "a\x00b", message: "UTF-8"},
		{name: "byte limit", content: strings.Repeat("a", memory.MaxFileBytes+1), message: "byte limit"},
		{name: "ASCII budget", content: strings.Repeat("a", memory.DefaultTokenBudget*4+1), message: "token budget"},
		{name: "Chinese budget", content: strings.Repeat("中", memory.DefaultTokenBudget/2+1), message: "token budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, existing := range []bool{false, true} {
				files, workspace := newFileTools(t)
				if existing {
					putFile(t, workspace, memory.FileName, "preserved memory")
				}
				if _, err := files.WriteFile(context.Background(), memory.FileName, tc.content); err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("invalid candidate accepted: %v", err)
				}
				if existing {
					assertFile(t, workspace, memory.FileName, "preserved memory")
				} else if _, err := os.Stat(filepath.Join(workspace, memory.FileName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed write created a file: %v", err)
				}
				// write 保留原有直接覆盖能力，无需新增读取前置条件。
				if _, err := files.WriteFile(context.Background(), memory.FileName, "valid memory"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	files, workspace := newFileTools(t)
	content := strings.Repeat("a", memory.DefaultTokenBudget*4)
	if _, err := files.WriteFile(context.Background(), memory.FileName, content); err != nil {
		t.Fatalf("candidate at token boundary: %v", err)
	}
	doc, err := memory.Load(context.Background(), workspace, memory.DefaultTokenBudget)
	if err != nil || doc == nil || doc.EstimatedTokens != memory.DefaultTokenBudget {
		t.Fatalf("load saved boundary candidate = %+v, %v", doc, err)
	}
}

func TestProjectMemory_EditValidatesWholeCandidateAndDeletesEntries(t *testing.T) {
	files, workspace := newFileTools(t)
	ctx := context.Background()
	// 替换片段自身很小，但合计原文后超出注入预算。
	original := strings.Repeat("a", (memory.DefaultTokenBudget-1)*4) + "X"
	if _, err := files.WriteFile(ctx, memory.FileName, original); err != nil {
		t.Fatal(err)
	}
	if _, err := files.EditFile(ctx, memory.FileName, "X", "中文", false); err == nil || !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("only replacement fragment was budgeted: %v", err)
	}
	assertFile(t, workspace, memory.FileName, original)
	if _, err := files.EditFile(ctx, memory.FileName, "X", strings.Repeat("b", memory.MaxFileBytes), false); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized edit = %v", err)
	}
	assertFile(t, workspace, memory.FileName, original)
	if _, err := files.WriteFile(ctx, memory.FileName, "- 使用 Go。\n- 使用 Go。\n- 保留测试。\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := files.EditFile(ctx, memory.FileName, "- 使用 Go。\n", "", false); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("ambiguous deletion = %v", err)
	}
	if _, err := files.EditFile(ctx, memory.FileName, "- 使用 Go。\n", "", true); err != nil {
		t.Fatal(err)
	}
	assertFile(t, workspace, memory.FileName, "- 保留测试。\n")
	if _, err := files.EditFile(ctx, memory.FileName, "- 保留测试。\n", "", false); err != nil {
		t.Fatal(err)
	}
	assertFile(t, workspace, memory.FileName, "")
}
