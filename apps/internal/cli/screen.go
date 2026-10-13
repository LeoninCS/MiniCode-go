package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
)

const maxInputRows = 6

// screenEntry 保留本次运行的显示内容，窗口变化时重新排版。
type screenEntry struct {
	content  string
	markdown bool
	width    int
	rendered string
}

// sessionScreen 统一管理会话的备用屏幕，回复区位于固定底部输入区上方。
// 工作协程独占重绘；输入、输出和键盘协议控制序列共用同一把锁。
type sessionScreen struct {
	stdout    io.Writer
	mu        sync.Mutex
	entries   []*screenEntry
	stream    *screenEntry
	prompt    string
	input     string
	pos       int
	editing   bool
	top       int
	scroll    int
	dirty     bool
	closed    bool
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newSessionScreen(stdout io.Writer) *sessionScreen {
	s := &sessionScreen{
		stdout: stdout, prompt: "> ", dirty: true,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	_, _ = io.WriteString(stdout, "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")
	go s.run()
	return s
}

func (s *sessionScreen) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sessionScreen) add(content string, markdown bool) {
	s.addEntry(&screenEntry{content: previewText(content), markdown: markdown})
}

func (s *sessionScreen) addEntry(entry *screenEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.entries = append(s.entries, entry)
	s.scroll = 0
	s.dirty = true
	s.notify()
}

func (s *sessionScreen) appendStream(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.stream == nil {
		s.stream = &screenEntry{markdown: true}
		s.notify()
	}
	s.stream.content += content
	s.stream.width = 0
	s.dirty = true
}

func (s *sessionScreen) endStream() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stream != nil {
		s.stream = nil
		s.dirty = true
		s.notify()
	}
}

func (s *sessionScreen) setInput(prompt, input string, pos int, editing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.prompt, s.input, s.pos, s.editing = prompt, input, pos, editing
	s.dirty = true
	s.notify()
}

func (s *sessionScreen) finishInput(prompt, input string, submitted bool) {
	if submitted && input != "" {
		s.add(prompt+input, false)
	}
	s.setInput("> ", "", 0, false)
}

func (s *sessionScreen) writeControl(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		_, _ = io.WriteString(s.stdout, content)
	}
}

func (s *sessionScreen) page(direction int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, height := outputSize(s.stdout)
	s.scroll = max(0, s.scroll+direction*max(1, height-3))
	s.dirty = true
	s.notify()
}

func (s *sessionScreen) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.stop)
		<-s.done
	})
}

func (s *sessionScreen) run() {
	defer close(s.done)
	defer func() {
		_, _ = io.WriteString(s.stdout, "\x1b[0m\x1b[?25h\x1b[?1049l")
	}()
	ticker := time.NewTicker(streamRefreshInterval)
	defer ticker.Stop()
	resized, stopResize := screenResizeSignals()
	defer stopResize()
	var renderer *glamour.TermRenderer
	var width, height int
	render := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		nextWidth, nextHeight := outputSize(s.stdout)
		nextWidth = max(1, nextWidth-1)
		if !s.dirty && nextWidth == width && nextHeight == height {
			return
		}
		if nextWidth != width {
			renderer, _ = newMarkdownRenderer(max(10, nextWidth))
		}
		width, height = nextWidth, nextHeight
		_, _ = io.WriteString(s.stdout, s.frame(width, height, renderer))
		s.dirty = false
	}
	render()
	for {
		select {
		case <-s.stop:
			render()
			return
		case <-s.wake:
			render()
		case <-resized:
			s.mu.Lock()
			s.dirty = true
			s.mu.Unlock()
			render()
		case <-ticker.C:
			render()
		}
	}
}

// frame 按终端行列定位，避免输出或软折行把底部输入区滚出屏幕。
func (s *sessionScreen) frame(width, height int, renderer *glamour.TermRenderer) string {
	prompt := screenInputPrompt(s.prompt, width)
	line := []rune(s.input)
	pos := min(max(0, s.pos), len(line))
	inputRows := layoutInput(line, prompt, width+1)
	cursorRow := inputCursorRow(inputRows, pos)
	inputHeight := min(len(inputRows), max(1, min(maxInputRows, height/3)))
	s.top = min(s.top, max(0, len(inputRows)-inputHeight))
	if cursorRow < s.top {
		s.top = cursorRow
	} else if cursorRow >= s.top+inputHeight {
		s.top = cursorRow - inputHeight + 1
	}
	inputStart := height - inputHeight
	conversationHeight := max(0, inputStart-1)
	var rows []string
	for _, entry := range s.entries {
		rows = append(rows, entry.rows(width, renderer)...)
	}
	if s.stream != nil {
		rows = append(rows, s.stream.rows(width, renderer)...)
	}
	s.scroll = min(s.scroll, max(0, len(rows)-conversationHeight))
	end := len(rows) - s.scroll
	rows = rows[max(0, end-conversationHeight):end]
	var frame strings.Builder
	frame.WriteString("\x1b[?2026h\x1b[?25l\x1b[H\x1b[0m")
	for row := range height {
		fmt.Fprintf(&frame, "\x1b[%d;1H\x1b[0m\x1b[2K", row+1)
		switch {
		case row < conversationHeight:
			if row < len(rows) {
				frame.WriteString(rows[row])
			}
		case row == inputStart-1:
			frame.WriteString(ansiDim + strings.Repeat("─", width) + ansiReset)
		case row >= inputStart:
			index := s.top + row - inputStart
			if index == 0 {
				frame.WriteString(prompt)
			}
			frame.WriteString(strings.ReplaceAll(string(line[inputRows[index].start:inputRows[index].end]), "\t", "    "))
		}
	}
	column := inputTextWidth(line[inputRows[cursorRow].start:pos])
	if cursorRow == 0 {
		column += ansi.StringWidth(prompt)
	}
	fmt.Fprintf(&frame, "\x1b[0m\x1b[%d;%dH", inputStart+cursorRow-s.top+1, min(width, column)+1)
	if s.editing {
		frame.WriteString("\x1b[?25h")
	}
	frame.WriteString("\x1b[?2026l")
	return frame.String()
}

func screenInputPrompt(prompt string, width int) string {
	return ansi.Truncate(previewText(prompt), max(1, width-1), "")
}

func (entry *screenEntry) rows(width int, renderer *glamour.TermRenderer) []string {
	if entry.width != width {
		entry.rendered = entry.content
		if entry.markdown {
			entry.rendered = previewText(entry.content)
			if renderer != nil {
				if rendered, err := renderer.Render(entry.rendered); err == nil {
					entry.rendered = rendered
				}
			}
		}
		entry.rendered = ansi.Hardwrap(strings.ReplaceAll(entry.rendered, "\t", "    "), width, true)
		entry.width = width
	}
	return strings.Split(strings.Trim(entry.rendered, "\r\n"), "\n")
}
