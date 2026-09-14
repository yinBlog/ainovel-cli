package bootstrap

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func typeText(m setupFormModel, s string) setupFormModel {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(setupFormModel)
	}
	return m
}

func press(m setupFormModel, t tea.KeyType) setupFormModel {
	next, _ := m.Update(tea.KeyMsg{Type: t})
	return next.(setupFormModel)
}

func TestSetupForm_DefaultsAndValidationOrder(t *testing.T) {
	m := newSetupFormModel()
	if m.provider().name != "openrouter" || m.baseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("defaults: %+v", m)
	}
	// 直接 Enter：缺 API Key，焦点跳到 API Key 并给出原因。
	m = press(m, tea.KeyEnter)
	if m.saved || m.focus != fieldAPIKey || !strings.Contains(m.errText, "API Key") {
		t.Fatalf("expected api key error, got %+v", m)
	}
	m = typeText(m, "sk-or-v1-abcdefgh")
	m = press(m, tea.KeyEnter)
	if m.saved || m.focus != fieldModel {
		t.Fatalf("expected model error next, got focus=%d err=%q", m.focus, m.errText)
	}
	m = typeText(m, "google/gemini-2.5-flash")
	m = press(m, tea.KeyEnter)
	if !m.saved {
		t.Fatalf("form should save, err=%q", m.errText)
	}
	cfg := m.buildConfig()
	pc := cfg.Providers["openrouter"]
	if cfg.Provider != "openrouter" || cfg.ModelName != "google/gemini-2.5-flash" ||
		pc.APIKey != "sk-or-v1-abcdefgh" || pc.BaseURL != "https://openrouter.ai/api/v1" || len(pc.Models) != 1 {
		t.Fatalf("config wrong: %+v", cfg)
	}
}

func TestSetupForm_ProviderCycleKeepsUserBaseURL(t *testing.T) {
	m := newSetupFormModel()
	// 预设 URL 随 provider 切换。
	m = press(m, tea.KeyRight) // anthropic（无预设 URL）
	if m.provider().name != "anthropic" || m.baseURL != "" {
		t.Fatalf("switch should clear preset url: %+v", m)
	}
	m = press(m, tea.KeyLeft) // 回到 openrouter
	if m.baseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("switch back should restore preset: %q", m.baseURL)
	}
	// 用户改过的 URL 不被切换覆盖。
	m = press(m, tea.KeyTab) // API Key
	m = press(m, tea.KeyTab) // Base URL
	if m.focus != fieldBaseURL {
		t.Fatalf("focus = %d", m.focus)
	}
	for range []rune(m.baseURL) {
		m = press(m, tea.KeyBackspace)
	}
	m = typeText(m, "proxy.example.com") // 用户自填、且缺 scheme
	m = press(m, tea.KeyUp)
	m = press(m, tea.KeyUp) // Provider
	m = press(m, tea.KeyRight)
	if m.baseURL != "proxy.example.com" {
		t.Fatalf("user-edited url must survive provider switch: %q", m.baseURL)
	}
	// 补上 API Key 后，非法 URL 被拦（校验顺序：名称 → Key → URL → 模型）。
	m = press(m, tea.KeyTab) // API Key
	m = typeText(m, "sk-ant-abcdefghijk")
	m = press(m, tea.KeyEnter)
	if m.saved || m.focus != fieldBaseURL {
		t.Fatalf("bad url should block save at Base URL, got focus=%d err=%q", m.focus, m.errText)
	}
}

func TestSetupForm_CustomProxyFields(t *testing.T) {
	m := newSetupFormModel()
	for m.provider().name != "custom" {
		m = press(m, tea.KeyRight)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"名称", "协议", "OpenAI 兼容", "Custom Proxy"} {
		if !strings.Contains(view, want) {
			t.Fatalf("custom view missing %q:\n%s", want, view)
		}
	}
	m = press(m, tea.KeyTab) // 名称
	if m.focus != fieldCustomName {
		t.Fatalf("focus = %d", m.focus)
	}
	m = press(m, tea.KeyTab) // 协议
	m = press(m, tea.KeyRight)
	if apiTypeOptions[m.typeIdx].name != "anthropic" {
		t.Fatalf("type cycle wrong: %d", m.typeIdx)
	}
	// 自定义代理：API Key 可空，但 Base URL 必填。
	m = press(m, tea.KeyTab) // API Key
	m = press(m, tea.KeyTab) // Base URL
	m = press(m, tea.KeyTab) // Model
	m = typeText(m, "gpt-4o")
	m = press(m, tea.KeyEnter)
	if m.saved || m.focus != fieldBaseURL {
		t.Fatalf("custom proxy without base url should be blocked, got focus=%d err=%q", m.focus, m.errText)
	}
	m = typeText(m, "https://proxy.example.com/v1")
	m = press(m, tea.KeyEnter)
	if !m.saved {
		t.Fatalf("should save: %q", m.errText)
	}
	cfg := m.buildConfig()
	pc, ok := cfg.Providers["my-proxy"]
	if !ok || cfg.Provider != "my-proxy" || pc.Type != "anthropic" || pc.APIKey != "" || pc.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("custom config wrong: %+v", cfg)
	}
	// 非自定义 provider 时隐藏字段不可聚焦。
	m = newSetupFormModel()
	m = press(m, tea.KeyTab)
	if m.focus != fieldAPIKey {
		t.Fatalf("Tab from provider should skip hidden custom fields, got %d", m.focus)
	}
}

func TestSetupForm_ViewMasksKeyWhenUnfocused(t *testing.T) {
	m := newSetupFormModel()
	m = press(m, tea.KeyTab)
	m = typeText(m, "sk-or-v1-abcdefghijk")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "sk-or-v1-abcdefghijk") {
		t.Fatalf("focused key should be visible while typing:\n%s", v)
	}
	m = press(m, tea.KeyTab)
	v := ansi.Strip(m.View())
	if strings.Contains(v, "sk-or-v1-abcdefghijk") || !strings.Contains(v, "sk-o****hijk") {
		t.Fatalf("unfocused key should be masked:\n%s", v)
	}
	m = press(m, tea.KeyEsc)
	if !m.cancelled {
		t.Fatal("Esc should cancel")
	}
}
