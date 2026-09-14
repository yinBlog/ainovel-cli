package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/library"
)

// denseSnapshot 构造一份"长篇写到第 13 章、三个角色都有状态、用量与缓存齐全"的快照，
// 用来检查紧凑版式在真实数据密度下的表现。
func denseSnapshot() host.UISnapshot {
	outline := make([]host.OutlineSnapshot, 0, 30)
	for i := 1; i <= 30; i++ {
		outline = append(outline, host.OutlineSnapshot{Chapter: i, Title: "第" + strings.Repeat("章", i%5+2)})
	}
	return host.UISnapshot{
		Provider: "openrouter", BookTitle: "光斑", ModelName: "gemini-2.5-pro", ModelContextWindow: 1_000_000,
		RuntimeState: "running", StatusLabel: "RUNNING", Phase: "writing", Flow: "writing", IsRunning: true,
		CompletedCount: 12, TotalWordCount: 48_200, InProgressChapter: 13, AdvanceMode: "auto",
		Layered: true, CurrentVolumeArc: "v1a2", NextVolumeTitle: "北境", CompassDirection: "少年登顶", CompassScale: "约 300 章",
		Outline:    outline,
		Characters: []string{"阿禾（主角；药铺杂役，辨药入微）", "李掌柜（药铺掌柜）", "影子（反派）"},
		Synopsis:   "边城药铺杂役少年卷入失踪案。",
		Agents: []host.AgentSnapshot{
			{Name: "writer", State: "running", TaskKind: "chapter_write", Tool: "draft_chapter",
				Context: host.AgentContextSnapshot{Tokens: 42_000, ContextWindow: 100_000, Percent: 42}},
			{Name: "editor", State: "idle", Summary: "待命"},
			{Name: "architect_long", State: "idle", Summary: "待命"},
		},
		TotalInputTokens: 1_234_000, TotalOutputTokens: 89_300, TotalCacheReadTokens: 900_000,
		TotalCostUSD: 3.21, TotalSavedUSD: 0.8, BudgetLimitUSD: 5,
		OverallCacheCapable: true, OverallRecentCacheRead: 80, OverallRecentInput: 100, OverallRecentSamples: 10,
		CachePerAgent: []host.AgentCacheStat{
			{Role: "writer", Model: "gemini-2.5-pro", Input: 900_000, Output: 60_000, CacheRead: 700_000, Cost: 2.4, CacheCapable: true, RecentCacheRead: 80, RecentInput: 100, RecentSamples: 10},
			{Role: "editor", Model: "gemini-2.5-pro", Input: 300_000, Output: 20_000, CacheRead: 200_000, Cost: 0.8, CacheCapable: true},
		},
		CachePerModel:   []host.AgentCacheStat{{Model: "google/gemini-2.5-pro", Input: 1_200_000, Output: 80_000, Cost: 3.2}},
		PendingRewrites: []int{7}, RewriteReason: "节奏失速",
		LastCommitSummary: "第 12 章提交：少年入城，掌柜失踪的消息传开。",
	}
}

// TestWorkbenchViewFitsTerminal 守护整屏契约：View 输出行数不超过终端高，任一行不超过终端宽。
func TestWorkbenchViewFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 36}, {160, 48}, {100, 30}} {
		m := NewModel(nil, "v1.0.0")
		m.width, m.height = size[0], size[1]
		m.mode = modeRunning
		m.snapshot = denseSnapshot()
		m.resizeTextarea()
		m.updateViewportSize()
		m.refreshStateViewport()
		m.refreshDetailViewport()
		m.applyEvent(host.Event{Time: time.Now(), Category: "DISPATCH", Summary: "writer（第 13 章）", Agent: "writer"})
		m.refreshEventViewport()

		out := m.View()
		if size[0] == 120 {
			t.Logf("workbench %dx%d:\n%s", m.width, m.height, ansi.Strip(out))
		}
		lines := strings.Split(out, "\n")
		if len(lines) > m.height {
			t.Fatalf("%dx%d: view has %d lines > height %d", m.width, m.height, len(lines), m.height)
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > m.width {
				t.Fatalf("%dx%d: line %d width %d > %d: %q", m.width, m.height, i, w, m.width, ansi.Strip(line))
			}
		}
	}
}

