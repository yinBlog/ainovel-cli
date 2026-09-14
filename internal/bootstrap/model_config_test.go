package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestModelConfigAcceptsLegacyAndObjectEntries(t *testing.T) {
	var cfg Config
	input := `{
  "provider":"custom","model":"legacy-model",
  "providers":{"custom":{"type":"openai","models":[
    "legacy-model",
    {"name":"large-model","context_window":400000}
  ]}}
}`
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	models := cfg.Providers["custom"].Models
	if len(models) != 2 || models[0].Name != "legacy-model" || models[0].ContextWindow != 0 {
		t.Fatalf("legacy model decode = %#v", models)
	}
	if models[1].Name != "large-model" || models[1].ContextWindow != 400000 {
		t.Fatalf("object model decode = %#v", models[1])
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"models":["legacy-model"`) {
		t.Fatalf("models should be normalized to objects: %s", data)
	}
	if !strings.Contains(string(data), `"name":"legacy-model"`) {
		t.Fatalf("normalized model missing: %s", data)
	}
}

// json_schema 三态：未配置=nil（按 adapter 能力）、true/false=显式声明；
// legacy 字符串条目读入为 nil；写回再读取不得改变三态。
func TestModelConfigJSONSchemaTriState(t *testing.T) {
	var cfg Config
	input := `{"providers":{"custom":{"models":[
    {"name":"a","json_schema":true},
    {"name":"b","json_schema":false},
    {"name":"c"},
    "legacy"
  ]}}}`
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertTriState := func(models []ModelConfig, stage string) {
		t.Helper()
		if models[0].JSONSchema == nil || !*models[0].JSONSchema {
			t.Fatalf("%s: a 应为 true, got %v", stage, models[0].JSONSchema)
		}
		if models[1].JSONSchema == nil || *models[1].JSONSchema {
			t.Fatalf("%s: b 应为 false, got %v", stage, models[1].JSONSchema)
		}
		if models[2].JSONSchema != nil || models[3].JSONSchema != nil {
			t.Fatalf("%s: c/legacy 应为 nil, got %v %v", stage, models[2].JSONSchema, models[3].JSONSchema)
		}
	}
	assertTriState(cfg.Providers["custom"].Models, "decode")

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Config
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	assertTriState(again.Providers["custom"].Models, "round-trip")

	if v := cfg.ModelJSONSchema("custom", "a"); v == nil || !*v {
		t.Fatalf("ModelJSONSchema(custom,a) = %v", v)
	}
	if v := cfg.ModelJSONSchema("custom", "missing"); v != nil {
		t.Fatalf("未列入模型应为 nil, got %v", v)
	}
	if v := cfg.ModelJSONSchema("nope", "a"); v != nil {
		t.Fatalf("未知 provider 应为 nil, got %v", v)
	}
}

// SwappableModel 的 json_schema 覆盖值必须随热切换原子更新：
// 切到声明不同的模型后，下一次 JSONSchemaOverride 现读即得新事实。
func TestSwappableModelJSONSchemaOverrideFollowsSwap(t *testing.T) {
	tr, fa := true, false
	cfg := Config{
		Provider: "proxy", ModelName: "a",
		Providers: map[string]ProviderConfig{"proxy": {
			Type: "openai", APIKey: "k", BaseURL: "https://example.com/v1",
			Models: []ModelConfig{{Name: "a", JSONSchema: &tr}, {Name: "b", JSONSchema: &fa}, {Name: "c"}},
		}},
	}
	ms, err := NewModelSet(cfg)
	if err != nil {
		t.Fatalf("new model set: %v", err)
	}
	if v := ms.Default.JSONSchemaOverride(); v == nil || !*v {
		t.Fatalf("初始应为 true, got %v", v)
	}
	facts := ms.Default.StructuredOutputFacts()
	if facts.Info.Name != "a" || facts.Info.Provider != "openai" || facts.JSONSchemaOverride == nil || !*facts.JSONSchemaOverride {
		t.Fatalf("初始结构化事实快照不一致: %+v", facts)
	}
	if err := ms.Swap("default", "proxy", "b"); err != nil {
		t.Fatalf("swap b: %v", err)
	}
	if v := ms.Default.JSONSchemaOverride(); v == nil || *v {
		t.Fatalf("切到 b 后应为 false, got %v", v)
	}
	facts = ms.Default.StructuredOutputFacts()
	if facts.Info.Name != "b" || facts.JSONSchemaOverride == nil || *facts.JSONSchemaOverride {
		t.Fatalf("切换后结构化事实快照不一致: %+v", facts)
	}
	if err := ms.Swap("default", "proxy", "c"); err != nil {
		t.Fatalf("swap c: %v", err)
	}
	if v := ms.Default.JSONSchemaOverride(); v != nil {
		t.Fatalf("切到未声明的 c 后应为 nil, got %v", v)
	}
}

func TestResolveContextWindowIsProviderAware(t *testing.T) {
	cfg := Config{
		ContextWindow: 300000,
		Providers: map[string]ProviderConfig{
			"one": {Models: []ModelConfig{{Name: "same", ContextWindow: 128000}}},
			"two": {Models: []ModelConfig{{Name: "same", ContextWindow: 900000}}},
		},
	}
	if got, source := cfg.ResolveContextWindow("one", "same"); got != 128000 || source != CtxWindowModelConfig {
		t.Fatalf("one/same = %d %s", got, source)
	}
	if got, source := cfg.ResolveContextWindow("two", "same"); got != 900000 || source != CtxWindowModelConfig {
		t.Fatalf("two/same = %d %s", got, source)
	}
	if got, source := cfg.ResolveContextWindow("one", "unknown"); got != 300000 || source != CtxWindowConfig {
		t.Fatalf("legacy fallback = %d %s", got, source)
	}
}

func TestSaveProviderConfigPreservesSelectionAndUsesPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ainovel", "config.json")
	original := Config{
		Provider: "old", ModelName: "old-model", Style: "fantasy",
		Providers: map[string]ProviderConfig{"old": {Type: "openai", Models: []ModelConfig{{Name: "old-model"}}}},
		Budget:    BudgetConfig{BookUSD: 20, WarnRatio: 0.8},
	}
	if err := SaveConfig(path, original); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pc := ProviderConfig{Type: "openai", Models: []ModelConfig{{Name: "new-model", ContextWindow: 500000}}}
	if err := SaveProviderConfig(path, "new", pc); err != nil {
		t.Fatalf("save provider config: %v", err)
	}
	got, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 只补 providers 段：无关字段与顶层 provider/model 选择必须原样保留。
	if got.Style != "fantasy" || got.Budget.BookUSD != 20 || got.Provider != "old" || got.ModelName != "old-model" {
		t.Fatalf("selection or unrelated fields mutated: %#v", got)
	}
	if _, ok := got.Providers["old"]; !ok {
		t.Fatal("existing provider was removed")
	}
	if got.Providers["new"].Models[0].ContextWindow != 500000 {
		t.Fatalf("new provider not patched in: %#v", got.Providers["new"])
	}
	// 权限断言只在有 POSIX 权限位语义的平台上有意义：Windows 把一切上报为
	// 0666/0444，此断言在该平台恒假（参见 version.TestReplaceExecutable 同款处理）。
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
		}
	}
}

// 运行时给一个原本没有角色覆盖的角色加主模型 + 备用渠道，已经装配好的 Worker
// 必须立刻跟上：ForRoleWithFailover 返回的包装层每次请求现读 ModelSet，
// 不缓存启动那一刻的选择。
func TestForRoleWithFailoverPicksUpRuntimeChanges(t *testing.T) {
	pc := ProviderConfig{
		Type: "openai", APIKey: "k", BaseURL: "https://example.com/v1",
		Models: []ModelConfig{{Name: "m-default"}, {Name: "m-alt"}},
	}
	backup := ProviderConfig{
		Type: "openai", APIKey: "k", BaseURL: "https://backup.example.com/v1",
		Models: []ModelConfig{{Name: "m-backup"}},
	}
	cfg := Config{
		Provider: "main", ModelName: "m-default",
		Providers: map[string]ProviderConfig{"main": pc, "backup": backup},
	}
	ms, err := NewModelSet(cfg)
	if err != nil {
		t.Fatalf("new model set: %v", err)
	}

	// 装配时 writer 还没有角色覆盖，跑的是默认模型。
	model := ms.ForRoleWithFailover("writer", nil)
	if got := ModelName(model); got != "m-default" {
		t.Fatalf("初始应跑默认模型，得到 %q", got)
	}
	if len(ms.fallbackTargets("writer")) != 0 {
		t.Fatal("初始不应有备用渠道")
	}

	candidate := CloneConfig(cfg)
	candidate.Roles = map[string]RoleConfig{"writer": {
		Provider: "main", Model: "m-alt",
		Fallbacks: []ModelRef{{Provider: "backup", Model: "m-backup"}},
	}}
	prepared, err := NewModelSet(candidate)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	ms.ApplyPrepared(prepared)

	// 同一个 model 变量（Worker 手里那份）必须看到新主模型和新备用链。
	if got := ModelName(model); got != "m-alt" {
		t.Fatalf("热应用后主模型应为 m-alt，得到 %q", got)
	}
	// ProviderName 报的是配置里的渠道 key（Info().Provider 是协议名，另一回事）。
	namer, ok := model.(interface{ ProviderName() string })
	if !ok || namer.ProviderName() != "main" {
		t.Fatalf("热应用后渠道 = %v", model)
	}
	targets := ms.fallbackTargets("writer")
	if len(targets) != 1 || targets[0].provider != "backup" || targets[0].name != "m-backup" {
		t.Fatalf("备用链 = %+v", targets)
	}
}

// slotOrder 决定整体切渠道时搬运哪些角色。它和 knownRoles 是两张手写的表，
// 漏掉一个角色不会报错——那个角色会被留在旧渠道上，安静地跟其他角色分家。
func TestSlotOrderCoversEveryKnownRole(t *testing.T) {
	inOrder := make(map[string]bool, len(slotOrder))
	for _, role := range slotOrder {
		if !knownRoles[role] {
			t.Fatalf("slotOrder 里的 %q 不是已知角色", role)
		}
		if inOrder[role] {
			t.Fatalf("slotOrder 里 %q 重复", role)
		}
		inOrder[role] = true
	}
	for role := range knownRoles {
		if !inOrder[role] {
			t.Fatalf("新角色 %q 没进 slotOrder：整体切渠道会把它落在旧渠道上", role)
		}
	}
	if slotOrder[0] == DefaultSlot {
		t.Fatal("默认档不在 slotOrder 里，它由 ActiveSlots 单独排在最前")
	}
}

// ActiveSlots 只搬有显式选择的槽位：没配过的角色跟着默认档走，不需要单独搬运。
func TestActiveSlotsCoversDefaultAndConfiguredRoles(t *testing.T) {
	cfg := Config{
		Provider: "p", ModelName: "m",
		Roles: map[string]RoleConfig{
			"writer":    {Provider: "p", Model: "mw"},
			"architect": {Provider: "p", Model: "ma"},
		},
	}
	slots := cfg.ActiveSlots()
	if len(slots) != 3 || slots[0] != DefaultSlot {
		t.Fatalf("默认档应排第一且只搬配过的角色：%v", slots)
	}
	// 顺序跟着 slotOrder 走，保证去向表每次显示一致。
	if slots[1] != "architect" || slots[2] != "writer" {
		t.Fatalf("槽位顺序应稳定：%v", slots)
	}
	if provider, model := cfg.SlotSelection(DefaultSlot); provider != "p" || model != "m" {
		t.Fatalf("默认档选择 = %s/%s", provider, model)
	}
	if provider, model := cfg.SlotSelection("writer"); provider != "p" || model != "mw" {
		t.Fatalf("writer 选择 = %s/%s", provider, model)
	}
}
