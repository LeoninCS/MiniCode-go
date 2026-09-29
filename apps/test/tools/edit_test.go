package tools_test

import (
	"context"
	"strings"
	"testing"
)

func TestEditFile_ExactReplacementAndDeletion(t *testing.T) {
	files, workspace := newFileTools(t)
	putFile(t, workspace, "input.txt", "before\r\n旧文本\r\nafter\r\n")
	ctx := context.Background()
	if _, err := files.ReadFile(ctx, "./input.txt", 2, 1); err != nil {
		t.Fatal(err)
	}
	if result, err := files.EditFile(ctx, "nested/../input.txt", "旧文本", "新文本"); err != nil || !strings.Contains(result, "input.txt") {
		t.Fatalf("edit = %q, %v", result, err)
	}
	assertFile(t, workspace, "input.txt", "before\r\n新文本\r\nafter\r\n")
	// 成功编辑后更新版本,允许继续编辑;删除通过空的新文本完成。
	if _, err := files.EditFile(ctx, "input.txt", "新文本\r\n", ""); err != nil {
		t.Fatal(err)
	}
	assertFile(t, workspace, "input.txt", "before\r\nafter\r\n")
}

func TestEditFile_NormalizesLineEndings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		oldText string
		newText string
		want    string
	}{
		{
			name: "CRLF file with LF edit", content: "before\r\n旧文本\r\nafter\r\n",
			oldText: "旧文本\nafter", newText: "新文本\nadded\nafter",
			want: "before\r\n新文本\r\nadded\r\nafter\r\n",
		},
		{
			name: "LF file with CRLF edit", content: "before\nold\nafter\n",
			oldText: "old\r\nafter", newText: "new\r\nadded\r\nafter",
			want: "before\nnew\nadded\nafter\n",
		},
		{
			name: "no final newline", content: "before\r\nold\r\nafter",
			oldText: "old\nafter", newText: "new\nafter",
			want: "before\r\nnew\r\nafter",
		},
		{
			name: "no existing newline defaults to LF", content: "old",
			oldText: "old", newText: "new\r\nadded",
			want: "new\nadded",
		},
		{
			name: "mixed file starts with LF", content: "before\nold\r\nafter\r\n",
			oldText: "old\nafter", newText: "new\r\nafter",
			want: "before\nnew\nafter\n",
		},
		{
			name: "mixed file starts with CRLF", content: "before\r\nold\nafter\n",
			oldText: "old\r\nafter", newText: "new\nafter",
			want: "before\r\nnew\r\nafter\r\n",
		},
		{
			name: "bare CR normalizes to LF", content: "before\rold\rafter\r",
			oldText: "old\rafter", newText: "new\rafter",
			want: "before\nnew\nafter\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, workspace := newFileTools(t)
			putFile(t, workspace, "input.txt", tc.content)
			ctx := context.Background()
			if _, err := files.ReadFile(ctx, "input.txt", 0, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := files.EditFile(ctx, "input.txt", tc.oldText, tc.newText); err != nil {
				t.Fatal(err)
			}
			assertFile(t, workspace, "input.txt", tc.want)
		})
	}
}

func TestEditFile_RejectsExternalLineEndingChange(t *testing.T) {
	files, workspace := newFileTools(t)
	putFile(t, workspace, "input.txt", "old\r\nafter\r\n")
	ctx := context.Background()
	if _, err := files.ReadFile(ctx, "input.txt", 0, 0); err != nil {
		t.Fatal(err)
	}
	putFile(t, workspace, "input.txt", "old\nafter\n")
	if _, err := files.EditFile(ctx, "input.txt", "old\nafter", "new\nafter"); err == nil || !strings.Contains(err.Error(), "changed since last read") {
		t.Fatalf("edit after external line ending change: %v", err)
	}
	assertFile(t, workspace, "input.txt", "old\nafter\n")
}

func TestEditFile_RequiresReadAndRejectsStaleContent(t *testing.T) {
	files, workspace := newFileTools(t)
	putFile(t, workspace, "input.txt", "one\ntarget\n")
	ctx := context.Background()
	if _, err := files.EditFile(ctx, "input.txt", "target", "changed"); err == nil || !strings.Contains(err.Error(), "read_file") {
		t.Fatalf("edit without read: %v", err)
	}
	if _, err := files.ReadFile(ctx, "input.txt", 2, 1); err != nil {
		t.Fatal(err)
	}
	// 即使修改发生在未展示的行中,且文件大小相同,也应检测到版本变化。
	putFile(t, workspace, "input.txt", "two\ntarget\n")
	if _, err := files.EditFile(ctx, "input.txt", "target", "changed"); err == nil || !strings.Contains(err.Error(), "changed since last read") {
		t.Fatalf("edit stale file: %v", err)
	}
	assertFile(t, workspace, "input.txt", "two\ntarget\n")
	if _, err := files.ReadFile(ctx, "input.txt", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := files.EditFile(ctx, "input.txt", "target", "changed"); err != nil {
		t.Fatalf("edit after reread: %v", err)
	}
	assertFile(t, workspace, "input.txt", "two\nchanged\n")
	if _, err := files.WriteFile(ctx, "input.txt", "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := files.EditFile(ctx, "input.txt", "replacement", "changed"); err != nil {
		t.Fatalf("edit after full write: %v", err)
	}
	assertFile(t, workspace, "input.txt", "changed")
}

func TestEditFile_RejectsAmbiguousOrInvalidChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		oldText string
		newText string
		wantErr string
	}{
		{name: "empty old text", content: "text", wantErr: "must not be empty"},
		{name: "no match", content: "text", oldText: "missing", wantErr: "was not found"},
		{name: "multiple matches", content: "text text", oldText: "text", wantErr: "exactly once"},
		{name: "overlapping matches", content: "aaa", oldText: "aa", wantErr: "exactly once"},
		{name: "multiple normalized matches", content: "old\r\nafter\nold\nafter\n", oldText: "old\r\nafter", wantErr: "include surrounding code"},
		{name: "binary replacement", content: "text", oldText: "text", newText: "\x00", wantErr: "UTF-8"},
		{name: "invalid UTF-8", content: "text", oldText: "text", newText: "\xff", wantErr: "UTF-8"},
		{name: "oversized result", content: "ab", oldText: "a", newText: strings.Repeat("x", 1<<20), wantErr: "limit"},
		{name: "oversized CRLF result", content: "old\r\n", oldText: "old\n", newText: strings.Repeat("x\n", 1<<19), wantErr: "limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, workspace := newFileTools(t)
			putFile(t, workspace, "input.txt", tc.content)
			ctx := context.Background()
			if _, err := files.ReadFile(ctx, "input.txt", 0, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := files.EditFile(ctx, "input.txt", tc.oldText, tc.newText); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			assertFile(t, workspace, "input.txt", tc.content)
		})
	}
}
