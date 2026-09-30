// Package agent 管理模型调用、消息历史与工具执行循环。
package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/tools"
)

const (
	maxTurns               = 500
	turnLimitSummaryPrompt = "已达到工具执行轮数上限。请停止调用工具，仅根据已有对话和工具结果给出最终回复。回答用户的问题，并说明已完成的工作、尚未完成的部分及原因；不要声称未验证的结果。"

	// promptMarker 是每轮读取用户输入前打印的提示符。
	promptMarker = "> "
	// exitCommand 和 quitCommand 结束交互循环。
	exitCommand = "/exit"
	quitCommand = "/quit"
)

// Session 保存一次会话的消息历史、工具注册表和工作区句柄,供连续多轮对话复用。
//
// 一个 Session 顺序使用:每轮用 Turn 追加一条用户消息并跑完 Agent Loop,
// 历史在轮次之间累积,系统 Prompt 始终位于消息链首位。
// 持有工作区而不是每轮重建,是为了让 edit 工具的"先读后改"校验跨轮有效。
type Session struct {
	client   *provider.Client
	files    *tools.FileTools
	registry *tools.ToolRegistry
	messages []provider.Message
}

// NewSession 打开工作区、注册内置工具,并把系统 Prompt 作为首条消息写入历史。
// 调用方负责在结束时调用 Close 释放工作区句柄。
func NewSession(client *provider.Client) (*Session, error) {
	if client == nil {
		return nil, errors.New("agent: nil client")
	}
	// 只解析一次绝对路径:系统 Prompt 要声明工作区,文件工具也不再依赖运行期间的工作目录。
	workspace, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	files, err := tools.NewFileTools(workspace)
	if err != nil {
		return nil, err
	}
	registry, err := tools.NewBuiltinToolRegistry(files)
	if err != nil {
		_ = files.Close()
		return nil, err
	}
	return &Session{
		client:   client,
		files:    files,
		registry: registry,
		messages: []provider.Message{provider.NewMessage(provider.RoleSystem, systemPrompt(workspace), "")},
	}, nil
}

// Run 逐轮读取输入并驱动会话,直到遇到退出命令或输入结束,返回进程退出码。
//
// output 决定每一轮怎么展示,prompt 接收每轮的提示符;错误一律经 output.ToolError 报给宿主。
// initial 是命令行位置参数,当作循环的第一轮输入,便于直接开工第一轮;为空时只从 stdin 读。
// 超时按轮计算:一轮卡住不影响继续下一轮。
// 某轮失败不影响继续下一轮;只要有任何一轮失败,退出码就是 1,
// 这样 `echo "任务" | minicode` 仍然能反映任务成败。
func (s *Session) Run(ctx context.Context, output Output, initial []string, timeout time.Duration, stdin io.Reader, prompt io.Writer) int {
	// 输入在后台读取,主循环才能同时等待用户输入和 ctx 取消。
	// 取消粒度细化成"只停当前一轮,会话继续"留到后续实现。
	lines := make(chan string)
	done := make(chan error, 1)
	go readLines(strings.Join(initial, " "), stdin, lines, done)

	failed := false
	for {
		fmt.Fprint(prompt, promptMarker)
		select {
		case <-ctx.Done():
			fmt.Fprintln(prompt)
			output.ToolError(ctx.Err())
			return 1
		case err := <-done:
			fmt.Fprintln(prompt)
			if err != nil {
				output.ToolError(err)
				return 1
			}
			if failed {
				return 1
			}
			return 0
		case line := <-lines:
			switch strings.TrimSpace(line) {
			case exitCommand, quitCommand:
				if failed {
					return 1
				}
				return 0
			case "":
				continue
			}
			turnCtx, cancelTurn := context.WithTimeout(ctx, timeout)
			err := s.Turn(turnCtx, line, output)
			cancelTurn()
			if err != nil {
				output.ToolError(err)
				failed = true
			}
		}
	}
}

