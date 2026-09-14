package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStateContent 生成状态侧栏的纯内容(不含边框/外框)，供 stateVP.SetContent 使用。
// 紧凑版式：区块头"标题 ───"下直接排行、无卡片竖线；概览压成 2~4 行，每个角色不超过 2 行，
// 返工 / 干预 / 停靠合并为一个"待处理"区块。
func renderStateContent(snap host.UISnapshot, contentW int) string {
	contentW = max(12, contentW)
	agents := sidebarAgents(snap.Agents)
	idleAgents := sidebarIdleAgents(snap.Agents)
	dim := lipgloss.NewStyle().Foreground(colorDim)
	var sections []string

	if snap.RecoveryLabel != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(colorMuted).Italic(true).
			Render(truncate(snap.RecoveryLabel, contentW)))
	}

	var overview strings.Builder
	// 状态：运行态 · 阶段/流程 · 推进政策
	phase := snapshotPhaseLabel(snap.Phase)
	state := snapshotRuntimeStateLabel(snap.RuntimeState) + " · " + phase
	if flow := snapshotFlowLabel(snap.Flow); flow != "-" && flow != phase {
		state += "/" + flow
	}
	switch snap.AdvanceMode {
	case "review":
		if snap.AdvancePermitChapter > 0 {
			state += fmt.Sprintf(" · 已放行第 %d 章", snap.AdvancePermitChapter)
		} else {
			state += " · 逐章验收"
		}
	case "auto":
		state += " · 自动"
	}
	overview.WriteString(renderCompactField("状态", state, contentW))
	// 进度：章数 · 字数
	var progress string
	switch {
	case snap.Layered:
		// 分层动态规划只展示当前弧已展开的章节，不泄漏骨架弧的粗估算。
		progress = fmt.Sprintf("%d 章", snap.CompletedCount)
		if planned := len(snap.Outline); planned > 0 {
			progress += fmt.Sprintf("/规划 %d", planned)
		}
	case snap.TotalChapters > 0:
		progress = fmt.Sprintf("%d/%d 章", snap.CompletedCount, snap.TotalChapters)
	default:
		progress = fmt.Sprintf("%d 章", snap.CompletedCount)
	}
	progress += " · " + formatNumber(snap.TotalWordCount) + " 字"
	overview.WriteString(renderCompactField("进度", progress, contentW))
	if label, ch := inProgressDisplay(snap); label != "" {
		overview.WriteString(renderCompactField("当前", fmt.Sprintf("%s 第 %d 章", label, ch), contentW))
	}
	if headline := snapshotHeadline(snap); headline != "" {
		label := "待办"
		if !snap.IsRunning {
			label = "待恢复"
		}
		overview.WriteString(renderCompactHighlight(label, headline, contentW))
	}
	sections = append(sections, renderSidebarSection("概览", overview.String(), contentW))

	if len(agents) > 0 {
		var agentBody strings.Builder
		for _, agent := range agents {
			agentBody.WriteString(renderAgentLine(agent, contentW))
			agentBody.WriteString("\n")
		}
		if len(idleAgents) > 0 {
			for _, l := range wrapAtSeparators("待命 "+strings.Join(idleAgents, " · "), contentW) {
				agentBody.WriteString(dim.Render(l))
				agentBody.WriteString("\n")
			}
		}
		sections = append(sections, renderSidebarSection("角色", agentBody.String(), contentW))
	}

	// 待处理块里全是"我现在需要做什么"：返工原因、用户自己输的干预方向、停靠原因。
	// 截断等于这一块白开，所以折行给全——侧栏本身是可滚动的 viewport，放得下。
	var pending strings.Builder
	if len(snap.PendingRewrites) > 0 {
		line := fmt.Sprintf("返工 %v", snap.PendingRewrites)
		if snap.RewriteReason != "" {
			line += " · " + snap.RewriteReason
		}
		writeIndented(&pending, line, contentW, 2, highlightValueStyle)
	}
	if snap.PendingSteer != "" {
		writeIndented(&pending, "干预 "+snap.PendingSteer, contentW, 2, highlightValueStyle)
	}
	if snap.HasAdvanceHold {
		writeIndented(&pending, "停靠 "+snap.AdvanceHoldReason, contentW, 2, highlightValueStyle)
	}
	if pending.Len() > 0 {
		sections = append(sections, renderSidebarSection("待处理", pending.String(), contentW))
	}

	// 创作结果优先于遥测明细：最近提交/审阅紧跟运行态与待处理事项，
	// 避免在常见高度下被角色用量和缓存统计挤到首屏之外。
	if snap.LastCommitSummary != "" {
		var body strings.Builder
		writeWrapped(&body, snap.LastCommitSummary, contentW, cardContentStyle)
		sections = append(sections, renderSidebarSection("最近提交", body.String(), contentW))
	}
	if snap.LastReviewSummary != "" {
		var body strings.Builder
		writeWrapped(&body, snap.LastReviewSummary, contentW, cardContentStyle)
		sections = append(sections, renderSidebarSection("最近审阅", body.String(), contentW))
	}

	if body := renderUsageSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("用量", body, contentW))
	}
	if body := renderCacheSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("缓存", body, contentW))
	}

	// 多章摘要通常最长，保留在侧栏底部供主动滚动回看。
	if len(snap.RecentSummaries) > 0 {
		var body strings.Builder
		for _, s := range snap.RecentSummaries {
			writeWrapped(&body, s, contentW, cardContentStyle)
		}
		sections = append(sections, renderSidebarSection("摘要", body.String(), contentW))
	}

	return strings.Join(sections, "\n")
}

