package memory_test

import (
	"path/filepath"
	"testing"

	"github.com/MiniCode-go/minicode/internal/memory"
)

func TestDetailPath_StaysWithinMemoryDirectory(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: ".minicode/memory/session.md", want: true},
		{path: "./.minicode/memory/compact/../session.md", want: true},
		{path: ".minicode/memory/nested/topic.md", want: true},
		{path: ".minicode/memory"},
		{path: ".minicode/memory/topic.txt"},
		{path: ".minicode/memory-other/topic.md"},
		{path: ".minicode/memory/../../topic.md"},
		{path: "../.minicode/memory/topic.md"},
		{path: filepath.Join(t.TempDir(), ".minicode", "memory", "topic.md")},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := memory.IsDetailPath(tc.path); got != tc.want {
				t.Fatalf("IsDetailPath(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
}
