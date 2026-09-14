package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

type fakeModelRuntime struct {
	providers   []string
	models      map[string][]host.ConfiguredModel
	curProvider string
	curModel    string
	thinking    map[string]string // role -> 存储的原始意图
	available   []agentcore.ThinkingLevel
	setCalls    []struct{ role, level string }
	switchCalls int
	fallbacks   map[string][]bootstrap.ModelRef
	fbCalls     []struct {
		role string
		refs []bootstrap.ModelRef
	}
	fbErr error
}

func (f *fakeModelRuntime) ConfiguredProviders() []string { return f.providers }
func (f *fakeModelRuntime) ConfiguredModelOptions(provider string) []host.ConfiguredModel {
	return f.models[provider]
}
func (f *fakeModelRuntime) CurrentModelSelection(role string) (string, string, bool) {
	return f.curProvider, f.curModel, true
}
func (f *fakeModelRuntime) AvailableThinking(role string) []agentcore.ThinkingLevel {
	return f.available
}
func (f *fakeModelRuntime) CurrentThinking(role string) string { return f.thinking[role] }
func (f *fakeModelRuntime) SwitchModel(role, provider, model string) error {
	f.switchCalls++
	f.curProvider, f.curModel = provider, model
	return nil
}
func (f *fakeModelRuntime) SetRoleThinking(role, level string) error {
	f.setCalls = append(f.setCalls, struct{ role, level string }{role, level})
	if f.thinking == nil {
		f.thinking = map[string]string{}
	}
	f.thinking[role] = level
	return nil
}

func (f *fakeModelRuntime) RoleFallbacks(role string) []bootstrap.ModelRef {
	return append([]bootstrap.ModelRef(nil), f.fallbacks[role]...)
}

func (f *fakeModelRuntime) SetRoleFallbacks(role string, refs []bootstrap.ModelRef) error {
	if f.fbErr != nil {
		return f.fbErr
	}
	f.fbCalls = append(f.fbCalls, struct {
		role string
		refs []bootstrap.ModelRef
	}{role, append([]bootstrap.ModelRef(nil), refs...)})
	if f.fallbacks == nil {
		f.fallbacks = map[string][]bootstrap.ModelRef{}
	}
	f.fallbacks[role] = append([]bootstrap.ModelRef(nil), refs...)
	return nil
}

// 存储的强度意图高于当前模型能力、面板无法呈现时，用户不动强度字段直接应用，
// 不应把意图误抹成初始默认值。
func TestModelSwitchKeepsUnrepresentableThinkingIntent(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "chat-only"}}},
		curProvider: "proxy", curModel: "chat-only",
		thinking:  map[string]string{"writer": "high"},
		available: nil, // 当前模型只有“继承”一档
	}
	st := newModelSwitchState(rt, "writer")
	if st.thinkingKey() != "" {
		t.Fatalf("high 无法呈现时面板应落在继承档，得到 %q", st.thinkingKey())
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 0 {
		t.Fatalf("未改动强度不应回写：%+v", rt.setCalls)
	}
	if rt.thinking["writer"] != "high" {
		t.Fatalf("意图被抹成 %q，应保留 high", rt.thinking["writer"])
	}
}

// 用户在面板里显式改动强度，则应回写为新值。
func TestModelSwitchAppliesExplicitThinkingChange(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "m"}}},
		curProvider: "proxy", curModel: "m",
		thinking:  map[string]string{"writer": ""},
		available: []agentcore.ThinkingLevel{"low", "high"},
	}
	st := newModelSwitchState(rt, "writer")
	st.focus = modelFocusThinking
	st.cycle(1, rt) // 移动强度字段
	want := st.thinkingKey()
	if want == "" {
		t.Fatal("测试前置：应已移动到某个非空强度档")
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 1 || rt.setCalls[0].level != want {
		t.Fatalf("显式改动应回写 %q，得到 %+v", want, rt.setCalls)
	}
}

func fallbackTestRuntime() *fakeModelRuntime {
	return &fakeModelRuntime{
		providers: []string{"anthropic", "openrouter"},
		models: map[string][]host.ConfiguredModel{
			"openrouter": {{Name: "gemini-pro"}, {Name: "gemini-flash"}},
			"anthropic":  {{Name: "claude-sonnet"}, {Name: "claude-haiku"}},
		},
		curProvider: "openrouter", curModel: "gemini-pro",
	}
}

// 在子编辑器里新增一行、改渠道和模型，应用后按面板顺序回写整条备用链。
func TestModelFallbackEditorAddsChain(t *testing.T) {
	rt := fallbackTestRuntime()
	st := newModelSwitchState(rt, "writer")
	st.fallbackEditing = true

	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyEnter}, rt) // ＋ 添加
	if len(st.fallbacks) != 1 {
		t.Fatalf("添加后应有 1 条，得到 %d", len(st.fallbacks))
	}
	// 新行默认避开主模型 openrouter/gemini-pro
	if st.fallbacks[0] == (bootstrap.ModelRef{Provider: "openrouter", Model: "gemini-pro"}) {
		t.Fatal("新增的备用渠道不应等于主模型")
	}
	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyDown}, rt)  // 回到 ＋ 行
	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyEnter}, rt) // 再加一条
	if len(st.fallbacks) != 2 {
		t.Fatalf("应有 2 条，得到 %d", len(st.fallbacks))
	}
	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyTab}, rt)   // 切到模型列
	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyRight}, rt) // 换模型
	want := append([]bootstrap.ModelRef(nil), st.fallbacks...)

	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyEsc}, rt)
	if st.fallbackEditing {
		t.Fatal("Esc 应退出子编辑器")
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.fbCalls) != 1 || rt.fbCalls[0].role != "writer" {
		t.Fatalf("应回写一次 writer 的备用链，得到 %+v", rt.fbCalls)
	}
	if !sameFallbacks(rt.fbCalls[0].refs, want) {
		t.Fatalf("回写内容 %+v，期望 %+v", rt.fbCalls[0].refs, want)
	}
}

