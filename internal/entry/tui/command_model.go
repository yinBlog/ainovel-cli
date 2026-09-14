package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

type modelRuntime interface {
	ConfiguredProviders() []string
	ConfiguredModelOptions(provider string) []host.ConfiguredModel
	CurrentModelSelection(role string) (string, string, bool)
	AvailableThinking(role string) []agentcore.ThinkingLevel
	CurrentThinking(role string) string
	SwitchModel(role, provider, model string) error
	SetRoleThinking(role, level string) error
	RoleFallbacks(role string) []bootstrap.ModelRef
	SetRoleFallbacks(role string, refs []bootstrap.ModelRef) error
}

type modelSwitchFocus int

const (
	modelFocusRole modelSwitchFocus = iota
	modelFocusProvider
	modelFocusModel
	modelFocusThinking
	modelFocusFallback
)

type modelRoleOption struct {
	Key   string
	Label string
}

var modelRoleOptions = []modelRoleOption{
	{Key: "default", Label: "默认"},

	{Key: "architect", Label: "Architect"},
	{Key: "writer", Label: "Writer"},
	{Key: "editor", Label: "Editor"},
}

type thinkingOption struct{ Key, Label string }

var allThinkingOptions = []thinkingOption{
	{"", "默认(继承)"},
	{"off", "关闭"},
	{"low", "低"},
	{"medium", "中"},
	{"high", "高"},
	{"xhigh", "极高"},
	{"max", "最高"},
}

func thinkingOptionsFor(rt modelRuntime, role string) []thinkingOption {
	levels := rt.AvailableThinking(role)
	if len(levels) == 0 {
		return []thinkingOption{allThinkingOptions[0]}
	}
	out := make([]thinkingOption, 0, len(levels))
	for _, level := range levels {
		key := string(level)
		for _, option := range allThinkingOptions {
			if option.Key == key {
				out = append(out, option)
				break
			}
		}
	}
	if len(out) == 0 {
		return []thinkingOption{allThinkingOptions[0]}
	}
	return out
}

func thinkingIndexOf(options []thinkingOption, level string) int {
	level = strings.ToLower(strings.TrimSpace(level))
	for i, o := range options {
		if o.Key == level {
			return i
		}
	}
	return 0 // 未知值 → 继承
}

type modelSwitchState struct {
	focus       modelSwitchFocus
	roleIdx     int
	providerIdx int
	modelIdx    int
	thinkingIdx int
	providers   []string
	models      []host.ConfiguredModel
	thinking    []thinkingOption
	// initialThinkingKey 记录面板打开时该角色强度字段的初始选中值。仅当用户实际移动了
	// 该字段才回写——存储的强度意图可能高于当前模型能力、面板无法呈现，不能因“没动”而误抹。
	initialThinkingKey string
	message            string

	// 备用渠道子编辑器：fallbacks 是草稿，Enter 应用时才与 initialFallbacks 比对回写。
	// 每项是独立的 provider+model，允许不同渠道挂不同模型。
	fallbackEditing  bool
	fallbacks        []bootstrap.ModelRef
	initialFallbacks []bootstrap.ModelRef
	fbCursor         int // 0..len(fallbacks)，末行是“＋ 添加备用渠道”
	fbColumn         int // 0=Provider，1=模型
}

func newModelSwitchState(rt modelRuntime, roleHint string) *modelSwitchState {
	state := &modelSwitchState{
		providers: rt.ConfiguredProviders(),
	}
	if len(state.providers) == 0 {
		state.message = "当前没有可用 provider"
	}

	roleHint = normalizeRoleKey(roleHint)
	for i, opt := range modelRoleOptions {
		if opt.Key == roleHint {
			state.roleIdx = i
			break
		}
	}
	state.syncSelection(rt)
	return state
}

