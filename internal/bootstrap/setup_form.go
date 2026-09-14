package bootstrap

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// 首次引导的单屏表单：所有字段同屏可见、可来回修改，Enter 一次性校验保存。
// 取代旧的五步逐个问答——问答式每步都是独立程序，答错要 Esc 重来。

type setupField int

const (
	fieldProvider setupField = iota
	fieldCustomName
	fieldCustomType
	fieldAPIKey
	fieldBaseURL
	fieldModel
	fieldCount
)

// setupFormModel 是表单状态。文本字段各自保存原始输入；Provider / 协议类型是枚举游标。
type setupFormModel struct {
	providerIdx int
	typeIdx     int
	customName  string
	apiKey      string
	baseURL     string
	model       string

	focus     setupField
	errText   string
	cancelled bool
	saved     bool
	width     int
}

func newSetupFormModel() setupFormModel {
	m := setupFormModel{customName: "my-proxy", width: 100}
	m.baseURL = m.provider().baseURL
	return m
}

func (m setupFormModel) provider() setupProvider { return setupProviders[m.providerIdx] }

// visible 报告某字段在当前 Provider 下是否显示（自定义代理才有名称 / 协议）。
func (m setupFormModel) visible(f setupField) bool {
	if f == fieldCustomName || f == fieldCustomType {
		return m.provider().needType
	}
	return true
}

func (m setupFormModel) Init() tea.Cmd { return nil }

func (m setupFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m setupFormModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.cancelled = true
		return m, tea.Quit
	case tea.KeyEnter:
		if f, err := m.validate(); err != nil {
			m.errText = err.Error()
			m.focus = f
			return m, nil
		}
		m.saved = true
		return m, tea.Quit
	case tea.KeyTab, tea.KeyDown:
		m.move(1)
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		m.move(-1)
		return m, nil
	case tea.KeyLeft, tea.KeyRight:
		delta := 1
		if msg.Type == tea.KeyLeft {
			delta = -1
		}
		switch m.focus {
		case fieldProvider:
			m.cycleProvider(delta)
		case fieldCustomType:
			m.typeIdx = (m.typeIdx + delta + len(apiTypeOptions)) % len(apiTypeOptions)
		}
		return m, nil
	case tea.KeyBackspace:
		m.editText(func(s string) string {
			r := []rune(s)
			if len(r) == 0 {
				return s
			}
			return string(r[:len(r)-1])
		})
		return m, nil
	case tea.KeySpace:
		m.editText(func(s string) string { return s + " " })
		return m, nil
	case tea.KeyRunes:
		text := utils.CleanInputRunes(msg.Runes)
		// 枚举字段上敲 j/k 也能切换，与旧选择器习惯一致。
		if m.focus == fieldProvider || m.focus == fieldCustomType {
			switch text {
			case "j":
				return m.afterEnumStep(1), nil
			case "k":
				return m.afterEnumStep(-1), nil
			}
			return m, nil
		}
		m.editText(func(s string) string { return s + text })
		return m, nil
	}
	return m, nil
}

func (m setupFormModel) afterEnumStep(delta int) setupFormModel {
	switch m.focus {
	case fieldProvider:
		m.cycleProvider(delta)
	case fieldCustomType:
		m.typeIdx = (m.typeIdx + delta + len(apiTypeOptions)) % len(apiTypeOptions)
	}
	return m
}

// cycleProvider 切换 Provider；Base URL 若仍是上一个预设的默认值就跟着换，用户改过的保留。
func (m *setupFormModel) cycleProvider(delta int) {
	prev := m.provider()
	m.providerIdx = (m.providerIdx + delta + len(setupProviders)) % len(setupProviders)
	if m.baseURL == prev.baseURL {
		m.baseURL = m.provider().baseURL
	}
	m.errText = ""
	if !m.visible(m.focus) {
		m.focus = fieldProvider
	}
}

func (m *setupFormModel) move(delta int) {
	for i := 0; i < int(fieldCount); i++ {
		m.focus = setupField((int(m.focus) + delta + int(fieldCount)) % int(fieldCount))
		if m.visible(m.focus) {
			return
		}
	}
}

func (m *setupFormModel) editText(fn func(string) string) {
	m.errText = ""
	switch m.focus {
	case fieldCustomName:
		m.customName = fn(m.customName)
	case fieldAPIKey:
		m.apiKey = fn(m.apiKey)
	case fieldBaseURL:
		m.baseURL = fn(m.baseURL)
	case fieldModel:
		m.model = fn(m.model)
	}
}

// validate 返回第一个不合法字段及原因；全部合法返回 (fieldProvider, nil)。
func (m setupFormModel) validate() (setupField, error) {
	sp := m.provider()
	if sp.needType && utils.CleanInputLine(m.customName) == "" {
		return fieldCustomName, fmt.Errorf("自定义代理需要一个名称，例如 my-proxy")
	}
	if !sp.apiKeyOptional && utils.CleanInputLine(m.apiKey) == "" {
		return fieldAPIKey, fmt.Errorf("%s 需要 API Key", sp.label)
	}
	if u := utils.CleanInputLine(m.baseURL); u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fieldBaseURL, fmt.Errorf("Base URL 需以 http:// 或 https:// 开头")
	}
	if sp.needType && utils.CleanInputLine(m.baseURL) == "" {
		return fieldBaseURL, fmt.Errorf("自定义代理必须填写 Base URL")
	}
	if utils.CleanInputLine(m.model) == "" {
		return fieldModel, fmt.Errorf("模型名称必填，例如 gpt-4o / claude-sonnet-4 / gemini-2.5-pro")
	}
	return fieldProvider, nil
}

