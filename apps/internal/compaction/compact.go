// Package compaction 提供上下文压缩能力。
package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/MiniCode-go/minicode/internal/provider"
)

const (
	// KeepRecentTokens 是固定的近期原文保留预算，不提供配置项。
	KeepRecentTokens = 20000
	maxSummaryTokens = 8 * 1024
	summaryPrefix    = "The following is a summary of earlier conversation context:\n<context-summary>\n"
	summarySuffix    = "\n</context-summary>"
)

// Result 仅在 Changed 为 true 时包含可提交的新检查点；失败时 State 保持原样。
// 摘要请求用量用于任务统计，不能校准主对话的上下文占用。
type Result struct {
	State          *State
	Changed        bool
	ModelCalls     int
	Usage          provider.Usage
	UsageResponses int
}

// Compact 保留最近约 20k token 的原始消息，使用 LLM 总结更早的内容。
// 工具调用和全部结果作为整体保留；最新一条消息或工具批次可超过保留预算。
// history 是不含系统 Prompt 的完整原始历史；压缩只更新检查点，不改写历史。
// 摘要输入不截断，超出单次请求预算时分块更新摘要。
func Compact(ctx context.Context, client provider.Provider, history []provider.Message, previous *State) (result Result, err error) {
	result.State = previous
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := provider.ValidateMessages(history); err != nil {
		return result, fmt.Errorf("compaction: invalid history: %w", err)
	}
	if err := previous.Validate(history); err != nil {
		return result, err
	}
	start := 0
	previousSummary := ""
	if previous != nil {
		start = previous.FirstKeptMessageIndex
		previousSummary = previous.Summary
	}
	cut := recentCut(history, start)
	if cut <= start {
		return result, nil
	}
	if client == nil {
		return result, errors.New("compaction: nil provider")
	}
	transcript := serializeConversation(history[start:cut])
	summary, err := summarize(ctx, client, transcript, previousSummary, &result)
	if err != nil {
		return result, fmt.Errorf("compaction: summarize history: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	candidate := &State{Summary: summary, FirstKeptMessageIndex: cut}
	if err := candidate.Validate(history); err != nil {
		return result, err
	}
	if err := provider.ValidateMessages(candidate.ContextMessages(history)); err != nil {
		return result, fmt.Errorf("compaction: invalid compacted context: %w", err)
	}
	result.State = candidate
	result.Changed = true
	return result, nil
}

// recentCut 从后向前保留完整消息；不会留下没有 assistant 调用的 tool 结果。
func recentCut(messages []provider.Message, start int) int {
	cut, tokens := len(messages), 0
	for cut > start {
		unitStart := cut - 1
		for unitStart > start && messages[unitStart].Role == provider.RoleTool {
			unitStart--
		}
		unitTokens := 0
		for _, message := range messages[unitStart:cut] {
			unitTokens += estimateMessage(message)
		}
		// 选择更接近 20k 的完整消息边界；始终至少保留最新的一个整体。
		if cut < len(messages) && tokens+unitTokens > KeepRecentTokens && KeepRecentTokens-tokens < tokens+unitTokens-KeepRecentTokens {
			break
		}
		cut = unitStart
		tokens += unitTokens
		if tokens >= KeepRecentTokens {
			break
		}
	}
	return cut
}

func serializeConversation(messages []provider.Message) string {
	var parts []string
	for _, message := range messages {
		switch message.Role {
		case provider.RoleUser:
			parts = append(parts, "[User]: "+message.Text())
		case provider.RoleAssistant:
			if message.Text() != "" {
				parts = append(parts, "[Assistant]: "+message.Text())
			}
			for _, call := range message.ToolCalls {
				parts = append(parts, fmt.Sprintf("[Assistant tool calls]: %s(%s) [id=%s]", call.Function.Name, call.Function.Arguments, call.ID))
			}
		case provider.RoleTool:
			parts = append(parts, fmt.Sprintf("[Tool result id=%s]: %s", message.ToolCallID, message.Text()))
		}
	}
	return strings.Join(parts, "\n\n")
}

func summaryRequest(transcript, previous string) provider.ChatRequest {
	prompt := "<conversation>\n" + transcript + "\n</conversation>\n\n"
	if previous == "" {
		prompt += summaryPrompt
	} else {
		prompt += "<previous-summary>\n" + previous + "\n</previous-summary>\n\n" + updateSummaryPrompt
	}
	return provider.ChatRequest{
		Messages: []provider.Message{
			provider.NewMessage(provider.RoleSystem, summarySystemPrompt, ""),
			provider.NewMessage(provider.RoleUser, prompt, ""),
		},
		MaxTokens: maxSummaryTokens,
	}
}

// summarize 分块仅改变请求分组，不省略任何文本。已有摘要随每块输入更新。
func summarize(ctx context.Context, client provider.Provider, transcript, previous string, result *Result) (string, error) {
	for transcript != "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// 保留固定余量覆盖输出和本地估算误差。额外 16 token 覆盖字符估算的取整。
		budget := ContextWindow - ReservedTokens - estimateRequest(summaryRequest("", previous)) - 16
		if budget <= 0 {
			return "", errors.New("summary instructions and previous summary exceed context budget")
		}
		end := textCut(transcript, budget)
		if end == 0 {
			return "", errors.New("no room for summary input")
		}
		summary, err := summarizeChunk(ctx, client, transcript[:end], previous, result)
		if err != nil {
			return "", err
		}
		previous = summary
		transcript = transcript[end:]
	}
	return previous, nil
}

// textCut 返回一个 UTF-8 边界，所有未发送内容继续进入下一块。
func textCut(text string, budget int) int {
	ascii, other := 0, 0
	for index, r := range text {
		if r <= 127 {
			ascii++
		} else {
			other++
		}
		if (ascii+3)/4+other*2 > budget {
			return index
		}
	}
	return len(text)
}

func summarizeChunk(ctx context.Context, client provider.Provider, text, previous string, result *Result) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result.ModelCalls++
	resp, err := client.Chat(ctx, summaryRequest(text, previous))
	if resp != nil && resp.Usage != nil {
		result.UsageResponses++
		result.Usage.PromptTokens += resp.Usage.PromptTokens
		result.Usage.CompletionTokens += resp.Usage.CompletionTokens
		total := resp.Usage.TotalTokens
		if total == 0 {
			total = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
		}
		result.Usage.TotalTokens += total
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		if provider.IsContextOverflow(err) {
			// 服务端分词与本地粗估不同：拆小本块继续，不能递归触发主对话恢复。
			middle := len(text) / 2
			for middle > 0 && !utf8.RuneStart(text[middle]) {
				middle--
			}
			if middle > 0 {
				left, splitErr := summarizeChunk(ctx, client, text[:middle], previous, result)
				if splitErr != nil {
					return "", splitErr
				}
				return summarize(ctx, client, text[middle:], left, result)
			}
		}
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty summary response from model")
	}
	choice := resp.Choices[0]
	if choice.Message.Role != provider.RoleAssistant || len(choice.Message.ToolCalls) > 0 {
		return "", errors.New("summary response must be assistant text without tools")
	}
	if choice.FinishReason != "" && choice.FinishReason != "stop" {
		return "", fmt.Errorf("incomplete summary response (finish_reason=%s)", choice.FinishReason)
	}
	summary := strings.TrimSpace(resp.Content())
	if summary == "" {
		return "", errors.New("empty summary response from model")
	}
	return summary, nil
}