// renderCompactField 是侧栏紧凑键值行：两字标签 + 值，标签列固定 5 列。
// 值超过 width 时按视觉宽度折行、续行悬挂缩进到值列——窄栏下宁多一行也不截断丢信息。
func renderCompactField(label, value string, width int) string {
	if value == "" {
		value = "-"
	}
	return renderHangingField(compactLabelStyle.Render(label), value, fieldValueStyle, width)
}

func renderCompactHighlight(label, value string, width int) string {
	return renderHangingField(compactLabelStyle.Render(label), value, highlightValueStyle, width)
}

func renderHangingField(labelCell, value string, style lipgloss.Style, width int) string {
	labelW := lipgloss.Width(labelCell)
	var b strings.Builder
	for i, line := range wrapAtSeparators(value, max(6, width-labelW)) {
		if i == 0 {
			b.WriteString(labelCell)
		} else {
			b.WriteString(strings.Repeat(" ", labelW))
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

// renderAgentLine 渲染一个运行角色：首行 状态点 + 角色色名字 + 任务 · ctx；
// 次行为当前工具或摘要，仅在存在且与任务不同的时候出现。
func renderAgentLine(agent host.AgentSnapshot, width int) string {
	stateColor := taskStatusColor(agent.State)
	icon := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateIcon(agent.State))
	name := lipgloss.NewStyle().Bold(true).Foreground(eventAgentColor(agent.Name)).Render(sidebarAgentName(agent.Name))
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	sep := lipgloss.NewStyle().Foreground(colorDim).Render(" · ")
	// ctx 占用放在任务前：它是窄栏下最不该被截掉的健康信号。
	var meta []string
	if agent.State != "running" {
		meta = append(meta, muted.Render(agentStateLabel(agent.State)))
	}
	if ctx := agentContextLine(agent); ctx != "" {
		meta = append(meta, ctx)
	}
	taskLine := agentTaskLine(agent)
	if taskLine != "" {
		meta = append(meta, muted.Render(taskLine))
	}
	head := icon + " " + name
	if len(meta) > 0 {
		head += " " + strings.Join(meta, sep)
	}
	// 与用量 / 缓存行同款：按 " · " 折行，续行缩进，不截断。
	line := strings.TrimRight(wrapStyledInline(head, width), "\n")

	detail := agent.Summary
	if agent.Tool != "" {
		detail = agent.Tool
	}
	if agent.State == "idle" && detail == "待命" {
		detail = ""
	}
	if detail != "" && detail != taskLine {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Render("  "+truncate(detail, max(8, width-2)))
	}
	return line
}

// wrapAtSeparators 把纯文本按 " · " 分段贪心装行：整段放不下就换行，单段仍超宽再按字符硬折。
// 侧栏的"状态 / 进度"值由若干短语用 " · " 拼成，按短语折行比按字符折可读得多。
func wrapAtSeparators(value string, width int) []string {
	if lipgloss.Width(value) <= width {
		return []string{value}
	}
	const sep = " · "
	var lines []string
	cur := ""
	for _, part := range strings.Split(value, sep) {
		candidate := part
		if cur != "" {
			candidate = cur + sep + part
		}
		if cur != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, cur)
			cur = part
			continue
		}
		cur = candidate
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	var out []string
	for _, l := range lines {
		out = append(out, wrapRunes(l, width)...)
	}
	return out
}

// wrapStyledInline 把一条带样式的内联行按 " · " 分段折成多行（续行缩进 2 列），
// 每行以 "\n" 结尾。段与段之间是独立的样式片段，所以按分隔符切开不会破坏 ANSI 序列；
// 单段仍超宽时按截断兜底。
func wrapStyledInline(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line + "\n"
	}
	sep := lipgloss.NewStyle().Foreground(colorDim).Render(" · ")
	parts := strings.Split(line, sep)
	var b strings.Builder
	cur := ""
	flush := func() {
		if cur != "" {
			b.WriteString(fitInlineLine(cur, width))
			b.WriteString("\n")
		}
	}
	for _, p := range parts {
		candidate := p
		if cur != "" {
			candidate = cur + sep + p
		}
		if cur != "" && lipgloss.Width(candidate) > width {
			flush()
			cur = "  " + p
			continue
		}
		cur = candidate
	}
	flush()
	return b.String()
}

