package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

// 渠道面板的“整体切换”部分。渠道的定义（协议/凭证/模型库）和“把整本书搬到哪家”
// 是同一个对象的两面，所以共用 /channel 这一个面板、同一张渠道表：
// 光标停在某个渠道上时，Enter 整体切换，E 进去编辑它的定义。
//
// 第三方中转很多，不同家的模型名往往对不上，所以按 Enter 之前先把每个角色会落到
// 哪个模型、依据是什么整张摆出来——“全部切换”只有先看得见才敢按。

const (
	channelNameCellWidth   = 20
	channelSlotCellWidth   = 11 // 去向表里的角色名列
	channelSourceCellWidth = 14 // “该渠道上次用的”这类依据说明
)

// channelSwitcher 是渠道面板需要的运行时能力，独立成接口便于测试。
type channelSwitcher interface {
	PlanChannelSwitch(provider string) ([]bootstrap.ChannelSlotPlan, error)
	SwitchChannel(provider string) error
}

// renderChannelChoices 渲染渠道表：每行报出协议、模型数、是否当前默认、在用几个角色。
// 最后一行是“新增渠道”入口。
func renderChannelChoices(state *modelConfigState, contentW int, limit int) []string {
	start, end := configWindow(len(state.providerChoices), state.cursor, limit)
	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		choice := state.providerChoices[i]
		focused := i == state.cursor
		if choice.existing == nil {
			lines = append(lines, fallbackCursorMark(focused)+channelAddLabel(focused, choice.label))
			continue
		}
		lines = append(lines, renderChannelRow(*choice.existing, focused, contentW))
	}
	if end-start < len(state.providerChoices) {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).
			Render(fmt.Sprintf("  …共 %d 个渠道", len(state.providerChoices)-1)))
	}
	return lines
}

func channelAddLabel(focused bool, label string) string {
	style := lipgloss.NewStyle().Foreground(colorDim)
	if focused {
		style = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	}
	return style.Render(label)
}

func renderChannelRow(channel host.ProviderSnapshot, focused bool, contentW int) string {
	nameStyle := lipgloss.NewStyle().Width(channelNameCellWidth).Foreground(bodyTextColor)
	if focused {
		nameStyle = nameStyle.Foreground(colorAccent).Bold(true)
	}
	return fallbackCursorMark(focused) +
		nameStyle.Render(truncate(channel.Name, channelNameCellWidth)) +
		lipgloss.NewStyle().Foreground(colorDim).
			Render(channelFacts(channel, contentW-channelNameCellWidth-2))
}

// channelFacts 按重要性排序渠道事实，宽度不够时从尾部丢整条，而不是把字符串拦腰截断。
// “缺 Key”决定这个渠道能不能用，排最前且永不被丢；角色占用只是参考，先丢它。
func channelFacts(channel host.ProviderSnapshot, width int) string {
	facts := make([]string, 0, 5)
	if channel.RequiresAPIKey && !channel.HasAPIKey {
		facts = append(facts, "缺 Key")
	}
	if channel.IsDefault {
		facts = append(facts, "当前默认")
	}
	facts = append(facts, channelProtocolLabel(channel), fmt.Sprintf("%d 模型", channel.ModelCount))
	if channel.SlotCount > 0 {
		facts = append(facts, fmt.Sprintf("在用 %d 角色", channel.SlotCount))
	}
	for len(facts) > 1 && lipgloss.Width(strings.Join(facts, " · ")) > width {
		facts = facts[:len(facts)-1]
	}
	return truncate(strings.Join(facts, " · "), width)
}

func channelProtocolLabel(channel host.ProviderSnapshot) string {
	protocol := channel.Protocol
	if protocol == "" {
		protocol = channel.Type
	}
	if channel.API != "" {
		return protocol + "/" + channel.API
	}
	return protocol
}

