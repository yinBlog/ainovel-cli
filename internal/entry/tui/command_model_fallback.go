package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// 备用渠道子编辑器。备用链是有序的：主模型失败时按行序依次尝试，每行是独立的
// provider+model 组合，所以不同渠道可以挂不同模型（同名模型也可换渠道重试）。
//
// 只有显式覆盖了主模型的角色才有备用链——默认档跑的是共享的默认模型，
// 给它配备用渠道在运行时无处生效，面板直接拒绝。

const (
	fallbackProviderCellWidth = 16
	fallbackModelCellWidth    = 28
)

func (s *modelSwitchState) supportsFallbacks() bool {
	return s.role() != "default"
}

func (s *modelSwitchState) syncFallbacks(rt modelRuntime) {
	s.fallbackEditing = false
	s.fbCursor, s.fbColumn = 0, 0
	if !s.supportsFallbacks() {
		s.fallbacks, s.initialFallbacks = nil, nil
		return
	}
	s.fallbacks = rt.RoleFallbacks(s.role())
	s.initialFallbacks = append([]bootstrap.ModelRef(nil), s.fallbacks...)
}

// fallbackSummary 是主面板那一行的浓缩显示；完整链在子编辑器里看。
func (s *modelSwitchState) fallbackSummary() string {
	if !s.supportsFallbacks() {
		return "不适用（默认档）"
	}
	if len(s.fallbacks) == 0 {
		return "未设置"
	}
	parts := make([]string, 0, len(s.fallbacks))
	for _, ref := range s.fallbacks {
		parts = append(parts, ref.Provider+"/"+ref.Model)
	}
	return truncate(strings.Join(parts, " → "), 36)
}

func sameFallbacks(a, b []bootstrap.ModelRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *modelSwitchState) handleFallbackKey(msg tea.KeyMsg, rt modelRuntime) {
	rows := len(s.fallbacks) + 1 // 末行是“＋ 添加备用渠道”
	switch msg.Type {
	case tea.KeyEsc:
		s.fallbackEditing = false
		s.message = ""
	case tea.KeyShiftUp, tea.KeyCtrlUp:
		s.moveFallback(-1)
	case tea.KeyShiftDown, tea.KeyCtrlDown:
		s.moveFallback(1)
	case tea.KeyUp:
		s.fbCursor = (s.fbCursor - 1 + rows) % rows
	case tea.KeyDown:
		s.fbCursor = (s.fbCursor + 1) % rows
	case tea.KeyTab, tea.KeyShiftTab:
		if s.fbCursor < len(s.fallbacks) {
			s.fbColumn = 1 - s.fbColumn
		}
	case tea.KeyLeft:
		s.cycleFallbackValue(-1, rt)
	case tea.KeyRight:
		s.cycleFallbackValue(1, rt)
	case tea.KeyDelete, tea.KeyBackspace:
		s.removeFallback()
	case tea.KeyEnter:
		if s.fbCursor >= len(s.fallbacks) {
			s.addFallback(rt)
			return
		}
		// 在已有行上 Enter = 确认返回主面板；真正落盘仍在主面板 Enter。
		s.fallbackEditing = false
		s.message = ""
	}
}

func (s *modelSwitchState) cycleFallbackValue(delta int, rt modelRuntime) {
	if s.fbCursor < 0 || s.fbCursor >= len(s.fallbacks) {
		return
	}
	entry := s.fallbacks[s.fbCursor]
	if s.fbColumn == 0 {
		if len(s.providers) == 0 {
			return
		}
		idx := indexOfProvider(s.providers, entry.Provider)
		idx = (idx + delta + len(s.providers)) % len(s.providers)
		entry.Provider = s.providers[idx]
		// 换渠道后原模型名多半不属于新渠道，落回新渠道的第一个模型。
		entry.Model = firstModelOf(rt, entry.Provider)
	} else {
		models := rt.ConfiguredModelOptions(entry.Provider)
		if len(models) == 0 {
			return
		}
		idx := 0
		for i, model := range models {
			if model.Name == entry.Model {
				idx = i
				break
			}
		}
		idx = (idx + delta + len(models)) % len(models)
		entry.Model = models[idx].Name
	}
	s.fallbacks[s.fbCursor] = entry
	s.message = ""
}

