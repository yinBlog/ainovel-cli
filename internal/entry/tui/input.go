package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
)

const resetForeground = "\x1b[39m"

// highlightCommandToken 只给已确认的命令 token 着色，保留 textarea 原有的
// 光标、反色和换行 ANSI 序列。参数从第一个空白字符开始，始终使用正文颜色。
func highlightCommandToken(inputView, inputValue, commandToken string) string {
	if commandToken == "" {
		return inputView
	}
	fields := strings.Fields(inputValue)
	if len(fields) == 0 || fields[0] != commandToken {
		return inputView
	}
	plain := ansi.Strip(inputView)
	start := strings.Index(plain, commandToken)
	if start < 0 {
		return inputView
	}
	return highlightANSIByteRange(inputView, start, start+len(commandToken))
}

// highlightANSIByteRange 在剥离 ANSI 后的字节区间上覆盖前景色。区间内若遇到
// textarea 自己的 SGR（例如反色光标），会在其后重新下发强调色；区间结束只重置
// 前景色，不清掉光标的其他终端属性。
func highlightANSIByteRange(value string, start, end int) string {
	if start < 0 || end <= start {
		return value
	}
	marker := lipgloss.NewStyle().Foreground(colorAccent).Render("x")
	markerAt := strings.IndexByte(marker, 'x')
	if markerAt <= 0 {
		return value
	}
	accent := marker[:markerAt]

	var out strings.Builder
	out.Grow(len(value) + len(accent)*2 + len(resetForeground))
	plainPos := 0
	active := false
	var state byte
	for len(value) > 0 {
		sequence, _, size, nextState := ansi.DecodeSequence(value, state, nil)
		state = nextState
		plain := ansi.Strip(sequence)
		if plain == "" {
			out.WriteString(sequence)
			if active {
				out.WriteString(accent)
			}
			value = value[size:]
			continue
		}
		if !active && plainPos == start {
			out.WriteString(accent)
			active = true
		}
		out.WriteString(sequence)
		value = value[size:]
		plainPos += len(plain)
		if active && plainPos >= end {
			out.WriteString(resetForeground)
			active = false
		}
	}
	if active {
		out.WriteString(resetForeground)
	}
	return out.String()
}

// renderInputBox 渲染底部输入区：输入框、快捷键提示行、最底部用量状态栏。
// 输入框单独负责输入与提示，不承载启动模式栏。
func renderInputBox(inputView, hints string, snap host.UISnapshot, outputDir string, width int) string {
	innerW := width - 4 // border + padding
	if innerW < 12 {
		innerW = 12
	}

	// 输入行：提示符 + 输入框
	prompt := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("❯ ")
	inputLine := prompt + inputView

	// 提示行：快捷键独占一到两行——模型/花费等运行信息移入底部状态栏，
	// 操作说明允许按分隔符折行，不把最后一组快捷键截成省略号。
	line2 := renderShortcutHints(hints, innerW)

	// 输入区（单一盒子，避免视觉上出现双输入框）
	inputStyle := lipgloss.NewStyle().
		Width(width).
		Border(baseBorder, true, false, true, false).
		BorderForeground(colorDim).
		Padding(0, 1)
	inputBlock := inputStyle.Render(inputLine)

	// 提示行（无边框，紧贴下横线下方）
	hintStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 2)
	hintBlock := hintStyle.Render(line2)

	// 状态栏占用输入区原有的末尾空行：整块高度不变，layoutHeights 无需调整。
	statusBlock := hintStyle.Render(renderStatusBar(snap, outputDir, innerW))

	return inputBlock + "\n" + hintBlock + "\n" + statusBlock
}

// renderShortcutHints 把提示行里的按键染成低对比键帽，让用户可以先扫到操作、
// 再阅读说明。inputHints 的特殊告警整行已有强调色，这里只处理普通提示。
func renderShortcutHints(hints string, width int) string {
	plain := ansi.Strip(hints)
	if strings.HasPrefix(plain, "✂ ") || strings.HasPrefix(plain, "Press ") {
		return fitInlineLine(hints, width)
	}

	dim := lipgloss.NewStyle().Foreground(colorDim)
	key := lipgloss.NewStyle().Foreground(colorAccent).Background(colorKeyBg).Bold(true)
	parts := strings.Split(plain, " · ")
	for i, part := range parts {
		name, rest, found := strings.Cut(part, " ")
		if !found || !isShortcutKey(name) {
			parts[i] = dim.Render(part)
			continue
		}
		parts[i] = key.Render(name)
		if rest != "" {
			parts[i] += dim.Render(" " + rest)
		}
	}
	separator := dim.Render(" · ")
	var lines []string
	line := ""
	for _, part := range parts {
		candidate := part
		if line != "" {
			candidate = line + separator + part
		}
		if line != "" && ansi.StringWidth(candidate) > width {
			lines = append(lines, line)
			line = part
			continue
		}
		line = candidate
	}
	if line != "" {
		lines = append(lines, line)
	}
	// 常规工作台提示最多占两行，避免窗口很矮时把正文挤没；按分隔符换行后，
	// 绝大多数终端尺寸都能完整展示所有操作。
	if len(lines) > 2 {
		lines = lines[:2]
		lines[1] = fitInlineLine(lines[1], width)
	}
	return strings.Join(lines, "\n")
}

func isShortcutKey(value string) bool {
	switch value {
	case "/", "Tab", "Tab/Shift+Tab", "Enter", "Esc", "End", "Ctrl+L", "Ctrl+R", "Ctrl+S", "Ctrl+P/N", "Ctrl+↑↓", "↑↓", "↑↓/PgUp":
		return true
	default:
		return false
	}
}

func joinInlineSides(left, right string, width int) string {
	if width <= 0 {
		return left + right
	}
	if strings.TrimSpace(right) == "" {
		return fitInlineLine(left, width)
	}

	right = fitInlineLine(right, width)
	rightW := ansi.StringWidth(right)
	if rightW >= width {
		return right
	}

	leftMax := width - rightW - 1
	if leftMax < 0 {
		leftMax = 0
	}
	left = fitInlineLine(left, leftMax)
	gap := width - ansi.StringWidth(left) - rightW
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func fitInlineLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, "...")
}