// buildConfig 把表单折成 Config。调用方须先 validate。
func (m setupFormModel) buildConfig() Config {
	sp := m.provider()
	providerName := sp.name
	pc := ProviderConfig{
		APIKey:  utils.CleanInputLine(m.apiKey),
		BaseURL: utils.CleanInputLine(m.baseURL),
	}
	if sp.needType {
		providerName = utils.CleanInputLine(m.customName)
		pc.Type = apiTypeOptions[m.typeIdx].name
	}
	model := utils.CleanInputLine(m.model)
	pc.Models = []ModelConfig{{Name: model}}
	return Config{
		Provider:  providerName,
		ModelName: model,
		Providers: map[string]ProviderConfig{providerName: pc},
		Roles:     map[string]RoleConfig{},
		Style:     "default",
	}
}

// ---------- 渲染 ----------

func (m setupFormModel) View() string {
	label := setupDimStyle
	focusLabel := setupHeaderStyle
	hint := setupDimStyle
	var b strings.Builder

	b.WriteString(setupHeaderStyle.Render("首次运行 · 初始化配置"))
	b.WriteString(hint.Render("  写入 " + DefaultConfigPath() + "，之后可随时编辑或在 TUI 里用 /channel 修改"))
	b.WriteString("\n\n")

	row := func(f setupField, name string, body string, extra string) {
		if !m.visible(f) {
			return
		}
		cursor := "  "
		ls := label
		if f == m.focus {
			cursor = setupCursorStyle.Render("❯ ")
			ls = focusLabel
		}
		b.WriteString(cursor)
		b.WriteString(ls.Width(10).Render(name))
		b.WriteString(body)
		if extra != "" {
			b.WriteString(hint.Render("  " + extra))
		}
		b.WriteString("\n")
	}

	sp := m.provider()
	providerBody := setupInputStyle.Render(sp.label)
	providerExtra := "←→ 切换 · " + sp.name
	if sp.apiKeyOptional && !sp.needType {
		providerExtra += " · API Key 可留空"
	}
	row(fieldProvider, "Provider", providerBody, providerExtra)
	row(fieldCustomName, "名称", m.textBody(fieldCustomName, m.customName, "my-proxy"), "配置里的 provider 键名")
	row(fieldCustomType, "协议", setupInputStyle.Render(apiTypeOptions[m.typeIdx].label), "←→ 切换")

	keyHint := "sk-xxx"
	if sp.apiKeyOptional {
		keyHint = "可留空"
	}
	row(fieldAPIKey, "API Key", m.textBody(fieldAPIKey, m.apiKey, keyHint), "")
	urlHint, urlExtra := "留空使用官方地址", "代理用户填代理地址"
	switch {
	case sp.needType:
		urlHint, urlExtra = "https://proxy.example.com/v1", "必填"
	case sp.baseURL != "":
		urlHint = sp.baseURL
	}
	row(fieldBaseURL, "Base URL", m.textBody(fieldBaseURL, m.baseURL, urlHint), urlExtra)
	row(fieldModel, "模型名称", m.textBody(fieldModel, m.model, "gpt-4o / claude-sonnet-4 / gemini-2.5-pro"), "必填")

	b.WriteString("\n")
	if m.errText != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render("! " + m.errText))
		b.WriteString("\n")
	}
	b.WriteString(hint.Render("Tab/↑↓ 切换字段 · ←→ 切换选项 · Enter 保存 · Esc 取消"))
	b.WriteString("\n")
	return b.String()
}

// textBody 渲染文本字段：聚焦时带光标；空值显示占位。API Key 失焦后打码。
func (m setupFormModel) textBody(f setupField, val, placeholder string) string {
	focused := f == m.focus
	if val == "" {
		if focused {
			return setupCursorStyle.Render("▌") + setupDimStyle.Render(placeholder)
		}
		return setupDimStyle.Render(placeholder)
	}
	shown := val
	if f == fieldAPIKey && !focused {
		shown = maskKey(val)
	}
	if focused {
		return shown + setupCursorStyle.Render("▌")
	}
	return shown
}

// runSetupForm 跑单屏表单，返回配置；取消返回错误。
func runSetupForm() (Config, error) {
	p := tea.NewProgram(newSetupFormModel(), tea.WithOutput(stderrWriter()))
	final, err := p.Run()
	if err != nil {
		return Config{}, err
	}
	result := final.(setupFormModel)
	if result.cancelled || !result.saved {
		return Config{}, fmt.Errorf("setup cancelled")
	}
	return result.buildConfig(), nil
}
