package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

type fakeChannelSwitcher struct {
	plans     map[string][]bootstrap.ChannelSlotPlan
	planErr   map[string]error
	switchErr error
	switched  []string
	planCalls []string
}

func (f *fakeChannelSwitcher) PlanChannelSwitch(provider string) ([]bootstrap.ChannelSlotPlan, error) {
	f.planCalls = append(f.planCalls, provider)
	if err := f.planErr[provider]; err != nil {
		return nil, err
	}
	return f.plans[provider], nil
}

func (f *fakeChannelSwitcher) SwitchChannel(provider string) error {
	if f.switchErr != nil {
		return f.switchErr
	}
	f.switched = append(f.switched, provider)
	return nil
}

func channelTestSwitcher() *fakeChannelSwitcher {
	return &fakeChannelSwitcher{
		plans: map[string][]bootstrap.ChannelSlotPlan{
			"alpha": {{Slot: "default", FromProvider: "beta", FromModel: "m-b", ToModel: "m-a", Source: bootstrap.ChannelSlotFirst}},
			"beta":  {{Slot: "default", FromProvider: "beta", FromModel: "m-b", ToModel: "m-b", Source: bootstrap.ChannelSlotSameName}},
		},
		planErr: map[string]error{"empty": errors.New(`渠道 "empty" 还没有模型`)},
	}
}

// alpha / beta（当前默认）/ empty，外加 buildProviderMenus 自动追加的“新增”行。
func channelTestState() *modelConfigState {
	st := &modelConfigState{
		editModelIdx: -1, step: configStepProvider,
		snapshot: host.ModelConfigurationSnapshot{Providers: []host.ProviderSnapshot{
			{Name: "alpha", Type: "openai", Protocol: "openai", ModelCount: 2, HasAPIKey: true, RequiresAPIKey: true},
			{Name: "beta", Type: "anthropic", Protocol: "anthropic", ModelCount: 1, HasAPIKey: true,
				RequiresAPIKey: true, SlotCount: 2, IsDefault: true},
			{Name: "empty", Type: "openai", Protocol: "openai", RequiresAPIKey: true},
		}},
	}
	st.buildProviderMenus()
	return st
}

// 打开面板光标就落在当前默认档所在的渠道上，并立刻预演该渠道。
func TestChannelListStartsOnCurrentChannel(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	st.focusDefaultChannel()
	st.refreshChannelPlan(rt)
	if got := st.currentChannel(); got == nil || got.Name != "beta" {
		t.Fatalf("光标应落在当前渠道 beta，得到 %+v", got)
	}
	if len(st.plan) != 1 || st.plan[0].ToModel != "m-b" {
		t.Fatalf("打开时就应预演好计划，得到 %+v", st.plan)
	}
}

// 移动光标必须重算预演——去向表是这个面板的全部价值，不能停在上一个渠道上。
func TestChannelPlanFollowsCursor(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	st.focusDefaultChannel()
	st.refreshChannelPlan(rt)
	if switched := st.handleChannelKey(tea.KeyMsg{Type: tea.KeyUp}, rt); switched {
		t.Fatal("移动光标不该触发切换")
	}
	if got := st.currentChannel(); got == nil || got.Name != "alpha" {
		t.Fatalf("光标 = %+v", got)
	}
	if len(st.plan) != 1 || st.plan[0].ToModel != "m-a" {
		t.Fatalf("计划应跟着重算，得到 %+v", st.plan)
	}
	if last := rt.planCalls[len(rt.planCalls)-1]; last != "alpha" {
		t.Fatalf("最后一次预演的是 %q", last)
	}
}

// 预演失败的渠道按 Enter 必须被拦住，且把原因显示出来。
func TestChannelSwitchRefusesUnusableChannel(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	st.cursor = 2 // empty
	st.refreshChannelPlan(rt)
	if st.planErr == "" {
		t.Fatal("空渠道应给出预演错误")
	}
	if switched := st.handleChannelKey(tea.KeyMsg{Type: tea.KeyEnter}, rt); switched {
		t.Fatal("预演失败时不应切换")
	}
	if len(rt.switched) != 0 {
		t.Fatalf("不应发生切换：%+v", rt.switched)
	}
	if !strings.Contains(st.message, "还没有模型") {
		t.Fatalf("应把原因显示出来，得到 %q", st.message)
	}
}

// Enter 整体切换、E 进定义编辑：同一行上的两个动作不能互相串。
func TestChannelEnterSwitchesAndEditOpensHub(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	st.cursor = 0 // alpha
	st.refreshChannelPlan(rt)

	if switched := st.handleChannelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, rt); switched {
		t.Fatal("E 是编辑定义，不该触发切换")
	}
	if st.step != configStepHub || st.provider != "alpha" {
		t.Fatalf("E 应进入 alpha 的定义编辑，得到 step=%v provider=%q", st.step, st.provider)
	}
	if len(rt.switched) != 0 {
		t.Fatalf("E 不应切换渠道：%+v", rt.switched)
	}

	st, rt = channelTestState(), channelTestSwitcher()
	st.cursor = 0
	st.refreshChannelPlan(rt)
	if switched := st.handleChannelKey(tea.KeyMsg{Type: tea.KeyEnter}, rt); !switched {
		t.Fatalf("Enter 应整体切换，message=%q", st.message)
	}
	if len(rt.switched) != 1 || rt.switched[0] != "alpha" {
		t.Fatalf("切换记录 = %+v", rt.switched)
	}
}