func (s *modelSwitchState) addFallback(rt modelRuntime) {
	provider, model := s.suggestFallback(rt)
	if provider == "" || model == "" {
		s.message = "没有可用的渠道/模型，请先用 /channel 添加"
		return
	}
	s.fallbacks = append(s.fallbacks, bootstrap.ModelRef{Provider: provider, Model: model})
	s.fbCursor = len(s.fallbacks) - 1
	s.fbColumn = 0
	s.message = ""
}

// suggestFallback 挑一个既不等于主模型、也不在链上的组合作为新行默认值；
// 全被占满时退回第一个可用组合，由用户自己用 ←→ 调整。
func (s *modelSwitchState) suggestFallback(rt modelRuntime) (string, string) {
	used := make(map[string]bool, len(s.fallbacks)+1)
	used[s.provider()+"/"+s.model()] = true
	for _, ref := range s.fallbacks {
		used[ref.Provider+"/"+ref.Model] = true
	}
	firstProvider, firstModel := "", ""
	for _, provider := range s.providers {
		for _, model := range rt.ConfiguredModelOptions(provider) {
			if firstProvider == "" {
				firstProvider, firstModel = provider, model.Name
			}
			if !used[provider+"/"+model.Name] {
				return provider, model.Name
			}
		}
	}
	return firstProvider, firstModel
}

func (s *modelSwitchState) removeFallback() {
	if s.fbCursor < 0 || s.fbCursor >= len(s.fallbacks) {
		return
	}
	s.fallbacks = append(s.fallbacks[:s.fbCursor], s.fallbacks[s.fbCursor+1:]...)
	if s.fbCursor > len(s.fallbacks) {
		s.fbCursor = len(s.fallbacks)
	}
	s.fbColumn = 0
	s.message = ""
}

// moveFallback 调整优先级：备用链按行序尝试，所以顺序就是策略。
func (s *modelSwitchState) moveFallback(delta int) {
	from, to := s.fbCursor, s.fbCursor+delta
	if from < 0 || from >= len(s.fallbacks) || to < 0 || to >= len(s.fallbacks) {
		return
	}
	s.fallbacks[from], s.fallbacks[to] = s.fallbacks[to], s.fallbacks[from]
	s.fbCursor = to
	s.message = ""
}

func indexOfProvider(providers []string, name string) int {
	for i, provider := range providers {
		if provider == name {
			return i
		}
	}
	return 0
}

func firstModelOf(rt modelRuntime, provider string) string {
	models := rt.ConfiguredModelOptions(provider)
	if len(models) == 0 {
		return ""
	}
	return models[0].Name
}

func fallbackPanelLines(state *modelSwitchState) []string {
	labelStyle := lipgloss.NewStyle().Foreground(colorMuted)
	lines := []string{
		labelStyle.Width(12).Render("主模型:") +
			lipgloss.NewStyle().Foreground(bodyTextColor).Padding(0, 1).
				Render(truncate(state.provider()+"/"+state.model(), 40)),
		"",
	}
	for i, ref := range state.fallbacks {
		active := state.fbCursor == i
		lines = append(lines, fmt.Sprintf("%s%d. %s %s",
			fallbackCursorMark(active),
			i+1,
			fallbackCell(ref.Provider, fallbackProviderCellWidth, active && state.fbColumn == 0),
			fallbackCell(ref.Model, fallbackModelCellWidth, active && state.fbColumn == 1),
		))
	}
	addStyle := lipgloss.NewStyle().Foreground(colorDim)
	if state.fbCursor >= len(state.fallbacks) {
		addStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	}
	lines = append(lines,
		fallbackCursorMark(state.fbCursor >= len(state.fallbacks))+addStyle.Render("＋ 添加备用渠道"),
		"",
		lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("↑↓ 选行   Tab 切列   ←→ 改值   Shift+↑↓ 调序"),
		lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("Del 删除   Enter 确认   Esc 返回（主面板 Enter 才落盘）"),
	)
	return lines
}

func fallbackCursorMark(active bool) string {
	if active {
		return lipgloss.NewStyle().Foreground(colorAccent).Render("▸ ")
	}
	return "  "
}

// 焦点单元格靠颜色和下划线区分；方括号是无色终端下的兜底提示（与 renderModelField 一致）。
func fallbackCell(value string, width int, focused bool) string {
	style := lipgloss.NewStyle().Width(width + 2).Foreground(bodyTextColor)
	if focused {
		style = style.Foreground(colorAccent).Bold(true).Underline(true)
	}
	return style.Render("[" + truncate(value, width) + "]")
}