// renderSidebarSection 渲染"标题 ───"区块头 + 内容；无卡片竖线与内边距，内容顶格。
func renderSidebarSection(title, body string, width int) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	return renderRuledHeader(title, width) + "\n" + body
}

func sidebarAgents(agents []host.AgentSnapshot) []host.AgentSnapshot {
	var out []host.AgentSnapshot
	for _, agent := range agents {
		if agent.State == "idle" {
			continue
		}
		out = append(out, agent)
	}
	if len(out) == 0 {
		out = append(out, agents...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i], out[j]
		if agentStateRank(li.State) != agentStateRank(lj.State) {
			return agentStateRank(li.State) < agentStateRank(lj.State)
		}
		return agentOrder(li.Name) < agentOrder(lj.Name)
	})
	return out
}

// sidebarAgentName 是侧栏用的短角色名：architect_long / architect_short 都显示 ARCHITECT
// （规划师的长短之分对用户是实现细节），其余与事件流一致的大写名。
func sidebarAgentName(name string) string {
	if strings.HasPrefix(name, "architect") {
		return "ARCHITECT"
	}
	return agentDisplayName(name)
}

func sidebarIdleAgents(agents []host.AgentSnapshot) []string {
	var names []string
	hasActive := false
	for _, agent := range agents {
		if agent.State != "idle" {
			hasActive = true
			continue
		}
		names = append(names, sidebarAgentName(agent.Name))
	}
	if !hasActive {
		return nil
	}
	sort.Strings(names)
	return names
}

// inProgressDisplay 计算"进行中"字段的标签和章节号。
// 根据 flow 选择动词（打磨/重写/写作）；in_progress_chapter 与 flow 不匹配时视为 stale：
//   - polishing/rewriting 模式下章节不在 pending_rewrites 中 → 回退到队列首章
//   - 字段为 0 时不渲染
func inProgressDisplay(snap host.UISnapshot) (label string, chapter int) {
	ch := snap.InProgressChapter
	switch snap.Flow {
	case "polishing":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "打磨中", ch
	case "rewriting":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "重写中", ch
	default:
		if ch <= 0 {
			return "", 0
		}
		return "写作中", ch
	}
}

