package host

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// 两个中转：a 有 m1/m2，b 有 m1/mx。默认档在 a/m1，writer 单独配了 a/m2。
func newChannelTestHost(t *testing.T) (*Host, string) {
	t.Helper()
	cfg := bootstrap.Config{
		Provider: "relay-a", ModelName: "m1",
		Providers: map[string]bootstrap.ProviderConfig{
			"relay-a": {Type: "openai", APIKey: "ka", BaseURL: "https://a.example.com/v1",
				Models: []bootstrap.ModelConfig{{Name: "m1"}, {Name: "m2"}}},
			"relay-b": {Type: "openai", APIKey: "kb", BaseURL: "https://b.example.com/v1",
				Models: []bootstrap.ModelConfig{{Name: "m1"}, {Name: "mx"}}},
			"relay-empty": {Type: "openai", APIKey: "kc", BaseURL: "https://c.example.com/v1"},
		},
		Roles: map[string]bootstrap.RoleConfig{"writer": {
			Provider: "relay-a", Model: "m2", ReasoningEffort: "high",
			Fallbacks: []bootstrap.ModelRef{{Provider: "relay-b", Model: "mx"}},
		}},
	}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatalf("new model set: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := bootstrap.SaveConfig(path, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return &Host{cfg: cfg, models: models, events: make(chan Event, 8), configPath: path}, path
}

// 切渠道是整体搬运：默认档和所有显式角色都要落到新渠道上，各自挑到真实存在的模型。
func TestSwitchChannelMovesEverySlot(t *testing.T) {
	h, path := newChannelTestHost(t)
	if err := h.SwitchChannel("relay-b"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if h.cfg.Provider != "relay-b" || h.cfg.ModelName != "m1" {
		t.Fatalf("默认档 = %s/%s，期望 relay-b/m1（同名）", h.cfg.Provider, h.cfg.ModelName)
	}
	// writer 原来在 a/m2，b 上没有 m2，只能落到 b 的第一个模型。
	writer := h.cfg.Roles["writer"]
	if writer.Provider != "relay-b" || writer.Model != "m1" {
		t.Fatalf("writer = %s/%s，期望 relay-b/m1（首个）", writer.Provider, writer.Model)
	}
	provider, model, _ := h.models.CurrentSelection("writer")
	if provider != "relay-b" || model != "m1" {
		t.Fatalf("运行时 writer = %s/%s", provider, model)
	}
	saved, err := bootstrap.LoadConfigFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if saved.Provider != "relay-b" || saved.Roles["writer"].Provider != "relay-b" {
		t.Fatalf("落盘后 default=%s writer=%s", saved.Provider, saved.Roles["writer"].Provider)
	}
}

// 每个渠道记住自己那套配法：来回切要还原成离开时的样子，而不是每次都重新猜。
func TestSwitchChannelRestoresPerChannelPreset(t *testing.T) {
	h, _ := newChannelTestHost(t)
	if err := h.SwitchChannel("relay-b"); err != nil {
		t.Fatalf("switch b: %v", err)
	}
	// 在 b 上手工把 writer 调成 mx，这次调整应被 b 记住。
	if err := h.SwitchModel("writer", "relay-b", "mx"); err != nil {
		t.Fatalf("tune on b: %v", err)
	}
	if err := h.SwitchChannel("relay-a"); err != nil {
		t.Fatalf("switch back to a: %v", err)
	}
	if got := h.cfg.Roles["writer"]; got.Provider != "relay-a" || got.Model != "m2" {
		t.Fatalf("切回 a 应还原成 a/m2，得到 %s/%s", got.Provider, got.Model)
	}
	if h.cfg.ModelName != "m1" {
		t.Fatalf("切回 a 的默认档应还原成 m1，得到 %q", h.cfg.ModelName)
	}
	if err := h.SwitchChannel("relay-b"); err != nil {
		t.Fatalf("switch b again: %v", err)
	}
	if got := h.cfg.Roles["writer"]; got.Model != "mx" {
		t.Fatalf("再切回 b 应还原成手工调过的 mx，得到 %q", got.Model)
	}
}

// 切渠道只动 provider/model：备用链和推理强度是正交的用户意图，不能被顺手抹掉。
func TestSwitchChannelKeepsFallbacksAndThinking(t *testing.T) {
	h, _ := newChannelTestHost(t)
	if err := h.SwitchChannel("relay-b"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	writer := h.cfg.Roles["writer"]
	if writer.ReasoningEffort != "high" {
		t.Fatalf("推理强度被改成 %q", writer.ReasoningEffort)
	}
	want := bootstrap.ModelRef{Provider: "relay-b", Model: "mx"}
	if len(writer.Fallbacks) != 1 || writer.Fallbacks[0] != want {
		t.Fatalf("备用链被改成 %+v", writer.Fallbacks)
	}
}

// 没有模型的渠道切不过去，且必须在预演阶段就报出来——面板靠它在按下 Enter 前拦住用户。
func TestPlanChannelSwitchRejectsUnusableChannel(t *testing.T) {
	h, _ := newChannelTestHost(t)
	if _, err := h.PlanChannelSwitch("relay-empty"); err == nil || !strings.Contains(err.Error(), "还没有模型") {
		t.Fatalf("空渠道应被拒，得到 %v", err)
	}
	if _, err := h.PlanChannelSwitch("ghost"); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("未知渠道应被拒，得到 %v", err)
	}
	if err := h.SwitchChannel("relay-empty"); err == nil {
		t.Fatal("空渠道不应切换成功")
	}
	if h.cfg.Provider != "relay-a" {
		t.Fatalf("失败后不应改动运行时配置：%q", h.cfg.Provider)
	}
}

// 预演给出的依据要能解释每个槽位的去向，TUI 直接把它显示给用户。
func TestPlanChannelSwitchReportsSource(t *testing.T) {
	h, _ := newChannelTestHost(t)
	plans, err := h.PlanChannelSwitch("relay-b")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	got := make(map[string]bootstrap.ChannelSlotSource, len(plans))
	for _, plan := range plans {
		got[plan.Slot] = plan.Source
	}
	if got[bootstrap.DefaultSlot] != bootstrap.ChannelSlotSameName {
		t.Fatalf("默认档 m1 在 b 上同名存在，依据应是 same_name，得到 %q", got[bootstrap.DefaultSlot])
	}
	if got["writer"] != bootstrap.ChannelSlotFirst {
		t.Fatalf("writer 的 m2 在 b 上不存在，依据应是 first，得到 %q", got["writer"])
	}
}

// 渠道列表要报出每个渠道的协议、模型数和当前占用情况——编辑定义和整体切换看的是同一张表。
func TestChannelsSnapshot(t *testing.T) {
	h, _ := newChannelTestHost(t)
	channels := h.ModelConfiguration().Providers
	byName := make(map[string]ProviderSnapshot, len(channels))
	for _, channel := range channels {
		byName[channel.Name] = channel
	}
	if len(channels) != 3 {
		t.Fatalf("应列出 3 个渠道，得到 %d", len(channels))
	}
	if a := byName["relay-a"]; !a.IsDefault || a.SlotCount != 2 || a.ModelCount != 2 {
		t.Fatalf("relay-a = %+v", a)
	}
	if b := byName["relay-b"]; b.IsDefault || b.SlotCount != 0 || b.ModelCount != 2 {
		t.Fatalf("relay-b = %+v", b)
	}
	if e := byName["relay-empty"]; e.ModelCount != 0 {
		t.Fatalf("relay-empty 应没有模型：%+v", e)
	}
	if got := byName["relay-a"].Protocol; got != "openai" {
		t.Fatalf("协议应解析成 openai，得到 %q", got)
	}
}

// 缺 API Key 的渠道要在预演阶段就被拦下来，而不是等切到一半建客户端时才失败。
// 判定沿用 ValidateBase 那条：没显式声明 type 的（内置 provider）才必须有 Key。
func TestPlanChannelSwitchRejectsChannelWithoutKey(t *testing.T) {
	h, _ := newChannelTestHost(t)
	h.cfg.Providers["openrouter"] = bootstrap.ProviderConfig{
		Models: []bootstrap.ModelConfig{{Name: "some-model"}},
	}

	if _, err := h.PlanChannelSwitch("openrouter"); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("缺 Key 的渠道应在预演阶段被拒，得到 %v", err)
	}
	if err := h.SwitchChannel("openrouter"); err == nil {
		t.Fatal("缺 Key 的渠道不应切换成功")
	}
	if h.cfg.Provider != "relay-a" {
		t.Fatalf("失败后不应改动运行时配置：%q", h.cfg.Provider)
	}
	// 配上 Key 就该放行，证明拦的是凭证而不是这个渠道本身。
	h.cfg.Providers["openrouter"] = bootstrap.ProviderConfig{
		APIKey: "k", Models: []bootstrap.ModelConfig{{Name: "some-model"}},
	}
	if _, err := h.PlanChannelSwitch("openrouter"); err != nil {
		t.Fatalf("配上 Key 后应可预演，得到 %v", err)
	}
}
