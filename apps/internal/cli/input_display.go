package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-runewidth"
)

// inputRow 是一行实际显示的文本范围，不包含换行符。软折行的相邻范围连续。
type inputRow struct {
	start int
	end   int
}

// inputDisplay 统一计算多行显示和光标位置。readline 的缓冲区编辑仍被复用，
// 但其内置重绘在真实换行叠加自动折行时会错位，因此在此统一重绘。
type inputDisplay struct {
	output    io.Writer
	prompt    string
	cursorRow int
	top       int
}

func (d *inputDisplay) size() (width, height int) {
	if IsTerminal(d.output) {
		width, height, _ = term.GetSize(d.output.(*os.File).Fd())
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func (d *inputDisplay) layout(line []rune) []inputRow {
	width, _ := d.size()
	// 留一列给行尾光标，避免终端的延迟自动折行与显式换行相互干扰。
	limit := max(1, width-1)
	column := runewidth.StringWidth(d.prompt)
	start := 0
	var rows []inputRow
	for pos, r := range line {
		if r == '\n' {
			rows = append(rows, inputRow{start: start, end: pos})
			start, column = pos+1, 0
			continue
		}
		w := inputRuneWidth(r)
		if column+w > limit && pos > start {
			rows = append(rows, inputRow{start: start, end: pos})
			start, column = pos, 0
		}
		column += w
	}
	return append(rows, inputRow{start: start, end: len(line)})
}

func inputCursorRow(rows []inputRow, pos int) int {
	for row := len(rows) - 1; row > 0; row-- {
		if pos >= rows[row].start {
			return row
		}
	}
	return 0
}

func inputRuneWidth(r rune) int {
	if r == '\t' {
		return 4
	}
	return runewidth.RuneWidth(r)
}

func inputTextWidth(line []rune) (width int) {
	for _, r := range line {
		width += inputRuneWidth(r)
	}
	return width
}

func (d *inputDisplay) moveToTop(out *strings.Builder) {
	out.WriteByte('\r')
	if d.cursorRow > 0 {
		fmt.Fprintf(out, "\x1b[%dA", d.cursorRow)
	}
	out.WriteString("\x1b[J")
}

func (d *inputDisplay) writeRows(out *strings.Builder, line []rune, rows []inputRow, start, end int) {
	for row := start; row < end; row++ {
		if row > start {
			out.WriteString("\r\n")
		}
		if row == 0 {
			out.WriteString(d.prompt)
		}
		for _, r := range line[rows[row].start:rows[row].end] {
			if r == '\t' {
				out.WriteString("    ")
			} else {
				out.WriteRune(r)
			}
		}
	}
}

func (d *inputDisplay) render(line []rune, pos int) {
	rows := d.layout(line)
	row := inputCursorRow(rows, pos)
	_, height := d.size()
	height = max(1, height-1)
	d.top = min(d.top, max(0, len(rows)-height))
	if row < d.top {
		d.top = row
	} else if row >= d.top+height {
		d.top = row - height + 1
	}
	end := min(len(rows), d.top+height)
	var out strings.Builder
	out.WriteString("\x1b[?25l")
	d.moveToTop(&out)
	d.writeRows(&out, line, rows, d.top, end)
	if up := end - row - 1; up > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", up)
	}
	column := inputTextWidth(line[rows[row].start:pos])
	if row == 0 {
		column += runewidth.StringWidth(d.prompt)
	}
	fmt.Fprintf(&out, "\r\x1b[%dG\x1b[?25h", column+1)
	fmt.Fprint(d.output, out.String())
	d.cursorRow = row - d.top
}

// finish 将完整输入留在终端记录中，并把光标放到输入之后供模型回复使用。
func (d *inputDisplay) finish(line []rune) {
	var out strings.Builder
	d.moveToTop(&out)
	rows := d.layout(line)
	d.writeRows(&out, line, rows, 0, len(rows))
	out.WriteString("\r\n")
	fmt.Fprint(d.output, out.String())
	d.cursorRow, d.top = 0, 0
}
