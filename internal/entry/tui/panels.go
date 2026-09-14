package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/library"
)

// renderTopBar 渲染顶部状态栏。
// 左侧：版本 · 进度（章数 / 字数）· 风格，中间：书名，右侧：状态胶囊。
// provider / model 不再重复出现在这里——底部状态栏常驻展示模型身份与用量。
func renderTopBar(snap host.UISnapshot, width int, spinnerFrame, version string) string {
	bookTitle := snap.BookTitle
	if bookTitle == "" {
		bookTitle = "未定书名"
	}

	var infoParts []string
	if version != "" {
		infoParts = append(infoParts, "ainovel-cli "+version)
	}
	if snap.CompletedCount > 0 || snap.TotalWordCount > 0 {
		progress := fmt.Sprintf("%d 章", snap.CompletedCount)
		if !snap.Layered && snap.TotalChapters > 0 {
			progress = fmt.Sprintf("%d/%d 章", snap.CompletedCount, snap.TotalChapters)
		}
		if snap.TotalWordCount > 0 {
			progress += " · " + formatNumber(snap.TotalWordCount) + " 字"
		}
		infoParts = append(infoParts, progress)
	}
	if snap.Layered && snap.CurrentVolumeArc != "" {
		infoParts = append(infoParts, snap.CurrentVolumeArc)
	}
	if snap.Style != "" && snap.Style != "default" {
		infoParts = append(infoParts, snap.Style)
	}
	leftText := strings.Join(infoParts, " · ")

	label := snap.StatusLabel
	if label == "" {
		label = "READY"
	}
	color, ok := statusColors[label]
	if !ok {
		color = colorDim
	}
	disp, ok := statusDisplay[label]
	if !ok {
		disp = struct {
			icon  string
			label string
		}{"○", strings.ToLower(label)}
	}
	icon := disp.icon
	if snap.IsRunning && spinnerFrame != "" {
		icon = spinnerFrame
	}
	statusText := disp.label
	if icon != "" {
		statusText = icon + " " + disp.label
	}
	status := statusPillStyle.Background(color).Render(statusText)

	innerW := max(12, width-2)
	titleText := truncate(bookTitle, max(8, innerW/3))
	centerW := max(16, lipgloss.Width(titleText)+6)
	if centerW > innerW-24 {
		centerW = max(8, innerW-24)
	}
	sideTotal := innerW - centerW
	if sideTotal < 0 {
		sideTotal = 0
		centerW = innerW
	}
	leftW := sideTotal / 2
	rightW := innerW - centerW - leftW

	leftCell := lipgloss.NewStyle().
		Width(leftW).
		AlignHorizontal(lipgloss.Left).
		Foreground(colorDim).
		Render(truncate(leftText, leftW))
	centerCell := lipgloss.NewStyle().
		Width(centerW).
		AlignHorizontal(lipgloss.Center).
		Bold(true).
		Foreground(bodyTextColor).
		Render(titleText)
	rightCell := lipgloss.NewStyle().
		Width(rightW).
		AlignHorizontal(lipgloss.Right).
		Render(status)

	content := leftCell + centerCell + rightCell
	if meta := renderTopBarMeta(snap, innerW); meta != "" {
		content += "\n" + meta
	}
	return topBarStyle.Width(width).
		Border(baseBorder, false, false, true, false).
		BorderForeground(colorDim).
		Render(content)
}