// renderChannelPlan 把“按下 Enter 之后每个角色变成什么”整张摆出来。
// 这是渠道面板的核心：整体切换是个大动作，不能盲按。
func renderChannelPlan(state *modelConfigState, contentW int) []string {
	channel := state.currentChannel()
	if channel == nil {
		return nil
	}
	if state.planErr != "" {
		return []string{lipgloss.NewStyle().Foreground(colorError).
			Render(truncate("无法切到 "+channel.Name+"："+state.planErr, contentW))}
	}
	if len(state.plan) == 0 {
		return nil
	}
	modelW := channelPlanModelWidth(contentW)
	lines := []string{lipgloss.NewStyle().Foreground(colorMuted).
		Render(fmt.Sprintf("切到 %s 后：", channel.Name))}
	for _, plan := range state.plan {
		toStyle := lipgloss.NewStyle().Width(modelW).Foreground(bodyTextColor)
		if plan.Changed() {
			toStyle = toStyle.Foreground(colorAccent)
		}
		lines = append(lines, "  "+
			lipgloss.NewStyle().Width(channelSlotCellWidth).Foreground(colorMuted).Render(channelSlotLabel(plan.Slot))+
			lipgloss.NewStyle().Width(modelW).Foreground(colorDim).Render(truncate(plan.FromModel, modelW-1))+
			lipgloss.NewStyle().Foreground(colorDim).Render(" → ")+
			toStyle.Render(truncate(plan.ToModel, modelW-1))+
			lipgloss.NewStyle().Foreground(colorDim).Render(host.ChannelSlotSourceLabel(plan.Source)))
	}
	return lines
}

func channelSlotLabel(slot string) string {
	for _, option := range modelRoleOptions {
		if option.Key == slot {
			return option.Label
		}
	}
	return slot
}

// channelPlanModelWidth 按内容宽度反算去向表里两列模型名各能占多宽。
// 行的构成：2 缩进 + 角色 + 原模型 + 3 箭头 + 新模型 + 依据说明。
func channelPlanModelWidth(contentW int) int {
	w := (contentW - 2 - channelSlotCellWidth - 3 - channelSourceCellWidth) / 2
	if w < 10 {
		w = 10
	}
	if w > 24 {
		w = 24
	}
	return w
}

// focusDefaultChannel 把光标放到当前默认档所在的渠道上：打开面板最想看的是“我现在在哪家”。
func (s *modelConfigState) focusDefaultChannel() {
	for i, choice := range s.providerChoices {
		if choice.existing != nil && choice.existing.IsDefault {
			s.cursor = i
			return
		}
	}
}

// focusChannel 把光标落到指定渠道上；找不到（比如刚新增还没保存）就留在原地。
func (s *modelConfigState) focusChannel(name string) {
	if name == "" {
		return
	}
	for i, choice := range s.providerChoices {
		if choice.existing != nil && choice.existing.Name == name {
			s.cursor = i
			return
		}
	}
}

// handleChannelKey 处理渠道列表这一步的按键：↑↓ 选渠道并重算预演，E 进定义编辑，
// Enter 整体切换（停在“新增”行时 Enter 是新增）。返回 true 表示已整体切换、面板该关了。
//
// E 编辑、Enter 切换的分工来自使用频率：编辑渠道定义是低频精修，整体切换是某家中转
// 挂掉时的应急动作，应该落在最顺手的键上。
func (s *modelConfigState) handleChannelKey(msg tea.KeyMsg, rt channelSwitcher) bool {
	before := s.cursor
	moveConfigCursor(s, msg, len(s.providerChoices))
	if s.cursor != before {
		s.message = ""
		s.refreshChannelPlan(rt)
	}
	if s.cursor < 0 || s.cursor >= len(s.providerChoices) {
		return false
	}
	choice := s.providerChoices[s.cursor]

	if !choice.add && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 &&
		(msg.Runes[0] == 'e' || msg.Runes[0] == 'E') {
		s.applyProviderChoice(choice)
		return false
	}
	if msg.Type != tea.KeyEnter {
		return false
	}
	if choice.add {
		s.step = configStepAddPicker
		s.cursor = 0
		s.message = ""
		return false
	}
	// 预演失败的渠道切不过去：错误在光标停上去时就显示了，按下也必须被拦住。
	if s.planErr != "" {
		s.message = s.planErr
		return false
	}
	if err := rt.SwitchChannel(choice.label); err != nil {
		s.message = err.Error()
		return false
	}
	return true
}
