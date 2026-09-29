package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"
)

func TestReadFile_LineRanges(t *testing.T) {
	files, workspace := newFileTools(t)
	for _, tc := range []struct {
		name    string
		content string
		offset  int
		limit   int
		want    string
	}{
		{name: "default", content: "一\n二\n", want: "1: 一\n2: 二\n"},
		{name: "no trailing newline", content: "one\ntwo", want: "1: one\n2: two\n"},
		{name: "range", content: "one\ntwo\nthree\nfour\n", offset: 2, limit: 2, want: "2: two\n3: three\n\n[output truncated]\n"},
		{name: "last line", content: "one\ntwo\n", offset: 2, limit: 20, want: "2: two\n"},
		{name: "blank lines", content: "\n\n", want: "1: \n2: \n"},
		{name: "empty", content: "", want: "[empty file]"},
		{name: "CRLF", content: "one\r\ntwo\r\n", want: "1: one\r\n2: two\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putFile(t, workspace, "input.txt", tc.content)
			got, err := files.ReadFile(context.Background(), "input.txt", tc.offset, tc.limit)
			if err != nil || got != tc.want {
				t.Fatalf("read = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestReadFile_OutputLimits(t *testing.T) {
	files, workspace := newFileTools(t)
	ctx := context.Background()
	putFile(t, workspace, "lines.txt", strings.Repeat("line\n", 201))
	result, err := files.ReadFile(ctx, "lines.txt", 0, 0)
	if err != nil || !strings.Contains(result, "200: line\n") || strings.Contains(result, "201:") || !strings.Contains(result, "[output truncated]") {
		t.Fatalf("default line limit failed: %q, %v", result, err)
	}
	result, err = files.ReadFile(ctx, "lines.txt", 201, 0)
	if err != nil || result != "201: line\n" {
		t.Fatalf("continuation = %q, %v", result, err)
	}
	putFile(t, workspace, "long.txt", strings.Repeat("中文", 20000))
	result, err = files.ReadFile(ctx, "long.txt", 0, 0)
	if err != nil || !utf8.ValidString(result) || len(result) > 64*1024+len("\n[output truncated]\n") || !strings.HasSuffix(result, "\n[output truncated]\n") {
		t.Fatalf("byte limit failed: length %d, valid UTF-8 %v, error %v", len(result), utf8.ValidString(result), err)
	}
}

func TestReadFile_RejectsInvalidInput(t *testing.T) {
	files, workspace := newFileTools(t)
	for _, tc := range []struct {
		name    string
		content string
		offset  int
		limit   int
	}{
		{name: "negative offset", content: "text", offset: -1},
		{name: "negative limit", content: "text", limit: -1},
		{name: "past end", content: "text\n", offset: 2},
		{name: "empty past end", offset: 2},
		{name: "binary", content: "a\x00b"},
		{name: "invalid UTF-8", content: "a\xffb"},
		{name: "oversized", content: strings.Repeat("a", (1<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putFile(t, workspace, "input.txt", tc.content)
			if _, err := files.ReadFile(context.Background(), "input.txt", tc.offset, tc.limit); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	if err := os.Mkdir(filepath.Join(workspace, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(workspace, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"missing", "directory", "pipe"} {
		if _, err := files.ReadFile(context.Background(), path, 0, 0); err == nil {
			t.Fatalf("read accepted %s", path)
		}
	}
}
