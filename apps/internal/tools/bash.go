// Package tools 执行模型请求的本地工具。
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/MiniCode-go/minicode/internal/provider"
)

const (
	maxOutputBytes         = 64 * 1024
	outputTruncationMarker = "\n[output truncated]\n"
)

// RunBash 在当前工作目录执行命令,实时展示并返回合并后的标准输出和错误输出。
// 每次调用使用独立的非交互 shell;命令失败时同时返回已产生的输出和错误。
func RunBash(ctx context.Context, command string, stdout io.Writer) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("bash: command must not be empty")
	}

	output := &commandOutput{stdout: stdout}
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	// 放入独立进程组,取消时同时终止 shell 和它启动的命令。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	// 使用同一个 writer,让 os/exec 串行写入两路输出。
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	// shell 已退出时,也清理仍留在该进程组中的后台命令。
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return output.buffer.String(), fmt.Errorf("bash: execute command: %w", ctx.Err())
	}
	if err != nil {
		return output.buffer.String(), fmt.Errorf("bash: execute command: %w", err)
	}
	return output.buffer.String(), nil
}

// executeBashTool 校验命令参数并执行 bash,实时输出命令结果。
func executeBashTool(ctx context.Context, call provider.ToolCall, stdout io.Writer) (string, error) {
	var arguments map[string]any
	if err := call.DecodeArguments(&arguments); err != nil {
		return "", err
	}
	command, ok := arguments["command"].(string)
	if !ok {
		return "", errors.New("bash: command must be a string")
	}
	if len(arguments) != 1 {
		return "", errors.New("bash: only the command parameter is supported")
	}
	return RunBash(ctx, command, stdout)
}

// buildBashTool 构造发送给模型的 bash 工具声明。
func buildBashTool() provider.Tool {
	return provider.Tool{
		Type: provider.ToolTypeFunction,
		Function: provider.FunctionDefinition{
			Name:        toolNameBash,
			Description: "Run a shell command in the current workspace.",
			Parameters: provider.JSONSchema{
				"type": "object",
				"properties": map[string]provider.JSONSchema{
					"command": {"type": "string", "description": "The shell command to run."},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}

// commandOutput 限制终端显示和回传模型的输出大小,继续接收并丢弃超出部分。
type commandOutput struct {
	stdout    io.Writer
	buffer    bytes.Buffer
	truncated bool
}

// Write 截断超出上限的部分,始终返回原始长度以避免 os/exec 提前终止写入。
func (o *commandOutput) Write(p []byte) (int, error) {
	size := len(p)
	if o.truncated {
		return size, nil
	}
	remaining := maxOutputBytes - o.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		o.truncated = true
	}
	n, err := o.stdout.Write(p)
	o.buffer.Write(p[:n])
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	if o.truncated {
		o.buffer.WriteString(outputTruncationMarker)
		if _, err := io.WriteString(o.stdout, outputTruncationMarker); err != nil {
			return n, err
		}
	}
	return size, nil
}
