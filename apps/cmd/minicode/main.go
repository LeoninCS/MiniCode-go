// Command minicode 是 MiniCode-go 项目的 CLI 入口。
//
// Day 1 仅支持一次性的非流式文本请求:
// 读取用户输入,调用模型服务,把模型回复输出到 stdout。
// 交互式会话、工具调用、Agent Loop、流式输出等能力
// 将在 Day 2～Day 12 逐步加入。
package main

import (
	"bufio"
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

	"github.com/MiniCode-go/minicode/internal/provider"
)

const (
	envAPIKey  = "MINICODE_API_KEY"
	envBaseURL = "MINICODE_BASE_URL"
	envModel   = "MINICODE_MODEL"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run 是 main 的可测入口,把 stdin/stdout/stderr 抽成参数方便测试。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("minicode", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		apiKey  = fs.String("api-key", "", "模型服务 API Key(覆盖 MINICODE_API_KEY)")
		baseURL = fs.String("base-url", "", "模型服务 Base URL,例如 https://api.openai.com/v1(覆盖 MINICODE_BASE_URL)")
		model   = fs.String("model", "", "模型名(覆盖 MINICODE_MODEL)")
		timeout = fs.Duration("timeout", 60*time.Second, "单次请求的超时时间")
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

	stdinIsTTY := isTerminal(stdin)
	prompt, err := readPrompt(fs.Args(), stdin, stdinIsTTY)
	if err != nil {
		fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 2
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(stderr, "minicode: empty prompt")
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, *timeout)
	defer cancelTimeout()

	client := provider.NewClient(provider.Config{
		BaseURL: baseURLVal,
		APIKey:  apiKeyVal,
		Model:   modelVal,
	})

	resp, err := client.Chat(ctx, provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 1
	}

	content := resp.FirstContent()
	if content == "" {
		fmt.Fprintln(stderr, "minicode: empty response from model")
		return 1
	}
	fmt.Fprintln(stdout, content)
	return 0
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
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// readPrompt 决定用户输入来自哪:剩余的 CLI 参数,或 stdin。
// stdinIsTTY 表示 stdin 是否是字符设备(终端),
// 用于在没有参数也没有管道输入时给出更友好的错误。
func readPrompt(args []string, stdin io.Reader, stdinIsTTY bool) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if stdinIsTTY {
		return "", errors.New("no prompt provided (pass it as argument or pipe via stdin)")
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	var sb strings.Builder
	for scanner.Scan() {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return sb.String(), nil
}

// isTerminal 尽量轻量地判断一个 reader 是否是 TTY。
// 仅在传入 *os.File 时探测;否则一律返回 false。
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// printConfigHint 在配置缺失时给一个最小使用提示。
func printConfigHint(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  MINICODE_API_KEY=... MINICODE_BASE_URL=... MINICODE_MODEL=... minicode \"your prompt\"")
	fmt.Fprintln(w, "  echo 'your prompt' | MINICODE_API_KEY=... MINICODE_BASE_URL=... MINICODE_MODEL=... minicode")
}
