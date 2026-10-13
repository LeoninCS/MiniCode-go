//go:build darwin || netbsd || freebsd || openbsd || linux || dragonfly || solaris

package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/charmbracelet/x/termios"
)

// silenceTerminalEcho 防止任务执行时内核回显覆盖固定输入区，仍保留 SIGINT。
// readline 每次离开 raw 模式恢复到无回显状态，退出程序时再恢复原始设置。
func silenceTerminalEcho(input *os.File) (func() error, error) {
	state, err := term.GetState(input.Fd())
	if err != nil {
		return nil, fmt.Errorf("read terminal state: %w", err)
	}
	settings, err := termios.GetTermios(int(input.Fd()))
	if err != nil {
		return nil, fmt.Errorf("read terminal settings: %w", err)
	}
	if err := termios.SetTermios(int(input.Fd()), uint32(settings.Ispeed), uint32(settings.Ospeed),
		nil, nil, nil, nil, map[termios.L]bool{termios.ECHO: false, termios.ECHONL: false}); err != nil {
		return nil, fmt.Errorf("disable terminal echo: %w", err)
	}
	return func() error { return term.Restore(input.Fd(), state) }, nil
}

// screenResizeSignals 让快速缩放后也重绘，避免轮询错过中间尺寸。
func screenResizeSignals() (<-chan os.Signal, func()) {
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	return resized, func() { signal.Stop(resized) }
}
