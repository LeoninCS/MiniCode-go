package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	maxFileBytes     = 1 << 20
	defaultReadLines = 200
	filePermissions  = 0o644
	dirPermissions   = 0o755
)

// FileTools 在固定工作区内读取和修改文本文件,记录最近一次读取或成功写入的内容版本。
// 同一个实例应顺序调用;任务结束后由调用方调用 Close。
type FileTools struct {
	root         *os.Root
	readVersions map[string][sha256.Size]byte
}

// NewFileTools 打开工作区,文件路径均相对于该目录,不依赖后续进程工作目录变化。
func NewFileTools(workspace string) (*FileTools, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, fmt.Errorf("file tools: open workspace: %w", err)
	}
	return &FileTools{root: root, readVersions: make(map[string][sha256.Size]byte)}, nil
}

// Close 释放工作区句柄。
func (f *FileTools) Close() error {
	if err := f.root.Close(); err != nil {
		return fmt.Errorf("file tools: close workspace: %w", err)
	}
	return nil
}

// cleanFilePath 统一文件路径,拒绝空路径和工作区本身;越界检查由 os.Root 负责。
func cleanFilePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path must not be empty")
	}
	path = filepath.Clean(path)
	if path == "." {
		return "", errors.New("path must name a file")
	}
	return path, nil
}

// fsReadFile 从工作区读取原始文本,限制大小并拒绝非普通文件及二进制内容。
func fsReadFile(ctx context.Context, root *os.Root, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 非阻塞打开后检查实际句柄,避免目标被替换成 FIFO 时阻塞任务。
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path must name a regular file")
	}
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("file exceeds %d byte limit", maxFileBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateText(content); err != nil {
		return nil, err
	}
	return content, nil
}

// validateText 拒绝超出大小限制以及非 UTF-8 或含 NUL 字节的内容。
func validateText(content []byte) error {
	if len(content) > maxFileBytes {
		return fmt.Errorf("content exceeds %d byte limit", maxFileBytes)
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return errors.New("only UTF-8 text without NUL bytes is supported")
	}
	return nil
}

// fileMode 拒绝以符号链接或非普通文件为写入目标,已有文件保留权限位。
func fileMode(root *os.Root, path string) (os.FileMode, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return filePermissions, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat destination: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("destination must be a regular file, not a directory or symbolic link")
	}
	return info.Mode().Perm(), nil
}

// fsWriteFile 在工作区的目标目录内创建临时文件,完整写入后再原子替换目标。
// expected 非空时在替换前复核读取版本,避免覆盖已观察到的外部修改。
func fsWriteFile(ctx context.Context, root *os.Root, path string, content []byte, expected *[sha256.Size]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent, err := root.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	mode, err := fileMode(parent, name)
	if err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("create temporary name: %w", err)
	}
	tempName := fmt.Sprintf(".minicode-%x.tmp", random)
	temp, err := parent.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = parent.Remove(tempName) }()
	defer func() { _ = temp.Close() }()
	if _, err := temp.Write(content); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Chmod(mode); err != nil {
		return fmt.Errorf("set file permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if _, err := fileMode(parent, name); err != nil {
		return err
	}
	if expected != nil {
		current, err := fsReadFile(ctx, parent, name)
		if err != nil {
			return fmt.Errorf("recheck file: %w", err)
		}
		if sha256.Sum256(current) != *expected {
			return errors.New("file changed since last read or write; read the file again before editing")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.Rename(tempName, name); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
