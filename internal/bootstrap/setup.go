package bootstrap

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/rules"
)

// exampleConfig 是引导后写入 ~/.ainovel/config.example.jsonc 的带注释模板。
// 嵌入文件必须与仓库根目录 config.example.jsonc 保持一致，测试会防止漂移。
//
//go:embed config.example.jsonc
var exampleConfig string

// NeedsSetup 检查是否需要首次引导（全局与项目级配置都不存在时触发）。
func NeedsSetup() bool {
	if p := DefaultConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	if _, err := os.Stat(projectConfigPathIn("")); err == nil {
		return false
	}
	return true
}

type setupProvider struct {
	name           string
	label          string
	baseURL        string // 预填的 base_url
	needType       bool   // 自定义代理需要额外问 type 和 base_url
	apiKeyOptional bool   // true 表示 API Key 允许留空
}

// ProviderPreset 是首次引导和运行时 /config 共用的 provider 目录项。
type ProviderPreset struct {
	Name           string
	Label          string
	BaseURL        string
	NeedType       bool
	APIKeyOptional bool
}

var setupProviders = []setupProvider{
	{name: "openrouter", label: "OpenRouter", baseURL: "https://openrouter.ai/api/v1"},
	{name: "anthropic", label: "Anthropic"},
	{name: "gemini", label: "Gemini"},
	{name: "openai", label: "OpenAI"},
	{name: "deepseek", label: "DeepSeek"},
	{name: "qwen", label: "Qwen"},
	{name: "glm", label: "GLM"},
	{name: "grok", label: "Grok"},
	{name: "ollama", label: "Ollama", baseURL: "http://localhost:11434/v1", apiKeyOptional: true},
	{name: "bedrock", label: "Bedrock", apiKeyOptional: true},
	{name: "custom", label: "Custom Proxy", needType: true, apiKeyOptional: true},
}

// ProviderPresets 返回一份可安全修改的预设列表。
func ProviderPresets() []ProviderPreset {
	out := make([]ProviderPreset, 0, len(setupProviders))
	for _, preset := range setupProviders {
		out = append(out, ProviderPreset{
			Name: preset.name, Label: preset.label, BaseURL: preset.baseURL,
			NeedType: preset.needType, APIKeyOptional: preset.apiKeyOptional,
		})
	}
	return out
}

// RunSetup 运行首次引导（单屏表单），保存并返回生成的配置。
func RunSetup() (Config, error) {
	cfg, err := runSetupForm()
	if err != nil {
		return Config{}, err
	}

	path := DefaultConfigPath()
	if err := SaveConfig(path, cfg); err != nil {
		return cfg, fmt.Errorf("save config: %w", err)
	}
	saveExampleConfig()

	// 全局偏好目录由启动流程（runWithConfig）统一创建，这里仅取路径用于提示
	rulesDir := rules.DefaultHomeRulesDir()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s 配置已保存到 %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓"), path)
	fmt.Fprintf(os.Stderr, "  默认模型：%s · 按角色配置不同模型请编辑配置文件或在 TUI 里 /model\n", cfg.ModelName)
	if rulesDir != "" {
		fmt.Fprintf(os.Stderr, "  全局写作偏好可放 %s 下的 .md 文件（见其中 README.txt）\n", rulesDir)
	}
	fmt.Fprintln(os.Stderr)
	return cfg, nil
}

func stderrWriter() *os.File { return os.Stderr }

func saveExampleConfig() {
	dir, err := configDir()
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "config.example.jsonc"), []byte(exampleConfig), 0o644)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

var apiTypeOptions = []setupProvider{
	{name: "openai", label: "OpenAI 兼容"},
	{name: "anthropic", label: "Anthropic 兼容"},
	{name: "gemini", label: "Gemini 兼容"},
}

// ---------- 样式 ----------

var (
	setupCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	setupDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	setupHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	setupInputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)