func snapshotHeadline(snap host.UISnapshot) string {
	if snap.PendingSteer != "" {
		if !snap.IsRunning {
			return "待恢复：处理用户干预"
		}
		return "等待处理用户干预"
	}
	if len(snap.PendingRewrites) > 0 {
		if !snap.IsRunning {
			return "待恢复：返工处理"
		}
		return "等待返工处理"
	}
	if snap.AdvanceMode == "review" && !snap.IsRunning && snap.Phase == "writing" {
		return "逐章验收：等待放行下一章"
	}
	return ""
}

func snapshotPhaseLabel(phase string) string {
	switch phase {
	case "premise":
		return "前提"
	case "outline":
		return "大纲"
	case "writing":
		return "写作"
	case "complete":
		return "完成"
	case "init":
		return "初始化"
	default:
		if phase == "" {
			return "-"
		}
		return phase
	}
}

func snapshotRuntimeStateLabel(state string) string {
	switch state {
	case "running":
		return "运行中"
	case "pausing":
		return "暂停中"
	case "paused":
		return "已暂停"
	case "completed":
		return "已完成"
	default:
		return "空闲"
	}
}

func snapshotFlowLabel(flow string) string {
	switch flow {
	case "":
		return "-"
	case "writing":
		return "写作"
	case "reviewing":
		return "评审"
	case "rewriting":
		return "重写"
	case "polishing":
		return "打磨"
	case "steering":
		return "干预"
	default:
		return flow
	}
}