// renderTopBarMeta 是工作台的第二层上下文：把用户下一步最关心的阶段、流程、
// 当前章节和正在工作的 agent 集中到顶栏，避免必须先扫三栏才能拼出当前状态。
func renderTopBarMeta(snap host.UISnapshot, width int) string {
	var left []string
	if phase := snapshotPhaseLabel(snap.Phase); phase != "-" {
		left = append(left, "阶段 "+phase)
	}
	if flow := snapshotFlowLabel(snap.Flow); flow != "-" && flow != snapshotPhaseLabel(snap.Phase) {
		left = append(left, "流程 "+flow)
	}
	if snap.InProgressChapter > 0 {
		left = append(left, fmt.Sprintf("第 %d 章进行中", snap.InProgressChapter))
	} else if snap.CurrentChapter > 0 {
		left = append(left, fmt.Sprintf("下一章 第 %d 章", snap.CurrentChapter))
	}
	if snap.LastCheckpointName != "" {
		left = append(left, "检查点 "+snap.LastCheckpointName)
	}
	if len(left) == 0 && len(snap.Agents) == 0 {
		return ""
	}

	dim := lipgloss.NewStyle().Foreground(colorDim)
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	leftText := dim.Render(strings.Join(left, " · "))
	rightText := ""
	if agent, ok := currentAgent(snap.Agents); ok {
		parts := []string{"当前 " + sidebarAgentName(agent.Name)}
		task := agentTaskLine(agent)
		if task != "" {
			parts = append(parts, task)
		}
		if agent.Tool != "" && agent.Tool != task {
			parts = append(parts, agent.Tool)
		}
		if agent.Context.ContextWindow > 0 && agent.Context.Tokens > 0 {
			parts = append(parts, fmt.Sprintf("ctx %.0f%%", agent.Context.Percent))
		}
		rightText = muted.Render(strings.Join(parts, " · "))
	}
	return joinInlineSides(leftText, rightText, width)
}

func currentAgent(agents []host.AgentSnapshot) (host.AgentSnapshot, bool) {
	for _, agent := range agents {
		if agent.State == "running" {
			return agent, true
		}
	}
	for _, agent := range agents {
		if agent.State == "failed" {
			return agent, true
		}
	}
	return host.AgentSnapshot{}, false
}

// renderStatePanel 把状态侧栏内容(已在 stateVP 中)包进左侧带右边框的盒子。
// 与 renderDetailPanel 对称：内容由 renderStateContent 生成并喂进 viewport，这里只负责框。
// MaxHeight 钳高，防止窗口缩小时溢出比右栏高（见 panels_test.go 的高度契约）。
func renderStatePanel(vp viewport.Model, width, height int, focused bool) string {
	borderColor := colorDim
	if focused {
		borderColor = colorAccent
	}
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(baseBorder, false, true, false, false).
		BorderForeground(borderColor).
		Padding(0, 1)
	return style.Render(vp.View())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderDetailPanel 渲染右侧可滚动详情面板。
func renderDetailPanel(vp viewport.Model, width, height int, focused bool) string {
	borderColor := colorDim
	if focused {
		borderColor = colorAccent
	}
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(baseBorder, false, false, false, true).
		BorderForeground(borderColor).
		Padding(0, 1)

	return style.Render(vp.View())
}

// renderWelcome 渲染新建态首屏。紧凑布局：标题行 → 2×2 能力格 → 最近的书 → 模式与示例 → 入口提示。
// recent 为空时不渲染书架块；currentDir 用来给"当前目录的书"打标。
func renderWelcome(width, height int, errMsg string, mode startupMode, importHint, updateHint string, recent []library.Book, currentDir string) string {
	accent := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	accent2 := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	dim := lipgloss.NewStyle().Foreground(colorDim)
	body := lipgloss.NewStyle().Foreground(bodyTextColor)

	blockW := min(max(60, width-8), 96)
	divider := dim.Render(strings.Repeat("─", blockW))

	var b strings.Builder
	b.WriteString(accent.Render("A I N O V E L"))
	b.WriteString(dim.Render("  ·  "))
	b.WriteString(muted.Italic(true).Render("AI-Powered Novel Creation Engine"))
	b.WriteString("\n")
	b.WriteString(divider)
	b.WriteString("\n")

	// 2×2 能力格：每格 label + 简述，两格一行。
	features := [][2]string{
		{"多模型协作", "Architect 规划 / Writer 创作 / Editor 审阅"},
		{"断点恢复", "崩溃或中断后从上次进度续写"},
		{"实时干预", "创作中随时调整剧情走向"},
		{"分层长篇", "卷-弧-章滚动规划，500+ 章"},
	}
	cellW := blockW / 2
	for i := 0; i < len(features); i += 2 {
		left := renderFeatureCell(features[i], cellW, body, dim)
		right := ""
		if i+1 < len(features) {
			right = renderFeatureCell(features[i+1], cellW, body, dim)
		}
		b.WriteString(lipgloss.NewStyle().Width(cellW).Render(left))
		b.WriteString(right)
		b.WriteString("\n")
	}
	b.WriteString(divider)
	b.WriteString("\n")

	// 最近的书：来自书架登记，最多 5 本；当前目录的书打标。
	if shelf := renderRecentBooks(recent, currentDir, blockW); shelf != "" {
		b.WriteString(shelf)
		b.WriteString(divider)
		b.WriteString("\n")
	}

	b.WriteString(muted.Render("模式 "))
	b.WriteString(accent2.Render(mode.label()))
	b.WriteString(dim.Render(" · " + mode.subtitle() + " · Tab 切换"))
	b.WriteString("\n")
	examples := []string{
		"写一部 12 章都市悬疑小说，主角是一名女法医",
		"创作一部仙侠长篇，主角从凡人修炼至飞升",
		"写一个科幻短篇，讲述 AI 觉醒后的伦理困境",
	}
	for _, ex := range examples {
		b.WriteString(dim.Render("  › "))
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Render(ex))
		b.WriteString("\n")
	}

	if importHint != "" {
		// 这本书停在导入半路：显著提示恢复入口，替代常规导入提示。
		b.WriteString(accent2.Render("! " + importHint))
	} else {
		b.WriteString(dim.Render("/start <文件> 从设定起书 · /import <文件> 导入续写 · /books 书架 · /help 全部命令"))
	}
	if updateHint != "" {
		b.WriteString("\n")
		b.WriteString(accent2.Render("! " + updateHint))
	}
	if errMsg != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Bold(true).Render("! " + errMsg))
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Render(b.String())
}

