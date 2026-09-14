package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// 只读面板的折行工具。
//
// 这些页面存在的唯一理由就是让人看清内容，用 … 把内容吞掉等于白开一次面板。
// 面板本来就在可滚动的 viewport 里，多占几行的代价远小于看不到。
// 真正需要保持单行的只有带光标的列表（行高要可预测），那些地方改用 Tab 展开。

// wrapIndented 把一段文本按宽度折行，续行缩进对齐。返回的每一行都已上色。
// width 是可用总宽，indent 是续行相对首行的缩进列数。
func wrapIndented(text string, width, indent int, style lipgloss.Style) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	body := width - indent
	if body < 8 {
		body = max(8, width)
		indent = 0
	}
	pad := strings.Repeat(" ", indent)
	var out []string
	for i, line := range strings.Split(wrapText(text, body), "\n") {
		if i == 0 {
			out = append(out, style.Render(line))
			continue
		}
		out = append(out, style.Render(pad+strings.TrimLeft(line, " ")))
	}
	return out
}

// writeIndented 把折行结果依次写进 builder，每行一个换行。
// 与 panels_outline.go 的 writeWrapped 的区别只在于续行缩进。
func writeIndented(b *strings.Builder, text string, width, indent int, style lipgloss.Style) {
	for _, line := range wrapIndented(text, width, indent, style) {
		b.WriteString(line)
		b.WriteString("\n")
	}
}