func renderUsageSidebar(snap host.UISnapshot, width int) string {
	if snap.TotalInputTokens <= 0 && snap.TotalOutputTokens <= 0 && snap.TotalCostUSD <= 0 {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colorDim)
	val := lipgloss.NewStyle().Foreground(bodyTextColor)
	var b strings.Builder
	// 首行合并会话累计：↑输入 ↓输出 · 费用/预算 百分比 · 省
	line := dim.Render("↑") + val.Render(formatTokensCompact(snap.TotalInputTokens)) + " " +
		dim.Render("↓") + val.Render(formatTokensCompact(snap.TotalOutputTokens))
	if cost := formatCostUSD(snap.TotalCostUSD); cost != "" {
		line += dim.Render(" · ") + val.Render(cost)
		if snap.BudgetLimitUSD > 0 {
			pct := snap.TotalCostUSD / snap.BudgetLimitUSD * 100
			line += dim.Render(fmt.Sprintf("/%s %.0f%%", formatCostUSD(snap.BudgetLimitUSD), pct))
		}
	} else if snap.BudgetLimitUSD > 0 {
		line += dim.Render(" · ") + dim.Render("预算 "+formatCostUSD(snap.BudgetLimitUSD))
	}
	if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
		line += dim.Render(" · ") + dim.Render("省"+saved)
	}
	b.WriteString(wrapStyledInline(line, width))

	agentStats := usageStatsByCost(snap.CachePerAgent)
	if len(agentStats) > 0 {
		b.WriteString(renderUsageGroupHeader("角色", width))
		limit := min(len(agentStats), 4)
		for i := 0; i < limit; i++ {
			a := agentStats[i]
			b.WriteString(renderUsageLine(sidebarAgentName(a.Role), eventAgentColor(a.Role), a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	modelStats := usageStatsByCost(snap.CachePerModel)
	if len(modelStats) > 0 {
		b.WriteString(renderUsageGroupHeader("模型", width))
		limit := min(len(modelStats), 3)
		for i := 0; i < limit; i++ {
			a := modelStats[i]
			b.WriteString(renderUsageLine(modelDisplayName(a.Model), bodyTextColor, a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func usageStatsByCost(in []host.AgentCacheStat) []host.AgentCacheStat {
	out := append([]host.AgentCacheStat(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input+out[i].Output > out[j].Input+out[j].Output
	})
	return out
}

func renderUsageGroupHeader(label string, width int) string {
	line := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("·", max(8, width-lipgloss.Width(label)-3)))
	return lipgloss.NewStyle().Foreground(colorMuted).Render(label+" ") + line + "\n"
}

func renderUsageLine(name string, color lipgloss.TerminalColor, input, output int, cost float64, width int) string {
	// 名称列：常规 11 列；窄栏 8 列；宽栏（≥40）把多出来的宽度让给名称（上限 18），
	// 右侧 "1.3M · $3.20" 约需 13 列始终保留。
	nameW := 11
	switch {
	case width < 24:
		nameW = 8
	case width >= 40:
		nameW = min(18, width-14)
	}
	nameCell := lipgloss.NewStyle().Foreground(color).Width(nameW).
		Render(truncate(name, nameW))
	tokens := formatTokensCompact(input + output)
	right := tokens
	if costStr := formatCostUSD(cost); costStr != "" {
		right += " · " + costStr
	}
	// 名称恰好占满固定列宽时，padding 不会留下尾随空格；显式分隔，避免
	// "gpt-5.6-sol5.3k" 这类模型名与用量粘连。
	return fitInlineLine(nameCell+" "+lipgloss.NewStyle().Foreground(colorDim).Render(right), width)
}

func modelDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "unknown"
	}
	parts := strings.Split(model, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[1:], "/")
	}
	if len(parts) == 2 {
		return parts[1]
	}
	return model
}

// renderCacheSidebar 渲染左栏"缓存"区块。
//
// 三种态：
//  1. 完全没消费 token：返回空，section 不渲染
//  2. 当前会话所有 role 都跑的是不支持 prompt cache 的模型：仅渲染一行"未启用"提示
//  3. 已启用：两行汇总（命中率 / 读写量与断裂）+ per-role 行
//
// per-role 行 capable 时显示"累计/近10%"；不 capable 时显示"未启用"。
func renderCacheSidebar(snap host.UISnapshot, width int) string {
	// 上游 streaming 没发 OpenAI 的 final usage chunk —— 累计数据全为 0，
	// 但这不是"没启用 cache"也不是"用量太低被门控藏起来"，必须显式提示。优先级最高。
	if snap.MissingAssistantUsage > 0 && snap.TotalInputTokens <= 0 {
		warn := lipgloss.NewStyle().Foreground(colorError).Bold(true).
			Render(fmt.Sprintf("⚠ 上游未返 usage（%d 次）", snap.MissingAssistantUsage))
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("检查 provider stream_options.include_usage", max(8, width-2)))
		return warn + "\n" + hint + "\n"
	}

	if snap.TotalInputTokens <= 0 && snap.TotalCacheWriteTokens <= 0 {
		return ""
	}

	// 全程未启用 → 显示一行解释，避免用户误判为"0% 命中需要排查"
	if !snap.OverallCacheCapable && snap.TotalCacheReadTokens == 0 && snap.TotalCacheWriteTokens == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("当前模型未启用 prompt cache", max(8, width-2))) + "\n"
	}

	dim := lipgloss.NewStyle().Foreground(colorDim)
	val := lipgloss.NewStyle().Foreground(bodyTextColor)
	var b strings.Builder

	// 第一行：累计命中 · 近 N 命中 · 节省
	overallHit := cacheHitRate(snap.TotalCacheReadTokens, snap.TotalInputTokens)
	line := dim.Render("累计 ") + colorPercent(overallHit)
	if snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
		recent := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
		line += dim.Render(" · ") + dim.Render(fmt.Sprintf("近%d ", snap.OverallRecentSamples)) + colorPercent(recent)
	}
	if savedStr := formatCostUSD(snap.TotalSavedUSD); savedStr != "" {
		line += dim.Render(" · ") + dim.Render("省"+savedStr)
	}
	b.WriteString(wrapStyledInline(line, width))

	// 第二行：读量 · 写量（OpenAI / Gemini 系自动缓存无溢价、不报写量）· 断裂次数
	line = dim.Render("读 ") + val.Render(formatTokensCompact(snap.TotalCacheReadTokens))
	if snap.TotalCacheWriteTokens > 0 {
		line += dim.Render(" · ") + dim.Render("写 ") + val.Render(formatTokensCompact(snap.TotalCacheWriteTokens))
	}
	if snap.TotalCacheBreaks > 0 {
		// 断裂 = 前缀未缩短而命中骤降；次数多通常指向服务端逐出或中转轮询上游，详情看 tui.log。
		line += dim.Render(" · ") + lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf("断裂 %d", snap.TotalCacheBreaks))
	}
	b.WriteString(wrapStyledInline(line, width))

	// Arbiter 按设计不参与 prompt cache（KB 级一次性裁定，无稳定前缀可复用）。
	for _, a := range snap.CachePerAgent {
		if a.Role == "arbiter" {
			continue
		}
		b.WriteString(renderCacheAgentLine(a, width))
		b.WriteString("\n")
	}
	return b.String()
}

