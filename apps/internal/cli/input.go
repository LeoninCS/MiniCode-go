package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/ergochat/readline"
)

const (
	// Kitty keyboard protocol 的“消除歧义”标志让支持它的终端为 Shift+Enter
	// 发送带修饰键的 CSI-u 序列。不支持该协议的终端会安全忽略这两个控制序列。
	enableExtendedKeys  = "\x1b[>1u"
	disableExtendedKeys = "\x1b[<u"
)

type terminalKeyReader struct {
	input   *bufio.Reader
	closer  io.Closer
	pending []byte
}

func newTerminalKeyReader(input io.ReadCloser) io.ReadCloser {
	return &terminalKeyReader{input: bufio.NewReader(input), closer: input}
}

// Read 将扩展键盘协议中的 Shift+Enter、Ctrl+J 和 Ctrl+C
// 转换为 readline 能识别的控制字符。
func (r *terminalKeyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		first, err := r.input.ReadByte()
		if err != nil {
			return 0, err
		}
		r.pending = append(r.pending, first)
		if first != '\x1b' {
			break
		}

		// 读取一个完整 CSI 序列。终止字节范围是 0x40～0x7e；限制长度可防止
		// 非法输入让该层无限缓存。非 CSI 的 Esc 组合只多读取一个字节。
		second, err := r.input.ReadByte()
		if err != nil {
			break
		}
		r.pending = append(r.pending, second)
		if second == '[' {
			for len(r.pending) < 32 {
				next, readErr := r.input.ReadByte()
				if readErr != nil {
					break
				}
				r.pending = append(r.pending, next)
				if next >= 0x40 && next <= 0x7e {
					break
				}
			}
		}

		switch string(r.pending) {
		case "\x1b[13;2u", "\x1b[27;2;13~", "\x1b[13;2~", "\x1b[106;5u":
			// Shift+Enter，以及 Kitty 协议中的 Ctrl+J。
			r.pending = append(r.pending[:0], readline.CharCtrlJ)
		case "\x1b[99;5u", "\x1b[27;5;99~":
			// Kitty / CSI-u 和 xterm modifyOtherKeys 中的 Ctrl+C。
			r.pending = append(r.pending[:0], readline.CharInterrupt)
		}
	}

	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *terminalKeyReader) Close() error {
	return r.closer.Close()
}

// multilineEditing 补充 readline 的跨行操作和多行显示，复用其缓冲区编辑。
// column 保留连续上下移动时希望到达的显示列。
type multilineEditing struct {
	display *inputDisplay
	line    []rune
	action  rune
	column  int
}

func (m *multilineEditing) filter(key rune) (rune, bool) {
	if key != readline.CharPrev && key != readline.CharNext {
		m.column = -1
	}
	switch key {
	case readline.CharCtrlJ, readline.CharLineStart, readline.CharLineEnd, readline.CharPrev, readline.CharNext:
		m.action = key
	default:
		return key, true
	}
	// Bell 本身不改动缓冲区，也不会提交输入。Listener 再完成上述操作，
	// 避免先让 readline 执行整段 Home/End 或切换历史。
	return readline.CharBell, true
}

func (m *multilineEditing) changed(line []rune, pos int, key rune) ([]rune, int, bool) {
	if key == 0 {
		m.column = -1
	}
	action := m.action
	m.action = 0
	start, end := lineBounds(line, pos)
	switch action {
	case readline.CharCtrlJ:
		updated := make([]rune, 0, len(line)+1)
		updated = append(updated, line[:pos]...)
		updated = append(updated, '\n')
		line = append(updated, line[pos:]...)
		pos++
	case readline.CharLineStart:
		pos = start
	case readline.CharLineEnd:
		pos = end
	case readline.CharPrev, readline.CharNext:
		rows := m.display.layout(line)
		row := inputCursorRow(rows, pos)
		if m.column < 0 {
			m.column = inputTextWidth(line[rows[row].start:pos])
		}
		if action == readline.CharPrev {
			if row == 0 {
				break
			}
			row--
		} else {
			if row == len(rows)-1 {
				break
			}
			row++
		}
		pos, end = rows[row].start, rows[row].end
		// 软折行的末尾位置同时也是下一行开头，不越过目标显示行。
		if row+1 < len(rows) && rows[row+1].start == end {
			end--
		}
		column := 0
		for pos < end && column+inputRuneWidth(line[pos]) <= m.column {
			column += inputRuneWidth(line[pos])
			pos++
		}
	}
	m.line = line
	m.display.render(line, pos)
	return line, pos, action != 0
}

