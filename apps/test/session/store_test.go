package session_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

func snapshot() sessionstore.Snapshot {
	call := provider.ToolCall{ID: "read", Type: provider.ToolTypeFunction, Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"file.go"}`}}
	return sessionstore.Snapshot{
		Version: sessionstore.Version, ID: "11111111-1111-4111-8111-111111111111", Workspace: "/workspace", LastTurnStatus: "completed",
		Messages: []provider.Message{
			provider.NewMessage(provider.RoleUser, "old request", ""),
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}},
			provider.NewMessage(provider.RoleTool, "file contents", call.ID),
			provider.NewMessage(provider.RoleAssistant, "done", ""),
		},
	}
}

func writeJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStore_PreservesRawHistoryAndIndependentCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	want := snapshot()
	want.Messages[0] = provider.NewMessage(provider.RoleUser, strings.Repeat("x", 1200000), "")
	want.Compaction = &compaction.State{Summary: "read file.go", FirstKeptMessageIndex: 1}
	if err := sessionstore.Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := sessionstore.Load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("raw history/checkpoint roundtrip: err=%v", err)
	}
	context := got.Compaction.ContextMessages(got.Messages)
	if len(context) != 4 || !strings.Contains(context[0].Text(), want.Compaction.Summary) || !reflect.DeepEqual(context[1:], want.Messages[1:]) {
		t.Fatal("model context did not select checkpoint and retained history")
	}
	for _, message := range context {
		if strings.Contains(message.Text(), strings.Repeat("x", 1000)) {
			t.Fatal("archived old input leaked into model context")
		}
	}
}

func TestLoad_UpgradesV1WithAndWithoutSummary(t *testing.T) {
	for _, hasSummary := range []bool{false, true} {
		name := "uncompacted"
		if hasSummary {
			name = "compacted"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.json")
			legacy := snapshot()
			legacy.Version = 1
			wantHistory := legacy.Messages
			if hasSummary {
				legacy.Messages = (&compaction.State{Summary: "legacy checkpoint"}).ContextMessages(wantHistory)
			}
			original := writeJSON(t, path, legacy)
			got, err := sessionstore.Load(path)
			if err != nil || got.Version != sessionstore.Version || !reflect.DeepEqual(got.Messages, wantHistory) {
				t.Fatalf("load legacy: version=%d err=%v", got.Version, err)
			}
			if hasSummary {
				if got.Compaction == nil || got.Compaction.Summary != "legacy checkpoint" || got.Compaction.FirstKeptMessageIndex != 0 || !reflect.DeepEqual(got.Compaction.ContextMessages(got.Messages), legacy.Messages) {
					t.Fatal("legacy summary was not moved into checkpoint without changing model context")
				}
			} else if got.Compaction != nil {
				t.Fatal("uncompacted legacy history acquired a summary")
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, original) {
				t.Fatal("loading legacy file modified disk contents")
			}
			if err := sessionstore.Save(path, got); err != nil {
				t.Fatal(err)
			}
			migrated, err := sessionstore.Load(path)
			if err != nil || !reflect.DeepEqual(migrated, got) {
				t.Fatalf("migrated roundtrip: %v", err)
			}
		})
	}
}

func TestStore_RejectsInvalidCheckpointWithoutOverwritingHistory(t *testing.T) {
	for _, checkpoint := range []*compaction.State{
		{Summary: " "},
		{Summary: "checkpoint", FirstKeptMessageIndex: -1},
		{Summary: "checkpoint", FirstKeptMessageIndex: 5},
		{Summary: "checkpoint", FirstKeptMessageIndex: 2},
	} {
		path := filepath.Join(t.TempDir(), "session.json")
		original := snapshot()
		if err := sessionstore.Save(path, original); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		invalid := original
		invalid.Compaction = checkpoint
		if err := sessionstore.Save(path, invalid); err == nil {
			t.Fatal("invalid checkpoint was saved")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, data) {
			t.Fatal("failed checkpoint save overwrote original history")
		}
		writeJSON(t, path, invalid)
		if _, err := sessionstore.Load(path); err == nil {
			t.Fatal("invalid checkpoint was loaded")
		}
	}
}

func TestSave_RejectsUnloadableSizeAndKeepsPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	original := snapshot()
	if err := sessionstore.Save(path, original); err != nil {
		t.Fatal(err)
	}
	large := original
	large.Messages = []provider.Message{provider.NewMessage(provider.RoleUser, strings.Repeat("x", 64<<20), "")}
	if err := sessionstore.Save(path, large); err == nil || !strings.Contains(err.Error(), "file exceeds") {
		t.Fatalf("save exceeded readable file limit: %v", err)
	}
	got, err := sessionstore.Load(path)
	if err != nil || !reflect.DeepEqual(got, original) {
		t.Fatalf("oversized save damaged previous file: %v", err)
	}
}
