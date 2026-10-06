// Package agent 管理模型调用、消息历史与工具执行循环。
package agent

import (
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
	approver ToolApprover
	messages []provider.Message
}

// NewSession 打开工作区、注册内置工具,并把系统 Prompt 作为首条消息写入历史。
// bash、write、edit 执行前会调用可选的 approver；CLI 始终提供实现。
// approver 为 nil 时直接执行工具。
// 调用方负责在结束时调用 Close 释放工作区句柄。
func NewSession(client *provider.Client, approver ToolApprover) (*Session, error) {
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
		approver: approver,
		messages: []provider.Message{provider.NewMessage(provider.RoleSystem, systemPrompt(workspace), "")},
	}, nil
}

// Input 负责读取一条完整的用户输入。终端实现可以提供光标编辑、历史和多行输入，
// 管道实现则继续逐行读取。prompt 是本次读取应展示的提示符。
type Input interface {
	Readline(prompt string) (string, error)
	Close() error
}

// Run 逐轮读取输入并驱动会话,直到遇到退出命令或输入结束,返回进程退出码。
//
// output 决定每一轮怎么展示,input 负责读取用户输入。
// initial 是命令行位置参数,当作循环的第一轮输入,便于直接开工第一轮;为空时从 input 读。
// 超时按轮计算:一轮卡住不影响继续下一轮。
// 某轮失败不影响继续下一轮;只要有任何一轮失败,退出码就是 1,
// 这样 `echo "任务" | minicode` 仍然能反映任务成败。
func (s *Session) Run(ctx context.Context, output Output, initial []string, timeout time.Duration, input Input) int {
	// 输入在后台读取,主循环才能同时等待根 context 取消。
	// readline 结果使用单元素缓冲，防止 context 与输入同时就绪时读取 goroutine 泄漏。
	type inputResult struct {
		line string
		err  error
	}
	results := make(chan inputResult, 1)
	read := func() {
		line, err := input.Readline(promptMarker)
		results <- inputResult{line: line, err: err}
	}

	failed := false
	pendingInitial := strings.Join(initial, " ")
	for {
		if pendingInitial != "" {
			results <- inputResult{line: pendingInitial}
			pendingInitial = ""
		} else {
			go read()
		}

		select {
		case <-ctx.Done():
			_ = input.Close()
			output.ToolError(ctx.Err())
			return 1
		case result := <-results:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					if failed {
						return 1
					}
					return 0
				}
				output.ToolError(result.err)
				return 1
			}
			line := result.line
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
			// 整个会话已取消时直接退出，不再启动下一轮输入 goroutine，
			// 避免它与 Close 竞争，在进程退出前重新进入终端 raw 模式。
			if ctx.Err() != nil {
				return 1
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
		// 带工具调用的文本属于模型的中间过程，不展示；只有不再调用工具时才输出最终回复。
		if content != "" && len(toolCalls) == 0 {
			output.Message(content)
		}
		// assistant 消息无论是否带工具调用都要进历史,否则下一轮模型看不到自己刚说过什么。
		s.messages = append(s.messages, resp.Choices[0].Message)
		if len(toolCalls) == 0 {
			return nil
		}
		assistant := len(s.messages) - 1
		for _, call := range toolCalls {
			if requiresApproval(call.Function.Name) && s.approver != nil {
				approved, err := s.approver.Approve(ctx, call)
				if err != nil {
					// 未为当前 assistant 的全部 tool_calls 生成结果时，整轮必须回滚。
					s.messages = s.messages[:assistant]
					return err
				}
				if !approved {
					result := "tool execution denied by user"
					s.messages = append(s.messages, provider.NewMessage(provider.RoleTool, result, call.ID))
					continue
				}
			}
			result, err := s.registry.Execute(ctx, call, io.Discard)
			if ctx.Err() != nil {
				// assistant 消息带着 tool_calls 进入历史,缺任何一条工具结果都会让
				// 下一次请求非法,所以整轮回滚而不是留下半轮。
				s.messages = s.messages[:assistant]
				return ctx.Err()
			}
			if err != nil {
				result += "\nerror: " + err.Error()
			}
			s.messages = append(s.messages, provider.NewMessage(provider.RoleTool, result, call.ID))
		}
	}
	content, err := summarizeAtTurnLimit(ctx, s.client, s.messages)
	if err != nil {
		output.Message(fmt.Sprintf("已达到最大执行轮数（%d），本次任务已停止，未能生成最终总结。请根据最终工作区状态确认已完成的工作。", maxTurns))
		return fmt.Errorf("reached maximum model turns (%d); summarize: %w", maxTurns, err)
	}
	output.Message(content)
	return fmt.Errorf("reached maximum model turns (%d)", maxTurns)
}

// Close 释放工作区句柄。
func (s *Session) Close() error {
	return s.files.Close()
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
