package compaction_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/cli"
	"github.com/MiniCode-go/minicode/internal/compaction"
	"github.com/MiniCode-go/minicode/internal/provider"
	sessionstore "github.com/MiniCode-go/minicode/internal/session"
)

const (
	semanticGoal       = "修复配置加载，使缺少 model.name 的配置返回明确错误"
	semanticConstraint = "禁止新增第三方依赖；禁止修改 docs/ 和 README.md"
	semanticFile       = "apps/internal/config/config.go"
	semanticTestFile   = "apps/test/cli/config_test.go"
	semanticCommand    = "go -C apps test ./..."
	semanticError      = "TestLoad_MissingName: expected missing model.name error, got nil"
)

type semanticFact struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type semanticPhase struct {
	Name       string
	Messages   []provider.Message
	Facts      []semanticFact
	References []string
	Summary    string // 确定性流程测试的参考输出，不用于替代真实模型的摘要。
}

func semanticScenario(t *testing.T, workspace string) []semanticPhase {
	t.Helper()
	batch := func(id string, files []string, testResult string) []provider.Message {
		calls := make([]provider.ToolCall, 0, len(files)+1)
		results := make([]provider.Message, 0, len(files)+1)
		for i, path := range files {
			arguments, err := json.Marshal(map[string]string{"path": path, "content": "// fixture revision " + id})
			if err != nil {
				t.Fatal(err)
			}
			callID := fmt.Sprintf("%s-write-%d", id, i)
			calls = append(calls, provider.ToolCall{ID: callID, Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{Name: "write", Arguments: string(arguments)}})
			results = append(results, provider.NewMessage(provider.RoleTool, "已修改 "+path, callID))
		}
		if testResult != "" {
			arguments, err := json.Marshal(map[string]string{"command": semanticCommand})
			if err != nil {
				t.Fatal(err)
			}
			callID := id + "-test"
			calls = append(calls, provider.ToolCall{ID: callID, Type: provider.ToolTypeFunction,
				Function: provider.FunctionCall{Name: "bash", Arguments: string(arguments)}})
			results = append(results, provider.NewMessage(provider.RoleTool, testResult, callID))
		}
		return append([]provider.Message{{Role: provider.RoleAssistant, ToolCalls: calls}}, results...)
	}
	common := []semanticFact{
		{"goal", "当前目标仍然是修复配置加载，缺少 model.name 时返回明确错误。"},
		{"dependencies", "仍然禁止新增第三方依赖。"},
		{"documentation", "仍然禁止修改 docs/ 和 README.md。"},
		{"workspace", "当前工作区的准确路径是 " + workspace + "。"},
		{"test_command", "测试命令准确保留为 " + semanticCommand + "。"},
	}
	first := append([]provider.Message{provider.NewMessage(provider.RoleUser,
		semanticGoal+"。"+semanticConstraint+"。当前工作区："+workspace+"。", "")}, batch("first", []string{semanticFile}, "FAIL\n"+semanticError)...)
	second := batch("fixed", []string{semanticFile, semanticTestFile}, "PASS\n所有包的测试通过，exit code 0")
	second = append(second, provider.NewMessage(provider.RoleAssistant, "缺少 model.name 的校验已修复，测试错误已解决，本次全量测试通过。", ""))
	third := append([]provider.Message{provider.NewMessage(provider.RoleUser, "再补充空白 model.name 的校验，其余目标和约束保持不变。", "")}, batch("untested", []string{semanticFile}, "")...)
	third = append(third, provider.NewMessage(provider.RoleAssistant,
		"刚刚再次修改了 "+semanticFile+"，加入空白 model.name 的校验。自这次修改以来尚未重新运行测试；此前 PASS 仅对应上一版代码。下一步补充空白字段测试并重新运行 "+semanticCommand+"。", ""))
	phases := []semanticPhase{
		{
			Name: "failed", Messages: first,
			Facts: []semanticFact{
				{"modified_files", "已修改的文件包含准确路径 " + semanticFile + "。"},
				{"test_status", "最新测试失败；准确保留失败信息 " + semanticError + "，当前仍需修复。"},
				{"next_step", "下一步仍需修复缺少 model.name 时没有报错的问题，并重新测试。"},
			},
			Summary: "## Goal\n" + semanticGoal + "\n\n## Constraints & Preferences\n" + semanticConstraint +
				"\n\n## Progress\n### Done\n已修改 " + semanticFile + "\n### Blocked\n最新测试失败：" + semanticError +
				"\n\n## Next Steps\n修复缺少 model.name 的校验，然后重新测试。\n\n## Critical Context\n工作区：" + workspace + "\n测试命令：" + semanticCommand,
		},
		{
			Name: "passed", Messages: second,
			Facts: []semanticFact{
				{"modified_files", "累计修改的文件包含准确路径 " + semanticFile + " 和 " + semanticTestFile + "。"},
				{"test_status", "缺少 model.name 的问题和测试失败已解决，最新一次全量测试通过；原错误不再是当前阻塞。"},
			},
			Summary: "## Goal\n" + semanticGoal + "\n\n## Constraints & Preferences\n" + semanticConstraint +
				"\n\n## Progress\n### Done\n已修改 " + semanticFile + " 和 " + semanticTestFile +
				"\n缺少 model.name 的问题已修复，原测试错误已解决，最新全量测试通过。\n### Blocked\n无\n\n## Next Steps\n报告修复结果。\n\n## Critical Context\n工作区：" + workspace + "\n测试命令：" + semanticCommand,
		},
		{
			Name: "modified_after_pass", Messages: third,
			Facts: []semanticFact{
				{"modified_files", "累计修改的文件仍包含准确路径 " + semanticFile + " 和 " + semanticTestFile + "。"},
				{"test_status", "在上一版测试通过之后又修改了代码；最新修改尚未复测，不能把此前 PASS 当作当前版本已通过。"},
				{"next_step", "下一步仍需补充空白 model.name 的测试，并运行 " + semanticCommand + " 验证最新修改。"},
			},
			Summary: "## Goal\n" + semanticGoal + "，并补充空白 model.name 的校验\n\n## Constraints & Preferences\n" + semanticConstraint +
				"\n\n## Progress\n### Done\n累计修改 " + semanticFile + " 和 " + semanticTestFile +
				"\n### In Progress\n上一版测试通过后又修改了代码，最新修改尚未复测；此前 PASS 不代表当前版本已通过。" +
				"\n\n## Next Steps\n补充空白 model.name 的测试，再运行 " + semanticCommand +
				"。\n\n## Critical Context\n工作区：" + workspace + "\n测试命令：" + semanticCommand,
		},
	}
	for i := range phases {
		phases[i].Facts = append(append([]semanticFact(nil), common...), phases[i].Facts...)
		phases[i].References = []string{workspace, semanticFile, semanticCommand}
		if i == 0 {
			phases[i].References = append(phases[i].References, semanticError)
		} else {
			phases[i].References = append(phases[i].References, semanticTestFile)
		}
	}
	return phases
}

