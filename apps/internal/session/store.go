// Package session 负责版本化会话快照的校验、读取和原子写入。
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/google/uuid"
)

const (
	// Version 是当前 Session 文件格式版本。
	Version             = 1
	maxSessionFileBytes = 64 << 20
)

// Snapshot 是磁盘上的会话快照。系统 Prompt 不落盘，由 Agent 恢复时重新生成。
type Snapshot struct {
	Version        int                `json:"version"`
	ID             string             `json:"id"`
	Workspace      string             `json:"workspace"`
	LastTurnStatus string             `json:"last_turn_status"`
	Messages       []provider.Message `json:"messages"`
}

// Load 读取并严格校验一个 Session 文件。
func Load(path string) (Snapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Snapshot{}, fmt.Errorf("load session: stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, errors.New("load session: path must name a regular file")
	}
	if info.Size() > maxSessionFileBytes {
		return Snapshot{}, fmt.Errorf("load session: file exceeds %d byte limit", maxSessionFileBytes)
	}

	var snapshot Snapshot
	decoder := json.NewDecoder(io.LimitReader(file, maxSessionFileBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("load session: decode JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Snapshot{}, fmt.Errorf("load session: decode JSON: %w", err)
	}
	if err := Validate(snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("load session: %w", err)
	}
	return snapshot, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values")
}

// Validate 检查快照版本、元数据以及工具调用与结果的协议配对。
func Validate(snapshot Snapshot) error {
	if snapshot.Version != Version {
		return fmt.Errorf("unsupported version %d (expected %d)", snapshot.Version, Version)
	}
	// 旧版 v1 文件没有 ID；允许读取，恢复后由 Agent 补齐并保存。
	if snapshot.ID != "" {
		if err := ValidateID(snapshot.ID); err != nil {
			return err
		}
	}
	if snapshot.LastTurnStatus != "completed" && snapshot.LastTurnStatus != "failed" {
		return fmt.Errorf("invalid last_turn_status %q", snapshot.LastTurnStatus)
	}
	if strings.TrimSpace(snapshot.Workspace) == "" {
		return errors.New("workspace is empty")
	}
	return validateMessages(snapshot.Messages)
}

// ValidateID 要求会话 ID 为标准小写 UUID，便于安全地用作文件名。
func ValidateID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("invalid session ID %q", id)
	}
	return nil
}

// Path 返回工作区内按 ID 存储的会话路径。
func Path(workspace, id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return filepath.Join(workspace, ".minicode", "sessions", id+".json"), nil
}

func validateMessages(messages []provider.Message) error {
	pending := map[string]struct{}{}
	for index, message := range messages {
		if len(pending) > 0 {
			if message.Role != provider.RoleTool {
				return fmt.Errorf("message %d: assistant tool calls are missing results", index)
			}
			if _, ok := pending[message.ToolCallID]; !ok {
				return fmt.Errorf("message %d: unexpected or duplicate tool result ID %q", index, message.ToolCallID)
			}
			if len(message.ToolCalls) != 0 || message.Content == nil {
				return fmt.Errorf("message %d: invalid tool result", index)
			}
			delete(pending, message.ToolCallID)
			continue
		}

		switch message.Role {
		case provider.RoleUser:
			if message.Content == nil || len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				return fmt.Errorf("message %d: invalid user message", index)
			}
		case provider.RoleAssistant:
			if message.ToolCallID != "" || (message.Content == nil && len(message.ToolCalls) == 0) {
				return fmt.Errorf("message %d: invalid assistant message", index)
			}
			for callIndex, call := range message.ToolCalls {
				if err := call.Validate(); err != nil {
					return fmt.Errorf("message %d tool call %d: %w", index, callIndex, err)
				}
				if _, exists := pending[call.ID]; exists {
					return fmt.Errorf("message %d: duplicate tool call ID %q", index, call.ID)
				}
				pending[call.ID] = struct{}{}
			}
		case provider.RoleTool:
			return fmt.Errorf("message %d: tool result has no preceding call", index)
		case provider.RoleSystem:
			return fmt.Errorf("message %d: system messages are not stored in session files", index)
		default:
			return fmt.Errorf("message %d: unsupported role %q", index, message.Role)
		}
	}
	if len(pending) > 0 {
		return errors.New("final assistant tool calls are missing results")
	}
	return nil
}

// Save 校验快照，随后在目标目录写入并同步临时文件，再原子替换目标。
func Save(path string, snapshot Snapshot) error {
	if err := ValidateID(snapshot.ID); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	if err := Validate(snapshot); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	parent := filepath.Dir(path)
	temp, err := os.CreateTemp(parent, ".minicode-session-*.tmp")
	if err != nil {
		return fmt.Errorf("save session: create temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("save session: set temporary file permissions: %w", err)
	}
	writer := bufio.NewWriter(temp)
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("save session: encode JSON: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("save session: flush temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("save session: sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("save session: close temporary file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("save session: replace file: %w", err)
	}
	return nil
}