// 停在“新增渠道”行时 Enter 是新增，不是切换（那行没有渠道可切）。
func TestChannelAddRowEntersPicker(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	st.cursor = len(st.providerChoices) - 1
	if got := st.currentChannel(); got != nil {
		t.Fatalf("新增行不应有渠道：%+v", got)
	}
	if switched := st.handleChannelKey(tea.KeyMsg{Type: tea.KeyEnter}, rt); switched {
		t.Fatal("新增行不该触发切换")
	}
	if st.step != configStepAddPicker {
		t.Fatalf("应进入新增目录，得到 step=%v", st.step)
	}
	if len(rt.switched) != 0 {
		t.Fatalf("不应发生切换：%+v", rt.switched)
	}
}

// 渠道行要报出协议、模型数、当前占用和缺 Key，这是选渠道时唯一的判断依据。
func TestChannelRowReportsFacts(t *testing.T) {
	channel := host.ProviderSnapshot{
		Name: "beta", Protocol: "anthropic", ModelCount: 1, SlotCount: 2, IsDefault: true,
		RequiresAPIKey: true,
	}
	row := renderChannelRow(channel, true, 96)
	for _, want := range []string{"beta", "anthropic", "1 模型", "当前默认", "在用 2 角色", "缺 Key"} {
		if !strings.Contains(row, want) {
			t.Fatalf("渠道行缺少 %q：%q", want, row)
		}
	}
	// 窄到放不下时丢的必须是最不重要的那条，而不是把“缺 Key”拦腰截掉——
	// 它决定这个渠道能不能用。
	narrow := renderChannelRow(channel, true, 60)
	if !strings.Contains(narrow, "缺 Key") {
		t.Fatalf("窄行丢掉了缺 Key：%q", narrow)
	}
	if strings.Contains(narrow, "在用 2 角色") {
		t.Fatalf("窄行应先丢角色占用：%q", narrow)
	}
}

// 渠道多、名字长时，渠道列表这一步的弹窗仍必须是规整矩形。
func TestChannelStepModalStaysRectangular(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	for i := 0; i < 15; i++ {
		st.snapshot.Providers = append(st.snapshot.Providers, host.ProviderSnapshot{
			Name: strings.Repeat("long-third-party-relay", 2), Protocol: "openai", ModelCount: 9,
		})
	}
	st.providerChoices = nil
	st.presetChoices = nil
	st.buildProviderMenus()
	st.cursor = 0
	rt.plans["alpha"] = append(rt.plans["alpha"], bootstrap.ChannelSlotPlan{
		Slot: "writer", FromProvider: "beta",
		FromModel: "an-extremely-long-model-name-from-some-relay",
		ToModel:   "another-extremely-long-model-name-v3-turbo",
		Source:    bootstrap.ChannelSlotFirst,
	})
	st.refreshChannelPlan(rt)
	st.message = "一条足够长的错误消息用来撑一撑这个弹窗的宽度限制看看会不会破掉"

	for _, width := range []int{70, 100, 160} {
		lines := strings.Split(renderModelConfigModal(width, st), "\n")
		want := lipgloss.Width(lines[0])
		if want > width {
			t.Fatalf("width=%d: 弹窗 %d 列超出终端", width, want)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != want {
				t.Fatalf("width=%d: 第 %d 行宽 %d，弹窗宽 %d：%q", width, i, got, want, line)
			}
		}
	}
}

// 从渠道详情 Esc 回列表：光标要落回刚才编辑的那个渠道，去向表也要跟着它重算。
// 否则列表高亮第一行、预览还停在上一个渠道，两边对不上。
func TestChannelEscapeBackRestoresCursor(t *testing.T) {
	st, rt := channelTestState(), channelTestSwitcher()
	st.cursor = 1 // beta
	st.refreshChannelPlan(rt)
	st.handleChannelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, rt)
	if st.step != configStepHub || st.provider != "beta" {
		t.Fatalf("测试前置：应进入 beta 的详情，得到 step=%v provider=%q", st.step, st.provider)
	}

	st.cursor = 0 // 模拟 Esc 分支里的重置
	st.step = configStepProvider
	st.focusChannel(st.provider)
	st.refreshChannelPlan(rt)

	if got := st.currentChannel(); got == nil || got.Name != "beta" {
		t.Fatalf("光标应落回 beta，得到 %+v", got)
	}
	if len(st.plan) != 1 || st.plan[0].ToModel != "m-b" {
		t.Fatalf("去向表应按 beta 重算，得到 %+v", st.plan)
	}
	if last := rt.planCalls[len(rt.planCalls)-1]; last != "beta" {
		t.Fatalf("最后一次预演的是 %q", last)
	}
}