// colorPercent 把百分比按命中率分档着色后转字符串，仅用于值列。
func colorPercent(p float64) string {
	return lipgloss.NewStyle().Foreground(cacheHitColor(p)).Bold(true).
		Render(formatPercent(p))
}

// renderCacheAgentLine 渲染单个 role 行：role + 命中率 + 缓存读 / 总输入。
//
// 把分子分母都摆出来（cacheRead / input）让用户一眼就能验算命中率的来源，
// 也能识别"高百分比但小样本"的侥幸数据（比如 100% / 1k 的可信度低于 80% / 300k）。
//
// 百分比优先用滑动窗稳态值；窗内无样本时回落到累计。整个左栏只有这一处用 "/"，
// 语义专一（数学除号：cache 命中量 / 总输入量），不会与其它分隔符混淆。
//
// 三种态：
//
//	未启用     "WRITER        未启用"
//	已启用     "WRITER        85%  · 323k / 394k"
//	无 cache  显式"未启用"，不混进 0/0 干扰判读
func renderCacheAgentLine(a host.AgentCacheStat, width int) string {
	// role 名与"角色"区保持一致；列宽随侧栏宽度取 8~10，窄栏下截断 ARCHITECT_LONG 尾部。
	roleW := 10
	if width < 28 {
		roleW = 8
	}
	roleStyle := lipgloss.NewStyle().Foreground(eventAgentColor(a.Role)).Width(roleW)
	role := roleStyle.Render(truncateWidth(sidebarAgentName(a.Role), roleW-1))

	if !a.CacheCapable {
		dim := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		return role + dim.Render("未启用")
	}

	// 稳态命中率优先；窗内无样本时回落到累计。
	hit := cacheHitRate(a.RecentCacheRead, a.RecentInput)
	if a.RecentSamples == 0 || a.RecentInput == 0 {
		hit = cacheHitRate(a.CacheRead, a.Input)
	}
	// 百分比固定 4 列宽（"100%"），避免读量列在 "5%" 与 "85%" 之间左右跳。
	pctCell := lipgloss.NewStyle().Width(4).Render(colorPercent(hit))

	// 累计读/累计输入 — 分子分母都用累计，"看出规模"是这一列的主诉求；
	// 百分比单独提供稳态信号。整行只有这一处用 "/"（数学除号），无空格以省列。
	tokens := lipgloss.NewStyle().Foreground(colorDim).Render(
		" " + formatTokensCompact(a.CacheRead) + "/" + formatTokensCompact(a.Input))
	return fitInlineLine(role+pctCell+tokens, width)
}