// 每轮均推进保留边界并保存、恢复；检查模型实际可读的上下文，不只检查原文存档。
func runSemanticScenario(t *testing.T, client provider.Provider, workspace string, phases []semanticPhase,
	check func(*testing.T, semanticPhase, string)) sessionstore.Snapshot {
	t.Helper()
	const id = "11111111-1111-4111-8111-111111111111"
	path, err := sessionstore.Path(workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	var history []provider.Message
	var state *compaction.State
	var saved sessionstore.Snapshot
	for i, phase := range phases {
		if !t.Run(phase.Name, func(t *testing.T) {
			history = append(history, phase.Messages...)
			// 空白使近期原文超过固定预算，保证每轮真的压缩，同时降低真实模型测试的无关输入量。
			recent := provider.NewMessage(provider.RoleAssistant, fmt.Sprintf("Unrelated fixture padding %d:\n", i)+strings.Repeat(" ", 4*compaction.KeepRecentTokens), "")
			history = append(history, recent)
			previousIndex := 0
			if state != nil {
				previousIndex = state.FirstKeptMessageIndex
			}
			result, err := compaction.Compact(t.Context(), client, history, state)
			if err != nil {
				t.Fatalf("compact phase %s: %v", phase.Name, err)
			}
			if !result.Changed || result.ModelCalls == 0 || result.State.FirstKeptMessageIndex <= previousIndex || result.State.FirstKeptMessageIndex != len(history)-1 {
				t.Fatalf("phase did not advance compaction: %+v", result)
			}
			saved = sessionstore.Snapshot{Version: sessionstore.Version, ID: id, Workspace: workspace,
				LastTurnStatus: "completed", Messages: history, Compaction: result.State}
			if err := sessionstore.Save(path, saved); err != nil {
				t.Fatal(err)
			}
			restored, err := sessionstore.Load(path)
			if err != nil || !reflect.DeepEqual(restored, saved) {
				t.Fatalf("checkpoint roundtrip: %v", err)
			}
			contextMessages := restored.Compaction.ContextMessages(restored.Messages)
			if len(contextMessages) != 2 || !reflect.DeepEqual(contextMessages[1], recent) || !strings.Contains(contextMessages[0].Text(), restored.Compaction.Summary) {
				t.Fatal("model context did not select the restored summary and recent message")
			}
			if err := provider.ValidateMessages(contextMessages); err != nil {
				t.Fatal(err)
			}
			for _, reference := range phase.References {
				if !strings.Contains(restored.Compaction.Summary, reference) {
					t.Fatalf("summary lost exact reference %q\nsummary:\n%s", reference, restored.Compaction.Summary)
				}
			}
			check(t, phase, restored.Compaction.Summary)
			history, state = restored.Messages, restored.Compaction
		}) {
			t.FailNow()
		}
	}
	return saved
}

// Fake Provider 只验证事实来源、旧摘要传递和恢复后的续接；真实摘要质量由 live 测试验收。
func TestCompact_SemanticScenarioPreservesInputsAndRestoresContinuation(t *testing.T) {
	workspace := t.TempDir()
	phases := semanticScenario(t, workspace)
	calls := 0
	client := summaryProvider(func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		if calls >= len(phases) {
			t.Fatal("unexpected summary request")
		}
		phase := phases[calls]
		transcript := conversationText(t, req)
		for _, message := range phase.Messages {
			if message.Text() != "" && !strings.Contains(transcript, message.Text()) {
				t.Fatalf("phase %s lost source text", phase.Name)
			}
			for _, call := range message.ToolCalls {
				if !strings.Contains(transcript, call.Function.Arguments) || !strings.Contains(transcript, call.ID) {
					t.Fatalf("phase %s lost tool arguments or ID", phase.Name)
				}
			}
		}
		if calls > 0 && !strings.Contains(req.Messages[1].Text(), "<previous-summary>\n"+phases[calls-1].Summary+"\n</previous-summary>") {
			t.Fatalf("phase %s lost previous summary", phase.Name)
		}
		calls++
		return summaryResponse(phase.Summary), nil
	})
	saved := runSemanticScenario(t, client, workspace, phases, func(t *testing.T, phase semanticPhase, summary string) {
		if summary != phase.Summary {
			t.Fatal("summary changed during checkpoint save/restore")
		}
	})
	if calls != 3 {
		t.Fatalf("summary calls = %d, want 3", calls)
	}
	t.Chdir(workspace)
	continuations := 0
	continuation := summaryProvider(func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		continuations++
		if len(req.Messages) != 4 || !strings.Contains(req.Messages[0].Text(), workspace) || !strings.Contains(req.Messages[1].Text(), phases[2].Summary) || req.Messages[3].Text() != "继续验证最新修改" {
			t.Fatal("restored agent did not receive final checkpoint and new user input")
		}
		return summaryResponse("继续补充空白字段测试并运行全量测试"), nil
	})
	session, err := agent.OpenSessionByID(continuation, nil, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Turn(t.Context(), "继续验证最新修改", cli.NewOutput(io.Discard, io.Discard, false)); err != nil {
		t.Fatal(err)
	}
	if continuations != 1 {
		t.Fatalf("continuations = %d, want 1", continuations)
	}
}