func lineBounds(line []rune, pos int) (start, end int) {
	start, end = pos, pos
	for start > 0 && line[start-1] != '\n' {
		start--
	}
	for end < len(line) && line[end] != '\n' {
		end++
	}
	return start, end
}

// Input 为 Agent 提供用户输入。真实终端使用 readline 进行行编辑，管道输入保持逐行读取。
type Input struct {
	scanner *bufio.Scanner
	prompt  io.Writer

	lineEditor *readline.Instance
	multiline  *multilineEditing
	closeOnce  sync.Once
	closeErr   error
}

// NewInput 根据 interactive 选择终端行编辑器或普通的逐行读取器。
// 终端模式支持多行显示和上下左右编辑；上下键在首末行停止，不切换历史。
// Shift+Enter 或 Ctrl+J 在当前位置插入换行，Enter 提交整段输入。
func NewInput(stdin io.Reader, stdout, stderr io.Writer, interactive bool) (*Input, error) {
	if !interactive {
		scanner := bufio.NewScanner(stdin)
		scanner.Buffer(make([]byte, 1024), 1<<20)
		return &Input{scanner: scanner, prompt: stdout}, nil
	}

	stdinCloser, ok := stdin.(io.ReadCloser)
	if !ok {
		return nil, fmt.Errorf("interactive input requires a closable stdin")
	}
	multiline := &multilineEditing{column: -1, display: &inputDisplay{output: stdout}}
	config := &readline.Config{
		Stdin:        newTerminalKeyReader(stdinCloser),
		Stdout:       stdout,
		Stderr:       stderr,
		HistoryLimit: 100,
		// 输入解析、raw 模式和编辑由 readline 负责，重绘只走多行显示器。
		FuncIsTerminal:      func() bool { return false },
		FuncFilterInputRune: multiline.filter,
		Listener:            multiline.changed,
	}

	editor, err := readline.NewFromConfig(config)
	if err != nil {
		return nil, fmt.Errorf("initialize terminal input: %w", err)
	}
	// 扩展键盘协议跟随 raw 模式进入和退出。提交输入后恢复普通 Ctrl+C，
	// 模型或工具执行期间才能由终端生成 SIGINT，走已有的 context 取消流程。
	config = editor.GetConfig()
	makeRaw, exitRaw := config.FuncMakeRaw, config.FuncExitRaw
	var modeMu sync.Mutex
	extendedKeys := false
	config.FuncMakeRaw = func() error {
		modeMu.Lock()
		defer modeMu.Unlock()
		err := makeRaw()
		if !extendedKeys {
			_, _ = fmt.Fprint(stdout, enableExtendedKeys)
			extendedKeys = true
		}
		return err
	}
	config.FuncExitRaw = func() error {
		modeMu.Lock()
		defer modeMu.Unlock()
		if extendedKeys {
			_, _ = fmt.Fprint(stdout, disableExtendedKeys)
			extendedKeys = false
		}
		return exitRaw()
	}
	if err := editor.SetConfig(config); err != nil {
		_ = editor.Close()
		return nil, fmt.Errorf("configure terminal input: %w", err)
	}
	return &Input{lineEditor: editor, multiline: multiline}, nil
}

// Readline 读取一条完整输入。非终端模式仍将每一行视为一轮任务。
func (i *Input) Readline(prompt string) (string, error) {
	if i.lineEditor != nil {
		i.multiline.display.prompt = prompt
		i.lineEditor.SetPrompt(prompt)
		line, err := i.lineEditor.ReadLine()
		i.multiline.display.finish(i.multiline.line)
		if errors.Is(err, readline.ErrInterrupt) {
			return line, context.Canceled
		}
		return line, err
	}

	_, _ = fmt.Fprint(i.prompt, prompt)
	if i.scanner.Scan() {
		return i.scanner.Text(), nil
	}
	_, _ = fmt.Fprintln(i.prompt)
	if err := i.scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

// Close 结束终端读取并恢复终端模式。普通管道输入无需关闭。
func (i *Input) Close() error {
	i.closeOnce.Do(func() {
		if i.lineEditor != nil {
			i.closeErr = i.lineEditor.Close()
		}
	})
	return i.closeErr
}
