package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type helpState struct {
	viewport viewport.Model
}

func newHelpState(width, height int) *helpState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	text := renderHelpText(contentW)

	vp := viewport.New(contentW, boxH-4)
	vp.SetContent(text)
	return &helpState{viewport: vp}
}

func renderHelpText(width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	usageStyle := lipgloss.NewStyle().Foreground(colorMuted)
	descStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim)

	var b strings.Builder
	b.WriteString(titleStyle.Render("命令"))
	b.WriteString(usageStyle.Render("  一行一条：用法 → 说明；分组按 系统 / 分析 / 写作"))
	b.WriteString("\n")

	// 用法列宽按最长用法自适应（上限 40），说明列吃掉剩余宽度。
	specs := commandSpecs()
	usageW := 0
	for _, spec := range specs {
		if w := lipgloss.Width(spec.Usage); w > usageW {
			usageW = w
		}
	}
	usageW = min(max(usageW, 12), 40)
	lastGroup := ""
	for _, spec := range specs {
		if spec.Group != lastGroup {
			b.WriteString(hintStyle.Render(commandGroupLabel(spec.Group)))
			b.WriteString("\n")
			lastGroup = spec.Group
		}
		usage := spec.Usage
		if len(spec.Aliases) > 0 {
			usage += " (/" + strings.Join(spec.Aliases, " /") + ")"
		}
		usageCell := lipgloss.NewStyle().Width(usageW).Render(truncate(usage, usageW))
		b.WriteString("  ")
		b.WriteString(nameStyle.Render(usageCell))
		b.WriteString("  ")
		// 说明折行而不是截断：帮助页被截掉的那半句往往正是"这条命令到底干什么"。
		writeIndented(&b, spec.Description, width, usageW+4, descStyle)
	}

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("快捷键"))
	b.WriteString("\n")
	for _, line := range []string{
		"输入 / 搜索命令",
		"↑↓ 选择命令候选",
		"Tab/Enter 接受补全",
		"Esc 关闭当前命令面板",
		"Ctrl+↑ / Ctrl+↓ 放大 / 缩小实时输出面板（事件流相应缩放，20%–80%）",
		"书架 ↑↓ 选书 · Enter 切到该书创作 · → 只看章节 · 末行 ＋ 新开一本书",
		"章节 ↑↓ 选章 · Enter 阅读 · Tab 展开该章标题与核心事件",
		"检索 ↑↓ 选命中 · Enter 跳到该章 · Tab 展开完整上下文",
		"阅读 n/p 或 ←→ 翻章 · Esc 逐级返回",
		"渠道 ↑↓ 选渠道 · Enter 整体切换全部角色 · E 编辑该渠道定义",
		"Ctrl+R 切换选中复制模式（关闭鼠标上报后可拖拽选中复制，再按一次恢复）",
	} {
		b.WriteString(hintStyle.Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

func commandGroupLabel(group string) string {
	switch group {
	case "system":
		return "系统"
	case "analysis":
		return "分析 · 只读，运行中可用"
	case "writing":
		return "写作"
	}
	return group
}

func renderHelpModal(width, height int, state *helpState) string {
	if state == nil {
		return ""
	}

	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)

	if state.viewport.Width != contentW {
		state.viewport.Width = contentW
	}
	if state.viewport.Height != boxH-4 {
		state.viewport.Height = boxH - 4
	}

	modal := renderPaddedModalFrame(
		boxW,
		boxH,
		"命令帮助",
		"  ↑↓ 滚动 · Esc 关闭",
		strings.Split(state.viewport.View(), "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.help == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.help = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.help.viewport.ScrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.help.viewport.ScrollDown(1)
		return m, nil
	case tea.KeyPgUp:
		m.help.viewport.HalfPageUp()
		return m, nil
	case tea.KeyPgDown:
		m.help.viewport.HalfPageDown()
		return m, nil
	default:
		return m, nil
	}
}
