package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// 浮在输入框上方的命令浮层：宽度按内容自适应、夹在 [minBox, maxBox] 之间，
// 高度等于内容高度。保证盒子是规整矩形——任何一行超出内宽都会把右边框挤歪。
func renderCommandBox(title string, lines []string, width, minBox, maxBox int) string {
	if width <= 0 || len(lines) == 0 {
		return ""
	}
	boxW := lipgloss.Width(strings.Join(lines, "\n")) + 8
	maxW := width - 2
	if maxW > maxBox {
		maxW = maxBox
	}
	if boxW > maxW {
		boxW = maxW
	}
	if boxW < minBox {
		boxW = minBox
	}

	innerW := boxW - 2
	if innerW < 16 {
		innerW = 16
	}
	sepW := innerW - lipgloss.Width(title) - 3
	if sepW < 0 {
		sepW = 0
	}
	lineStyle := lipgloss.NewStyle().Foreground(colorDim)
	topBorder := lineStyle.Render("┌─ ") + title + lineStyle.Render(" "+strings.Repeat("─", sepW)+"┐")
	bottomBorder := lineStyle.Render("└" + strings.Repeat("─", innerW) + "┘")

	body := make([]string, 0, len(lines))
	for _, line := range lines {
		// 内容按宽度排版是各面板自己的事；这里做最后一道兜底截断，
		// 保证任何调用者都拿不到一个被挤歪的盒子。
		if lipgloss.Width(line) > innerW {
			line = truncateStyledWidth(line, innerW)
		}
		padding := innerW - lipgloss.Width(line)
		if padding < 0 {
			padding = 0
		}
		body = append(body, lineStyle.Render("│")+line+strings.Repeat(" ", padding)+lineStyle.Render("│"))
	}
	return strings.Join(append(append([]string{topBorder}, body...), bottomBorder), "\n")
}
