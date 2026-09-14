package bootstrap

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/errs"
)

// 整体切渠道。第三方中转很多、每家的模型名各不相同，所以“换渠道”不是换一个字段，
// 而是把所有角色槽位一起搬到新渠道上，并各自落到该渠道真实存在的模型。
//
// 落哪个模型按三级优先：该渠道上次用过的（preset）→ 同名模型 → 该渠道第一个模型。
// 三级都是**提示性**的：preset 里的模型可能已被 /config 删掉，同名也可能不存在，
// 所以计划永远只从该渠道当前真实的候选模型里挑，挑完原样摆给用户看再确认。

// slotOrder 是角色槽位的展示与搬运顺序（默认档永远排第一，不在这个表里）。
var slotOrder = []string{
	"architect", "writer", "editor",
	"import_segment", "import_analyze", "import_synthesize",
}

// ChannelSlotSource 说明某个槽位的新模型是怎么定下来的，用于向用户解释这次搬运。
type ChannelSlotSource string

const (
	ChannelSlotPreset   ChannelSlotSource = "preset"    // 该渠道上次用的
	ChannelSlotSameName ChannelSlotSource = "same_name" // 同名模型
	ChannelSlotFirst    ChannelSlotSource = "first"     // 该渠道第一个模型
)

// ChannelSlotPlan 是一个角色槽位在整体切渠道后的去向。
type ChannelSlotPlan struct {
	Slot         string
	FromProvider string
	FromModel    string
	ToModel      string
	Source       ChannelSlotSource
}

// Changed 报告这次搬运是否真的改变了该槽位的选择。
func (p ChannelSlotPlan) Changed() bool {
	return p.FromProvider != "" && p.FromModel != p.ToModel
}

// ActiveSlots 返回当前有显式选择的槽位：默认档 + roles 里配过的角色。
// 没配过的角色跟着默认档走，不需要单独搬运。
func (c Config) ActiveSlots() []string {
	slots := make([]string, 0, len(c.Roles)+1)
	slots = append(slots, DefaultSlot)
	for _, role := range slotOrder {
		if _, ok := c.Roles[role]; ok {
			slots = append(slots, role)
		}
	}
	return slots
}

// SlotSelection 返回某个槽位当前的 provider/model。
func (c Config) SlotSelection(slot string) (provider, model string) {
	if slot == DefaultSlot {
		return c.Provider, c.ModelName
	}
	rc := c.Roles[slot]
	return rc.Provider, rc.Model
}

// PlanChannelSwitch 计算把所有槽位整体搬到 provider 后各自落在哪个模型上。
// 纯函数，不改配置——TUI 用它在用户确认前把整张去向表摆出来。
func (c Config) PlanChannelSwitch(provider string) ([]ChannelSlotPlan, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, fmt.Errorf("渠道名不能为空: %w", errs.ErrConfig)
	}
	if _, ok := c.Providers[provider]; !ok {
		return nil, fmt.Errorf("渠道 %q 未配置: %w", provider, errs.ErrConfig)
	}
	pc := c.Providers[provider]
	// 缺凭证的渠道切过去必然在建客户端时失败，预演阶段就报出来——
	// 面板的价值就是让用户在按 Enter 之前看见结果。
	if pc.RequiresAPIKey(provider) && pc.APIKey == "" {
		return nil, fmt.Errorf("渠道 %q 还没配 API Key: %w", provider, errs.ErrConfig)
	}
	candidates := c.CandidateModels(provider)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("渠道 %q 还没有模型，请先用 /channel 添加: %w", provider, errs.ErrConfig)
	}
	available := make(map[string]bool, len(candidates))
	for _, name := range candidates {
		available[name] = true
	}

	preset := c.ChannelPresets[provider]
	plans := make([]ChannelSlotPlan, 0, len(c.Roles)+1)
	for _, slot := range c.ActiveSlots() {
		fromProvider, fromModel := c.SlotSelection(slot)
		plan := ChannelSlotPlan{Slot: slot, FromProvider: fromProvider, FromModel: fromModel}
		switch remembered := preset[slot]; {
		case remembered != "" && available[remembered]:
			plan.ToModel, plan.Source = remembered, ChannelSlotPreset
		case available[fromModel]:
			plan.ToModel, plan.Source = fromModel, ChannelSlotSameName
		default:
			plan.ToModel, plan.Source = candidates[0], ChannelSlotFirst
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// RememberChannelSelection 把当前各槽位的选择记进它所属渠道的 preset。
// 在任何改变选择的操作**之前**调用，切回该渠道时才能还原成这次的样子。
func (c *Config) RememberChannelSelection() {
	for _, slot := range c.ActiveSlots() {
		provider, model := c.SlotSelection(slot)
		if provider == "" || model == "" {
			continue
		}
		if c.ChannelPresets == nil {
			c.ChannelPresets = make(map[string]ChannelPreset)
		}
		if c.ChannelPresets[provider] == nil {
			c.ChannelPresets[provider] = make(ChannelPreset)
		}
		c.ChannelPresets[provider][slot] = model
	}
}

// ApplyChannelSwitch 按计划把各槽位搬到 provider 上，并把结果记成该渠道的新 preset。
// 只动 provider/model：备用渠道链、推理强度都是正交的用户意图，原样保留。
func (c *Config) ApplyChannelSwitch(provider string, plans []ChannelSlotPlan) {
	for _, plan := range plans {
		if plan.Slot == DefaultSlot {
			c.Provider, c.ModelName = provider, plan.ToModel
			continue
		}
		rc := c.Roles[plan.Slot]
		rc.Provider, rc.Model = provider, plan.ToModel
		c.Roles[plan.Slot] = rc
	}
	c.RememberChannelSelection()
}
