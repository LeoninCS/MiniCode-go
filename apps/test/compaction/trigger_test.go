package compaction_test

import (
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
)

func TestTokenTrigger_Threshold(t *testing.T) {
	for _, tc := range []struct {
		name string
		used int
		want bool
	}{
		{"above reserve", 245759, false},
		{"exactly reserve", 245760, false},
		{"below reserve", 245761, true},
		{"full window", 262144, true},
		{"over window", 300000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var trigger compaction.TokenTrigger
			req := provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "问题", "")}}
			answer := provider.NewMessage(provider.RoleAssistant, "回答", "")
			trigger.Record(req, &provider.ChatResponse{
				Choices: []provider.Choice{{Message: answer}},
				Usage:   &provider.Usage{PromptTokens: tc.used - 1, CompletionTokens: 1},
			})
			req.Messages = append(req.Messages, answer)
			if got := trigger.ShouldCompact(req); got != tc.want {
				t.Fatalf("ShouldCompact = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestTokenTrigger_LatestUsageAndNewContent(t *testing.T) {
	var trigger compaction.TokenTrigger
	req := provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "问题", "")}}
	answer := provider.NewMessage(provider.RoleAssistant, "回答", "")
	for range 3 {
		trigger.Record(req, &provider.ChatResponse{
			Choices: []provider.Choice{{Message: answer}},
			Usage:   &provider.Usage{TotalTokens: 130000},
		})
		req.Messages = append(req.Messages, answer)
		if trigger.ShouldCompact(req) {
			t.Fatal("usage from different requests was accumulated")
		}
	}
	trigger.Record(req, &provider.ChatResponse{
		Choices: []provider.Choice{{Message: answer}},
		Usage:   &provider.Usage{TotalTokens: 245760},
	})
	req.Messages = append(req.Messages, answer)
	if trigger.ShouldCompact(req) {
		t.Fatal("exactly 16384 tokens remaining triggered compaction")
	}
	req.Messages = append(req.Messages, provider.NewMessage(provider.RoleTool, "新增工具结果", "read-file"))
	if !trigger.ShouldCompact(req) {
		t.Fatal("new tool result was omitted from the context budget")
	}
}

func TestTokenTrigger_EstimatesWithoutUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  provider.ChatRequest
	}{
		{"ASCII input", provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, strings.Repeat("x", 983044), "")}}},
		{"Chinese input", provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, strings.Repeat("中", 122881), "")}}},
		{"tool arguments", provider.ChatRequest{Messages: []provider.Message{{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "write", Function: provider.FunctionCall{Name: "write", Arguments: strings.Repeat("x", 983044)}}}}}}},
		{"tool definitions", provider.ChatRequest{Tools: []provider.Tool{{Function: provider.FunctionDefinition{Name: "read", Description: strings.Repeat("x", 983044)}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var trigger compaction.TokenTrigger
			if !trigger.ShouldCompact(tc.req) {
				t.Fatal("large request did not trigger compaction without model usage")
			}
		})
	}
	var trigger compaction.TokenTrigger
	if trigger.ShouldCompact(provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "hello", "")}}) {
		t.Fatal("small request triggered compaction")
	}
}

func TestTokenTrigger_MissingUsageKeepsCalibration(t *testing.T) {
	req := provider.ChatRequest{Messages: []provider.Message{provider.NewMessage(provider.RoleUser, "问题", "")}}
	answer := provider.NewMessage(provider.RoleAssistant, "回答", "")
	for _, usage := range []*provider.Usage{nil, {}, {PromptTokens: -1, TotalTokens: 999999}} {
		var trigger compaction.TokenTrigger
		trigger.Record(req, &provider.ChatResponse{Choices: []provider.Choice{{Message: answer}}, Usage: &provider.Usage{TotalTokens: 245761}})
		current := provider.ChatRequest{Messages: append(append([]provider.Message(nil), req.Messages...), answer)}
		trigger.Record(current, &provider.ChatResponse{Choices: []provider.Choice{{Message: answer}}, Usage: usage})
		if !trigger.ShouldCompact(current) {
			t.Fatalf("usage %+v discarded the last valid calibration", usage)
		}
	}
}