func normalizeRoleKey(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "default":
		return "default"
	case "architect", "writer", "editor":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func (s *modelSwitchState) role() string {
	return modelRoleOptions[s.roleIdx].Key
}

func (s *modelSwitchState) roleLabel() string {
	return modelRoleOptions[s.roleIdx].Label
}

func (s *modelSwitchState) provider() string {
	if len(s.providers) == 0 || s.providerIdx < 0 || s.providerIdx >= len(s.providers) {
		return ""
	}
	return s.providers[s.providerIdx]
}

func (s *modelSwitchState) model() string {
	if len(s.models) == 0 || s.modelIdx < 0 || s.modelIdx >= len(s.models) {
		return ""
	}
	return s.models[s.modelIdx].Name
}

func (s *modelSwitchState) modelLabel() string {
	if len(s.models) == 0 || s.modelIdx < 0 || s.modelIdx >= len(s.models) {
		return ""
	}
	model := s.models[s.modelIdx]
	if window := formatContextWindow(model.ContextWindow); window != "" {
		return model.Name + " · " + window
	}
	return model.Name
}

func (s *modelSwitchState) thinkingKey() string {
	if s.thinkingIdx < 0 || s.thinkingIdx >= len(s.thinking) {
		return ""
	}
	return s.thinking[s.thinkingIdx].Key
}

func (s *modelSwitchState) thinkingLabel() string {
	if s.thinkingIdx < 0 || s.thinkingIdx >= len(s.thinking) {
		return allThinkingOptions[0].Label
	}
	return s.thinking[s.thinkingIdx].Label
}

func (s *modelSwitchState) moveFocus(delta int) {
	total := 5
	s.focus = modelSwitchFocus((int(s.focus) + delta + total) % total)
}

func (s *modelSwitchState) cycle(delta int, rt modelRuntime) {
	switch s.focus {
	case modelFocusRole:
		total := len(modelRoleOptions)
		s.roleIdx = (s.roleIdx + delta + total) % total
		s.syncSelection(rt)
	case modelFocusProvider:
		if len(s.providers) == 0 {
			return
		}
		total := len(s.providers)
		s.providerIdx = (s.providerIdx + delta + total) % total
		s.syncModels(rt, "")
	case modelFocusModel:
		if len(s.models) == 0 {
			return
		}
		total := len(s.models)
		s.modelIdx = (s.modelIdx + delta + total) % total
	case modelFocusThinking:
		total := len(s.thinking)
		if total == 0 {
			return
		}
		s.thinkingIdx = (s.thinkingIdx + delta + total) % total
	}
}

func (s *modelSwitchState) syncSelection(rt modelRuntime) {
	provider, model, _ := rt.CurrentModelSelection(s.role())
	if len(s.providers) > 0 {
		s.providerIdx = 0
		for i, candidate := range s.providers {
			if candidate == provider {
				s.providerIdx = i
				break
			}
		}
	}
	s.syncModels(rt, model)
	s.syncThinking(rt)
	s.syncFallbacks(rt)
	s.message = ""
}

func (s *modelSwitchState) syncModels(rt modelRuntime, preferred string) {
	s.models = rt.ConfiguredModelOptions(s.provider())
	s.modelIdx = 0
	if len(s.models) == 0 {
		return
	}
	preferred = strings.TrimSpace(preferred)
	for i, model := range s.models {
		if model.Name == preferred {
			s.modelIdx = i
			return
		}
	}
}

func (s *modelSwitchState) syncThinking(rt modelRuntime) {
	s.thinking = thinkingOptionsFor(rt, s.role())
	s.thinkingIdx = thinkingIndexOf(s.thinking, rt.CurrentThinking(s.role()))
	s.initialThinkingKey = s.thinkingKey()
}

func (s *modelSwitchState) apply(rt modelRuntime) error {
	if len(s.providers) == 0 {
		return fmt.Errorf("当前没有可用 provider")
	}
	if len(s.models) == 0 {
		return fmt.Errorf("provider %q 没有已配置模型", s.provider())
	}
	wantThinking := s.thinkingKey()
	if err := rt.SwitchModel(s.role(), s.provider(), s.model()); err != nil {
		return err
	}
	// 推理强度与模型正交：仅当用户实际移动了强度字段才回写，避免把面板无法呈现的
	// 高意图（当前模型能力不足）误抹成初始默认值。
	if wantThinking != s.initialThinkingKey {
		if err := rt.SetRoleThinking(s.role(), wantThinking); err != nil {
			return err
		}
	}
	// 备用渠道同样只在草稿真的变过时回写：一次回写会重建整套模型客户端并落盘。
	// 必须排在 SwitchModel 之后——它先把当前选择固化成该角色的主模型。
	if s.supportsFallbacks() && !sameFallbacks(s.fallbacks, s.initialFallbacks) {
		if err := rt.SetRoleFallbacks(s.role(), s.fallbacks); err != nil {
			return err
		}
		s.initialFallbacks = append([]bootstrap.ModelRef(nil), s.fallbacks...)
	}
	s.syncThinking(rt)
	return nil
}

func (m Model) handleModelSwitchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelSwitch == nil {
		return m, nil
	}
	state := m.modelSwitch
	if state.fallbackEditing {
		state.handleFallbackKey(msg, m.runtime)
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.modelSwitch = nil
		return m, m.textarea.Focus()
	case tea.KeyTab, tea.KeyDown:
		state.moveFocus(1)
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		state.moveFocus(-1)
		return m, nil
	case tea.KeyLeft:
		state.cycle(-1, m.runtime)
		return m, nil
	case tea.KeyRight:
		state.cycle(1, m.runtime)
		return m, nil
	case tea.KeyEnter:
		if state.focus == modelFocusFallback {
			if !state.supportsFallbacks() {
				state.message = "默认档不支持备用渠道，请切到 Architect/Writer/Editor"
				return m, nil
			}
			state.fallbackEditing = true
			state.message = ""
			return m, nil
		}
		if err := state.apply(m.runtime); err != nil {
			state.message = err.Error()
			return m, nil
		}
		m.modelSwitch = nil
		return m, tea.Batch(m.textarea.Focus(), fetchSnapshot(m.runtime))
	default:
		return m, nil
	}
}