func renderFeatureCell(f [2]string, cellW int, label, desc lipgloss.Style) string {
	head := label.Render(f[0]) + "  "
	return head + desc.Render(truncate(f[1], max(4, cellW-lipgloss.Width(f[0])-3)))
}

// renderRecentBooks 渲染欢迎页书架块：一行一本，书名 · 状态 · 章数，右侧目录。
func renderRecentBooks(recent []library.Book, currentDir string, width int) string {
	var rows []library.Book
	for _, bk := range recent {
		if bk.Missing || bk.Err != nil || bk.Info == nil {
			continue
		}
		rows = append(rows, bk)
		if len(rows) == 5 {
			break
		}
	}
	if len(rows) == 0 {
		return ""
	}
	muted := lipgloss.NewStyle().Foreground(colorMuted)
	dim := lipgloss.NewStyle().Foreground(colorDim)
	name := lipgloss.NewStyle().Foreground(bodyTextColor).Bold(true)
	var b strings.Builder
	b.WriteString(muted.Render("最近的书"))
	b.WriteString(dim.Render("  在对应目录启动即可继续 · /books 查看全部"))
	b.WriteString("\n")
	for _, bk := range rows {
		title := bk.Info.Title
		if title == "" {
			title = "（未命名）"
		}
		left := "  " + name.Render(truncate(title, 16)) +
			dim.Render(fmt.Sprintf("  %s · %d 章", libraryPhaseLabel(bk.Info), bk.Info.Completed))
		if bk.Dir == currentDir {
			left += lipgloss.NewStyle().Foreground(colorSuccess).Render("  ← 当前目录")
		}
		right := dim.Render(truncate(library.DisplayPath(bk.Dir), max(12, width/2)))
		b.WriteString(joinInlineSides(left, right, width))
		b.WriteString("\n")
	}
	return b.String()
}
