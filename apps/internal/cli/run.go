package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniCode-go/minicode/internal/agent"
	"github.com/MiniCode-go/minicode/internal/config"
	"github.com/MiniCode-go/minicode/internal/provider"
	"github.com/MiniCode-go/minicode/internal/telemetry"
)

// Run 解析 CLI 参数、创建会话并运行交互循环。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("minicode", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		timeout   = fs.Duration("timeout", time.Hour, "单轮任务的超时时间(含模型请求和命令执行)")
		yes       = fs.Bool("yes", false, "自动批准 bash、write、edit 工具调用")
		sessionID = fs.String("session", "", "按 ID 恢复当前工作区的会话（默认新建并自动保存）")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	sessionSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "session" {
			sessionSet = true
		}
	})
	if sessionSet && *sessionID == "" {
		_, _ = fmt.Fprintln(stderr, "minicode: --session requires a non-empty ID")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		printConfigHint(stderr)
		return 2
	}

	telemetryClient, err := telemetry.New(context.Background(), telemetry.Config{
		PublicKey: cfg.Langfuse.PublicKey,
		SecretKey: cfg.Langfuse.SecretKey,
		Host:      cfg.Langfuse.Host,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 2
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := telemetryClient.Close(flushCtx); err != nil {
			_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		}
	}()

	client := provider.NewClient(provider.Config{
		BaseURL: cfg.Model.BaseURL,
		APIKey:  cfg.Model.APIKey,
		Model:   cfg.Model.Name,
		Tracer:  telemetryClient.Tracer(),
	})

	// Ctrl+C 和 SIGTERM 结束整个进程;单轮超时由 timeout 单独控制。
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	interactive := IsTerminal(stdin) && IsTerminal(stdout)
	output := NewOutput(stdout, stderr, interactive)
	input, err := NewInput(stdin, stdout, stderr, interactive)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 1
	}
	defer func() {
		if err := input.Close(); err != nil {
			_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		}
	}()

	approver := &toolApprover{input: input, output: output, autoApprove: *yes}
	session, err := agent.OpenSessionByID(client, approver, *sessionID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		return 1
	}
	defer func() {
		if err := session.Close(); err != nil {
			_, _ = fmt.Fprintln(stderr, "minicode: "+err.Error())
		}
	}()
	writer := stderr
	if interactive {
		writer = stdout
	}
	_, _ = fmt.Fprintln(writer, "[会话] "+session.ID())

	// 位置参数作为第一轮输入，之后继续从终端编辑器或管道读取。
	return session.Run(ctx, output, fs.Args(), *timeout, input)
}

// printConfigHint 在配置缺失时给一个最小使用提示。
func printConfigHint(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage:")
	_, _ = fmt.Fprintln(w, "  configure "+config.FilePath+" before starting minicode")
	_, _ = fmt.Fprintln(w, "  minicode \"your prompt\"")
}