// cacheHitRate 在 input 已含 cacheRead 的语义下直接除得百分比。
// input == 0 时返回 0，避免出现假命中。
func cacheHitRate(cacheRead, input int) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(input) * 100
}

// cacheHitColor 命中率染色：≥50% 绿 / 20–50% 黄 / <20% 红。
// 用与上下文使用率相反的方向：缓存命中率越高越健康。
func cacheHitColor(percent float64) lipgloss.AdaptiveColor {
	switch {
	case percent >= 50:
		return colorSuccess
	case percent >= 20:
		return colorReview
	default:
		return colorError
	}
}

func formatPercent(p float64) string {
	if p <= 0 {
		return "0%"
	}
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// formatTokensCompact 把 token 数渲染成 "8.2k" / "1.4M" 这种紧凑形式。
// 用于狭窄的 per-role 行，避免和 formatNumber 的逗号风格挤出去。
func formatTokensCompact(n int) string {
	if n <= 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func contextScopeLabel(scope string) string {
	switch scope {
	case "baseline":
		return "基线"
	case "projected":
		return "投影"
	case "recovered":
		return "恢复"
	case "committed":
		return "已提交"
	case "skipped":
		return "熔断跳过"
	default:
		return scope
	}
}

func contextStrategyLabel(strategy string) string {
	switch strategy {
	case "":
		return ""
	case "tool_result_microcompact":
		return "工具结果微压缩"
	case "light_trim":
		return "轻裁剪"
	case "full_summary":
		return "完整摘要"
	default:
		return strategy
	}
}

func agentDisplayName(name string) string {
	return strings.ToUpper(name)
}

func agentTaskLine(agent host.AgentSnapshot) string {
	if agent.TaskKind != "" {
		return taskKindLabel(agent.TaskKind)
	}
	if agent.Summary != "" {
		return agent.Summary
	}
	return ""
}

func agentContextLine(agent host.AgentSnapshot) string {
	ctx := agent.Context
	if ctx.ContextWindow <= 0 || ctx.Tokens <= 0 {
		return ""
	}
	percentColor := contextPercentColor(ctx.Percent)
	percentStr := lipgloss.NewStyle().Foreground(percentColor).Render(fmt.Sprintf("ctx %.0f%%", ctx.Percent))
	parts := []string{percentStr}
	if scope := contextScopeLabel(ctx.Scope); scope != "" {
		parts = append(parts, scope)
	}
	if strategy := contextStrategyLabel(ctx.Strategy); strategy != "" {
		parts = append(parts, strategy)
	}
	return strings.Join(parts, " · ")
}

func agentStateRank(state string) int {
	switch state {
	case "running":
		return 0
	case "failed":
		return 1
	default:
		return 2
	}
}

func agentOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "architect"):
		return 0
	case name == "editor":
		return 2
	case name == "writer":
		return 3
	default:
		return 9
	}
}

func agentStateLabel(state string) string {
	switch state {
	case "running":
		return "运行中"
	case "failed":
		return "异常"
	case "idle":
		return "待命"
	default:
		return state
	}
}

func agentStateIcon(state string) string {
	switch state {
	case "running":
		return "●"
	case "failed":
		return "×"
	default:
		return "·"
	}
}

func taskStatusColor(status string) lipgloss.AdaptiveColor {
	switch status {
	case "running":
		return colorSuccess
	case "queued":
		return colorMuted
	case "failed", "canceled":
		return colorError
	case "succeeded":
		return colorSuccess
	default:
		return colorDim
	}
}

func taskKindLabel(kind string) string {
	switch kind {
	case "foundation_plan":
		return "基础规划"
	case "chapter_write":
		return "章节写作"
	case "chapter_review":
		return "章节评审"
	case "chapter_rewrite":
		return "章节重写"
	case "chapter_polish":
		return "章节打磨"
	case "arc_expand":
		return "弧展开"
	case "volume_append":
		return "下一卷规划"
	case "steer_apply":
		return "处理干预"
	default:
		return kind
	}
}