// TestStateContentIsCompact 守护侧栏密度：概览不超过 4 行，运行中的角色两行以内，
// 无卡片竖线，且关键信息（状态/进度/当前/ctx/预算/缓存）都在。
func TestStateContentIsCompact(t *testing.T) {
	out := ansi.Strip(renderStateContent(denseSnapshot(), 34))
	for _, want := range []string{"状态", "运行中 · 写作 · 自动", "进度", "12 章/规划 30 · 48,200 字", "当前", "写作中 第 13 章",
		"WRITER", "章节写作", "ctx 42%", "draft_chapter", "待命", "返工 [7] · 节奏失速", "↑1.2M ↓89.3k", "$3.21/$5.00 64%", "累计 73%", "近10 80%", "读 900.0k",
		"最近提交", "第 12 章提交"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sidebar missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "│") {
		t.Fatalf("sidebar should not draw card borders:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	overviewStart := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "概览") {
			overviewStart = i
			break
		}
	}
	if overviewStart < 0 {
		t.Fatalf("no overview header:\n%s", out)
	}
	n := 0
	for _, l := range lines[overviewStart+1:] {
		if strings.HasPrefix(l, "角色") {
			break
		}
		n++
	}
	if n > 4 {
		t.Fatalf("overview should be <= 4 lines, got %d:\n%s", n, out)
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > 34 {
			t.Fatalf("sidebar line overflows %d: %q", w, l)
		}
	}
	if commit, usage := strings.Index(out, "最近提交"), strings.Index(out, "用量"); commit < 0 || usage < 0 || commit > usage {
		t.Fatalf("最近提交应排在遥测明细之前:\n%s", out)
	}
}

func TestActivityHeadersExposeFocusAndFollowState(t *testing.T) {
	vp := viewport.New(40, 4)
	event := ansi.Strip(renderEventFlowViewport(vp, 40, 5, true, false, 3, 1))
	if !strings.Contains(event, "▌ 事件流") || !strings.Contains(event, "已停留") || !strings.Contains(event, "3 条") {
		t.Fatalf("事件流标题缺少焦点或滚动状态: %q", event)
	}
	stream := ansi.Strip(renderStreamPanel(vp, 40, 5, false, true, true, 2, 0))
	if !strings.Contains(stream, "▍ 实时输出") || !strings.Contains(stream, "生成中") || !strings.Contains(stream, "跟随") || !strings.Contains(stream, "第 2 轮") {
		t.Fatalf("实时输出标题缺少运行或滚动状态: %q", stream)
	}
}

// TestWelcomeShowsRecentBooksCompact 守护欢迎页：最近的书一行一本、当前目录打标、能力格两格一行。
func TestWelcomeShowsRecentBooksCompact(t *testing.T) {
	recent := []library.Book{
		{Info: &library.BookInfo{Title: "光斑", Phase: "writing", Completed: 12}},
		{Info: &library.BookInfo{Title: "凡骨", Phase: "complete", Completed: 235}},
		{Missing: true},
	}
	recent[0].Dir = "/tmp/a/output/novel"
	recent[1].Dir = "/tmp/b/output/novel"
	recent[2].Dir = "/tmp/gone"
	out := ansi.Strip(renderWelcome(120, 40, "", startupModeQuick, "", "", recent, "/tmp/a/output/novel"))
	for _, want := range []string{"A I N O V E L", "多模型协作", "断点恢复", "最近的书", "光斑", "写作中 · 12 章", "← 当前目录", "凡骨", "已完结 · 235 章", "模式 快速开始", "/books 书架"} {
		if !strings.Contains(out, want) {
			t.Fatalf("welcome missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gone") {
		t.Fatalf("missing dirs must not be listed:\n%s", out)
	}
	// 能力格两格一行：多模型协作与断点恢复应在同一行。
	sameLine := false
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "多模型协作") && strings.Contains(l, "断点恢复") {
			sameLine = true
		}
	}
	if !sameLine {
		t.Fatalf("feature grid should be 2 per line:\n%s", out)
	}
	if lines := strings.Split(out, "\n"); len(lines) > 40 {
		t.Fatalf("welcome exceeds height: %d", len(lines))
	}
	// 没有书架数据时不渲染书架块，且不崩。
	if out := ansi.Strip(renderWelcome(120, 40, "", startupModeCoCreate, "", "", nil, "")); strings.Contains(out, "最近的书") {
		t.Fatalf("empty shelf should be hidden:\n%s", out)
	}
}

// TestHelpTextOneLinePerCommand 守护帮助面板：每条命令一行，按分组分节。
func TestHelpTextOneLinePerCommand(t *testing.T) {
	out := ansi.Strip(renderHelpText(100))
	for _, want := range []string{"系统", "分析", "写作", "/read <章节号>", "/export [path]", "/cocreate (/plan)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
	specs := commandSpecs()
	lines := strings.Split(out, "\n")
	cmdLines := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "/") {
			cmdLines++
		}
	}
	if cmdLines != len(specs) {
		t.Fatalf("want %d command lines, got %d:\n%s", len(specs), cmdLines, out)
	}
}
