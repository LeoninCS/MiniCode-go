package cli

import (
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
)

const streamRefreshInterval = 50 * time.Millisecond

// streamPreview 的工作协程独占渲染器和终端写入。关闭时等待协程退出，
// 保证恢复主屏幕后不会再出现预览帧，也不会保留上一次模型调用的文本。
type streamPreview struct {
	stdout   io.Writer
	mu       sync.Mutex
	content  strings.Builder
	revision uint64
	stop     chan struct{}
	done     chan struct{}
}

func newStreamPreview(stdout io.Writer, content string) *streamPreview {
	p := &streamPreview{stdout: stdout, stop: make(chan struct{}), done: make(chan struct{})}
	p.append(content)
	_, _ = io.WriteString(stdout, "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")
	go p.run()
	return p
}

func (p *streamPreview) append(content string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.content.WriteString(content)
	p.revision++
}

func (p *streamPreview) snapshot() (string, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.content.String(), p.revision
}

func (p *streamPreview) close() {
	close(p.stop)
	<-p.done
}

func (p *streamPreview) run() {
	defer close(p.done)
	defer func() {
		_, _ = io.WriteString(p.stdout, "\x1b[0m\x1b[?25h\x1b[?1049l")
	}()
	ticker := time.NewTicker(streamRefreshInterval)
	defer ticker.Stop()

	var renderer *glamour.TermRenderer
	var revision uint64
	var width, height int
	var previous string
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		text, nextRevision := p.snapshot()
		nextWidth, nextHeight := outputSize(p.stdout)
		// 留一列，避免行尾的延迟自动折行与显式换行叠加。
		nextWidth = max(1, nextWidth-1)
		resized := nextWidth != width || nextHeight != height
		if nextRevision != revision || resized {
			if nextWidth != width {
				// 极窄终端的样式边距可能超过宽度，先正常排版，再按视口折行。
				renderer, _ = newMarkdownRenderer(max(10, nextWidth))
			}
			width, height, revision = nextWidth, nextHeight, nextRevision
			text = previewText(text)
			rendered := text
			if renderer != nil {
				if result, err := renderer.Render(text); err == nil {
					rendered = result
				}
			}
			frame := previewFrame(rendered, width, height)
			select {
			case <-p.stop:
				return
			default:
			}
			if frame != previous || resized {
				_, _ = io.WriteString(p.stdout, frame)
				previous = frame
			}
		}
		select {
		case <-p.stop:
			return
		case <-ticker.C:
		}
	}
}

// 原文回退同样只显示文本；控制序列不能参与预览的光标与屏幕管理。
func previewText(text string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' && r != '\n' && r != '\t' || r == '\x7f' {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

// previewFrame 保留末尾可见行，自动跟随新内容。逐行清除旧内容，
// 再清除剩余行；单次同步更新避免重排时先清空整屏造成闪烁。
func previewFrame(rendered string, width, height int) string {
	rendered = strings.ReplaceAll(rendered, "\t", "    ")
	rows := strings.Split(strings.Trim(ansi.Hardwrap(rendered, width, true), "\r\n"), "\n")
	rows = rows[max(0, len(rows)-height):]
	return "\x1b[?2026h\x1b[H\x1b[0m" + strings.Join(rows, "\x1b[K\r\n") + "\x1b[0m\x1b[K\x1b[J\x1b[?2026l"
}