// Turn 追加一条用户消息并执行一轮 Agent Loop,直到模型给出最终回复、
// 达到轮数上限或发生不可恢复错误。
//
// 无论成功失败,历史都保持可以继续发送的状态:
// 工具失败会作为结果回传给模型,任务被取消时丢弃未配对的半轮消息。
func (s *Session) Turn(ctx context.Context, input string, output Output) error {
	s.messages = append(s.messages, provider.NewMessage(provider.RoleUser, input, ""))
	toolDefinitions := s.registry.Definitions()
	for turn := 0; turn < maxTurns; turn++ {
		resp, err := s.client.Chat(ctx, provider.ChatRequest{
			Messages: s.messages,
			Tools:    toolDefinitions,
		})
		if err != nil {
			return err
		}
		toolCalls, err := resp.ToolCalls()
		if err != nil {
			return fmt.Errorf("invalid response from model: %w", err)
		}
		content := resp.Content()
		if content == "" && len(toolCalls) == 0 {
			return errors.New("empty response from model")
		}
		if content != "" {
			output.Message(content)
		}
		// assistant 消息无论是否带工具调用都要进历史,否则下一轮模型看不到自己刚说过什么。
		s.messages = append(s.messages, resp.Choices[0].Message)
		if len(toolCalls) == 0 {
			return nil
		}
		assistant := len(s.messages) - 1
		for _, call := range toolCalls {
			output.ToolCall(call)
			result, err := s.registry.Execute(ctx, call, output)
			output.ToolResult(result)
			if ctx.Err() != nil {
				// assistant 消息带着 tool_calls 进入历史,缺任何一条工具结果都会让
				// 下一次请求非法,所以整轮回滚而不是留下半轮。
				s.messages = s.messages[:assistant]
				return ctx.Err()
			}
			if err != nil {
				output.ToolError(err)
				result += "\nerror: " + err.Error()
			}
			s.messages = append(s.messages, provider.NewMessage(provider.RoleTool, result, call.ID))
		}
	}
	content, err := summarizeAtTurnLimit(ctx, s.client, s.messages)
	if err != nil {
		output.Message(fmt.Sprintf("已达到最大执行轮数（%d），本次任务已停止，未能生成最终总结。请结合上方工具输出确认已完成的工作。", maxTurns))
		return fmt.Errorf("reached maximum model turns (%d); summarize: %w", maxTurns, err)
	}
	output.Message(content)
	return fmt.Errorf("reached maximum model turns (%d)", maxTurns)
}

// Close 释放工作区句柄。
func (s *Session) Close() error {
	return s.files.Close()
}

// readLines 先送出命令行位置参数作为第一轮输入,再逐行读取 stdin;
// 读完或出错时通过 done 报告,err 为 nil 表示正常 EOF。
//
// lines 不带缓冲:参数必须被主循环取走之后才会继续读 stdin,
// 否则参数和 EOF 同时就绪时 select 会随机挑一个,导致第一轮偶尔不执行。
func readLines(initial string, stdin io.Reader, lines chan<- string, done chan<- error) {
	if initial != "" {
		lines <- initial
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	for scanner.Scan() {
		lines <- scanner.Text()
	}
	done <- scanner.Err()
}

// summarizeAtTurnLimit 在完整工具结果之后请求一次总结,不再提供或执行工具。
func summarizeAtTurnLimit(ctx context.Context, client *provider.Client, messages []provider.Message) (string, error) {
	messages = append(messages, provider.NewMessage(provider.RoleSystem, turnLimitSummaryPrompt, ""))
	resp, err := client.Chat(ctx, provider.ChatRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	calls, err := resp.ToolCalls()
	if err != nil {
		return "", fmt.Errorf("invalid summary response: %w", err)
	}
	if len(calls) > 0 {
		return "", errors.New("summary response requested tools")
	}
	content := resp.Content()
	if strings.TrimSpace(content) == "" {
		return "", errors.New("empty summary response from model")
	}
	return content, nil
}
