package host

import (
	"fmt"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// 渠道维度的整体切换：把整本书从一个中转搬到另一个中转。第三方渠道多且经常临时
// 挂掉，逐个角色改一遍既慢又容易漏。渠道的定义（协议/凭证/模型库）和这里的整体切换
// 都归 /channel 面板，/model 只管单个角色的精调。
//
// 搬运只动 provider/model：备用渠道链和推理强度是正交的用户意图，原样保留。

// PlanChannelSwitch 预演整体切换：返回每个角色槽位会落到哪个模型、依据是什么。
// 不改任何状态，供 TUI 在用户确认前摆出整张去向表。
func (h *Host) PlanChannelSwitch(provider string) ([]bootstrap.ChannelSlotPlan, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.PlanChannelSwitch(provider)
}

// SwitchChannel 把所有角色槽位整体切到 provider：记住旧渠道的配法 → 按计划搬运 →
// 构建候选 ModelSet → 落盘 → 热应用。任一步失败都不改运行时状态。
func (h *Host) SwitchChannel(provider string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	provider = strings.TrimSpace(provider)
	plans, err := h.cfg.PlanChannelSwitch(provider)
	if err != nil {
		return err
	}

	candidate := bootstrap.CloneConfig(h.cfg)
	// 先把当前各槽位的选择记进它们各自所属的渠道，切回来时才能还原成现在的样子。
	candidate.RememberChannelSelection()
	candidate.ApplyChannelSwitch(provider, plans)

	if err := candidate.ValidateBase(); err != nil {
		return err
	}
	prepared, err := bootstrap.NewModelSet(candidate)
	if err != nil {
		return fmt.Errorf("切换渠道失败: %w", err)
	}
	if h.configPath == "" {
		return fmt.Errorf("无法定位配置文件路径")
	}
	if err := bootstrap.SaveConfig(h.configPath, candidate); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	h.models.ApplyPrepared(prepared)
	h.cfg = candidate
	// 模型客户端被整体重建：按各角色新模型的能力重新钳制推理强度（存储意图不变）。
	h.applyThinkingLocked(bootstrap.DefaultSlot)

	for _, plan := range plans {
		window, source := h.cfg.ResolveContextWindow(provider, plan.ToModel)
		bootstrap.LogContextWindowChoice(plan.Slot, plan.ToModel, window, source)
	}
	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Level:    "info",
		Summary:  fmt.Sprintf("渠道已切换：%s（%s）", provider, summarizeChannelPlans(plans)),
		Detail:   detailChannelPlans(provider, plans),
	})
	return nil
}

func summarizeChannelPlans(plans []bootstrap.ChannelSlotPlan) string {
	changed := 0
	for _, plan := range plans {
		if plan.Changed() {
			changed++
		}
	}
	if changed == 0 {
		return fmt.Sprintf("%d 个角色，模型未变", len(plans))
	}
	return fmt.Sprintf("%d 个角色，其中 %d 个换了模型", len(plans), changed)
}

func detailChannelPlans(provider string, plans []bootstrap.ChannelSlotPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "整体切换到渠道 %s：\n", provider)
	for _, plan := range plans {
		fmt.Fprintf(&b, "  %-18s %s/%s → %s/%s（%s）\n",
			plan.Slot, plan.FromProvider, plan.FromModel, provider, plan.ToModel,
			ChannelSlotSourceLabel(plan.Source))
	}
	return b.String()
}

// ChannelSlotSourceLabel 把搬运依据翻成一句人话，TUI 和事件详情共用。
func ChannelSlotSourceLabel(source bootstrap.ChannelSlotSource) string {
	switch source {
	case bootstrap.ChannelSlotPreset:
		return "该渠道上次用的"
	case bootstrap.ChannelSlotSameName:
		return "同名模型"
	case bootstrap.ChannelSlotFirst:
		return "该渠道首个模型"
	default:
		return string(source)
	}
}