// 换渠道时模型名必须跟着落到新渠道的模型上，不能把旧渠道的模型名带过去。
func TestModelFallbackProviderSwitchResetsModel(t *testing.T) {
	rt := fallbackTestRuntime()
	st := newModelSwitchState(rt, "writer")
	st.fallbacks = []bootstrap.ModelRef{{Provider: "openrouter", Model: "gemini-flash"}}
	st.fallbackEditing = true
	st.fbCursor, st.fbColumn = 0, 0

	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyLeft}, rt)
	got := st.fallbacks[0]
	if got.Provider != "anthropic" || got.Model != "claude-sonnet" {
		t.Fatalf("换渠道后应落到 anthropic/claude-sonnet，得到 %s/%s", got.Provider, got.Model)
	}
}

// 备用链是有序策略：Shift+↑ 调序、Del 删除都必须改变回写内容。
func TestModelFallbackReorderAndDelete(t *testing.T) {
	rt := fallbackTestRuntime()
	st := newModelSwitchState(rt, "writer")
	st.fallbacks = []bootstrap.ModelRef{
		{Provider: "anthropic", Model: "claude-sonnet"},
		{Provider: "anthropic", Model: "claude-haiku"},
		{Provider: "openrouter", Model: "gemini-flash"},
	}
	st.fallbackEditing = true
	st.fbCursor = 2

	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyShiftUp}, rt)
	if st.fallbacks[1].Model != "gemini-flash" || st.fbCursor != 1 {
		t.Fatalf("调序后链 = %+v，光标 = %d", st.fallbacks, st.fbCursor)
	}
	st.handleFallbackKey(tea.KeyMsg{Type: tea.KeyDelete}, rt)
	if len(st.fallbacks) != 2 || st.fallbacks[1].Model != "claude-haiku" {
		t.Fatalf("删除后链 = %+v", st.fallbacks)
	}
}

// 草稿没动过就不该回写：一次回写会重建整套模型客户端并落盘。
func TestModelFallbackUnchangedSkipsWrite(t *testing.T) {
	rt := fallbackTestRuntime()
	rt.fallbacks = map[string][]bootstrap.ModelRef{
		"writer": {{Provider: "anthropic", Model: "claude-sonnet"}},
	}
	st := newModelSwitchState(rt, "writer")
	if len(st.fallbacks) != 1 {
		t.Fatalf("打开面板应读到已存的备用链，得到 %+v", st.fallbacks)
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.fbCalls) != 0 {
		t.Fatalf("未改动不应回写：%+v", rt.fbCalls)
	}
}

// 默认档跑的是共享默认模型，没有角色级备用链可挂——面板不提供编辑。
func TestModelFallbackNotOfferedForDefaultRole(t *testing.T) {
	rt := fallbackTestRuntime()
	st := newModelSwitchState(rt, "default")
	if st.supportsFallbacks() {
		t.Fatal("默认档不应支持备用渠道")
	}
	if st.fallbackSummary() != "不适用（默认档）" {
		t.Fatalf("默认档摘要 = %q", st.fallbackSummary())
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.fbCalls) != 0 {
		t.Fatalf("默认档不应回写备用链：%+v", rt.fbCalls)
	}
}

// 面板是定宽盒子：任何一行超出内宽都会把右边框挤歪（备用链的渠道/模型名可以很长）。
func TestModelSwitchBarStaysRectangular(t *testing.T) {
	rt := fallbackTestRuntime()
	rt.models["openrouter"] = append(rt.models["openrouter"],
		host.ConfiguredModel{Name: "some-vendor/a-very-long-model-name-that-overflows:latest"})
	rt.providers = append(rt.providers, "a-very-long-provider-key-name")
	rt.models["a-very-long-provider-key-name"] = []host.ConfiguredModel{
		{Name: "another-extremely-long-model-identifier-v3"},
	}
	st := newModelSwitchState(rt, "writer")
	st.fallbacks = []bootstrap.ModelRef{
		{Provider: "a-very-long-provider-key-name", Model: "another-extremely-long-model-identifier-v3"},
		{Provider: "openrouter", Model: "some-vendor/a-very-long-model-name-that-overflows:latest"},
	}
	// 消息按终端宽度截断，但盒子最宽只有 68：要靠 renderCommandBox 的兜底截断
	// 才不会把右边框顶出去，所以这里刻意放一条远超盒子内宽的消息。
	st.message = strings.Repeat("这条错误消息长到足以顶穿盒子", 6)

	for _, width := range []int{60, 80, 120} {
		for _, editing := range []bool{false, true} {
			st.fallbackEditing = editing
			lines := strings.Split(renderModelSwitchBar(width, st), "\n")
			want := lipgloss.Width(lines[0])
			if want > width {
				t.Fatalf("width=%d editing=%v: 盒子 %d 列超出终端", width, editing, want)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != want {
					t.Fatalf("width=%d editing=%v: 第 %d 行宽 %d，盒子宽 %d：%q",
						width, editing, i, got, want, line)
				}
			}
		}
	}
}