func renderModelSwitchBar(width int, state *modelSwitchState) string {
	if state == nil || width <= 0 {
		return ""
	}

	titleText := "/model 切换模型"
	if state.fallbackEditing {
		titleText = "/model · " + state.roleLabel() + " 备用渠道"
	}
	title := lipgloss.NewStyle().
		Foreground(colorMuted).
		Bold(true).
		Render(titleText)

	var lines []string
	if state.fallbackEditing {
		lines = fallbackPanelLines(state)
	} else {
		hintText := "Tab 切字段   ←→ 切选项   Enter 应用   Esc 取消"
		switch state.focus {
		case modelFocusProvider:
			// 这里换渠道只动当前角色；整本书一起搬归 /channel。
			hintText = "←→ 换渠道（仅本角色）   整体切换用 /channel"
		case modelFocusFallback:
			hintText = "Tab 切字段   Enter 编辑备用渠道   Esc 取消"
		}
		lines = []string{
			renderModelField("角色", state.roleLabel(), state.focus == modelFocusRole),
			renderModelField("Provider", state.provider(), state.focus == modelFocusProvider),
			renderModelField("模型", state.modelLabel(), state.focus == modelFocusModel),
			renderModelField("推理强度", state.thinkingLabel(), state.focus == modelFocusThinking),
			renderModelField("备用渠道", state.fallbackSummary(), state.focus == modelFocusFallback),
			lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render(hintText),
		}
	}
	if state.message != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorError).Italic(true).Render(truncate(state.message, width-8)))
	}

	return renderCommandBox(title, lines, width, 56, 68)
}

func renderModelField(label, value string, focused bool) string {
	if strings.TrimSpace(value) == "" {
		value = "未设置"
	}
	labelText := lipgloss.NewStyle().
		Foreground(colorMuted).
		Width(12).
		Render(label + ":")
	style := lipgloss.NewStyle().Padding(0, 1).Foreground(bodyTextColor)
	if focused {
		style = style.Foreground(colorAccent).Bold(true).Underline(true)
	}
	return labelText + style.Render("["+value+"]")
}
