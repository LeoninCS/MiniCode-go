package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/provider"
)

const (
	envAPIKey  = "MINICODE_API_KEY"
	envBaseURL = "MINICODE_BASE_URL"
	envModel   = "MINICODE_MODEL"
)

// Run 解析 CLI 参数、创建会话并运行交互循环。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("minicode", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		apiKey  = fs.String("api-key", "", "模型服务 API Key(覆盖 MINICODE_API_KEY)")
		baseURL = fs.String("base-url", "", "模型服务 Base URL,例如 https://api.openai.com/v1(覆盖 MINICODE_BASE_URL)")
		model   = fs.String("model", "", "模型名(覆盖 MINICODE_MODEL)")
		timeout = fs.Duration("timeout", time.Hour, "单轮任务的超时时间(含模型请求和命令执行)")
		yes     = fs.Bool("yes", false, "自动批准 bash、write、edit 工具调用")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	apiKeyVal, baseURLVal, modelVal, err := loadConfig(*apiKey, *baseURL, *model)
	if err != nil {
		fmt.Fprintln(stderr, "minicode: "+err.Error())
		printConfigHint(stderr)
		return 2
	}

	client := provider.NewClient(provider.Config{
		BaseURL: baseURLVal,
		APIKey:  apiKeyVal,
		Model:   modelVal,
	})

	// Ctrl+C 和 SIGTERM 结束整个进程;单轮超时由 timeout 单独控制。
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	interactive := IsTerminal(stdin) && IsTerminal(stdout)
	output := NewOutput(stdout, stderr, interactive)
	input, err := NewInput(stdin, stdout, stderr, interactive)
	if err != nil {
		fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 1
	}
	defer func() {
		if err := input.Close(); err != nil {
			fmt.Fprintln(stderr, "minicode: "+err.Error())
		}
	}()

	approver := &toolApprover{input: input, output: output, autoApprove: *yes}
	session, err := agent.NewSession(client, approver)
	if err != nil {
		fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 1
	}
	defer func() {
		if err := session.Close(); err != nil {
			fmt.Fprintln(stderr, "minicode: "+err.Error())
		}
	}()

	// 位置参数作为第一轮输入，之后继续从终端编辑器或管道读取。
	return session.Run(ctx, output, fs.Args(), *timeout, input)
}

// loadConfig 把 flag 显式传入的值与对应环境变量合并;
// flag 非空时优先,否则回退到环境变量。
func loadConfig(flagKey, flagBase, flagModel string) (string, string, string, error) {
	apiKey := firstNonEmpty(flagKey, os.Getenv(envAPIKey))
	baseURL := firstNonEmpty(flagBase, os.Getenv(envBaseURL))
	model := firstNonEmpty(flagModel, os.Getenv(envModel))
	var missing []string
	if apiKey == "" {
		missing = append(missing, envAPIKey)
	}
	if baseURL == "" {
		missing = append(missing, envBaseURL)
	}
	if model == "" {
		missing = append(missing, envModel)
	}
	if len(missing) > 0 {
		return apiKey, baseURL, model, fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return apiKey, baseURL, model, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// printConfigHint 在配置缺失时给一个最小使用提示。
func printConfigHint(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  MINICODE_API_KEY=... MINICODE_BASE_URL=... MINICODE_MODEL=... minicode")
	fmt.Fprintln(w, "  MINICODE_API_KEY=... MINICODE_BASE_URL=... MINICODE_MODEL=... minicode \"your prompt\"")
	fmt.Fprintln(w, "  echo 'your prompt' | MINICODE_API_KEY=... MINICODE_BASE_URL=... MINICODE_MODEL=... minicode")
}
