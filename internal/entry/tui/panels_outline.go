package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// outlineGridThreshold 大纲切换多列的章节阈值。
// short tier 上限 25 章，20 以下单列一屏装得下、且能保留"进行中"徽标；
// 长篇 layered 模式滚动展开后 n 自然会突破 20，平滑切到多列。
const outlineGridThreshold = 12

// outlineCollapseKeep 折叠已完成章节时保留在当前章之前的已完成章数（给回看上下文）。
const outlineCollapseKeep = 2

// renderOutlineSection 按章节数选布局：少则单列（含"进行中"徽标），多则多列网格。
// 已完成章节超过一屏价值时折叠成一行"● 1–N 已完成"，让当前章与后续规划留在首屏。
func renderOutlineSection(snap host.UISnapshot, contentW int) string {
	visible, note := collapseCompletedOutline(snap)
	var b strings.Builder
	if note != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(truncate(note, contentW)))
		b.WriteString("\n")
	}
	view := snap
	view.Outline = visible
	if len(view.Outline) < outlineGridThreshold {
		b.WriteString(renderOutlineList(view, contentW))
	} else {
		b.WriteString(renderOutlineGrid(view, contentW))
	}
	return b.String()
}

// collapseCompletedOutline 把大纲里已完成的前缀折叠掉，只保留当前章前 outlineCollapseKeep 章。
// 条件：大纲超过网格阈值且已完成章数超过保留数，否则原样返回。返回折叠说明（空表示未折叠）。
func collapseCompletedOutline(snap host.UISnapshot) ([]host.OutlineSnapshot, string) {
	if len(snap.Outline) < outlineGridThreshold || snap.CompletedCount <= outlineCollapseKeep+1 {
		return snap.Outline, ""
	}
	cut := 0 // 折叠掉的章节数
	for i, e := range snap.Outline {
		if e.Chapter > snap.CompletedCount-outlineCollapseKeep {
			break
		}
		if e.Chapter <= snap.CompletedCount {
			cut = i + 1
		}
	}
	if cut == 0 {
		return snap.Outline, ""
	}
	first, last := snap.Outline[0].Chapter, snap.Outline[cut-1].Chapter
	note := fmt.Sprintf("● %d–%d 已完成 · 折叠 %d 章", first, last, cut)
	return snap.Outline[cut:], note
}

// renderOutlineList 单列章节列表（短篇用）。每行尾部带"进行中"徽标，垂直阅读节奏更接近目录。
func renderOutlineList(snap host.UISnapshot, contentW int) string {
	var b strings.Builder
	for _, e := range snap.Outline {
		ch := fmt.Sprintf("%2d", e.Chapter)
		var marker, chStyle string
		titleStyle := cardContentStyle
		switch {
		case snap.CompletedCount >= e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
		case snap.InProgressChapter == e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
			chStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		default:
			marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}
		title := truncate(e.Title, contentW-6)
		line := marker + chStyle + " " + titleStyle.Render(title)
		if snap.InProgressChapter == e.Chapter {
			line += lipgloss.NewStyle().Foreground(colorAccent).Italic(true).Render(" 进行中")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineGrid 把大纲章节按"列优先"填充为多列网格，避免宽屏单列大量留白。
// 列数按 contentW 自适应（1-4），列内章节连续递增（"读完一列再读下一列"）。
// 与单列布局的取舍：放弃尾部" 进行中"徽标——多列下徽标会破坏列对齐，
// 且 ▸ 标记 + 金色 + 左侧概览栏的"写作中 第 N 章"已经把进行中信息说清楚。
func renderOutlineGrid(snap host.UISnapshot, contentW int) string {
	n := len(snap.Outline)
	if n == 0 {
		return ""
	}
	chNumW := 2
	titleW := 0
	for _, e := range snap.Outline {
		if w := len(strconv.Itoa(e.Chapter)); w > chNumW {
			chNumW = w
		}
		if w := lipgloss.Width(e.Title); w > titleW {
			titleW = w
		}
	}
	// 标题宽度上限 14（约 7 个汉字）；偶尔出现的长标题截断，避免一两个长标题撑大全体 cell
	if titleW > 14 {
		titleW = 14
	} else if titleW < 4 {
		titleW = 4
	}
	cellW := 3 + chNumW + titleW // marker(1) + 空格(1) + 章号 + 空格(1) + 标题
	gutter := 4
	cols := (contentW + gutter) / (cellW + gutter)
	if cols < 1 {
		cols = 1
	} else if cols > 4 {
		cols = 4
	}
	rows := (n + cols - 1) / cols

	var b strings.Builder
	cellStyle := lipgloss.NewStyle().Width(cellW)
	gutterStr := strings.Repeat(" ", gutter)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			if idx >= n {
				break
			}
			cell := renderOutlineCell(snap.Outline[idx], snap, chNumW, titleW)
			// 后续列还有 cell 时按 cellW 补齐 + gutter；否则当前 cell 是行尾不补
			if c < cols-1 && (c+1)*rows+r < n {
				b.WriteString(cellStyle.Render(cell))
				b.WriteString(gutterStr)
			} else {
				b.WriteString(cell)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineCell 渲染单个章节 cell：完成（绿●）/ 进行中（金▸）/ 未开始（暗○）。
func renderOutlineCell(e host.OutlineSnapshot, snap host.UISnapshot, chNumW, titleW int) string {
	chStr := fmt.Sprintf("%*d", chNumW, e.Chapter)
	title := truncateWidth(e.Title, titleW)
	var marker, chRendered, titleRendered string
	switch {
	case snap.CompletedCount >= e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = cardContentStyle.Render(title)
	case snap.InProgressChapter == e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
		chRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(title)
	default:
		marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorMuted).Render(title)
	}
	return marker + " " + chRendered + " " + titleRendered
}

// truncateWidth 按"视觉宽度"截断（中文字符算 2 列），与 lipgloss.Width 同源。
// 不加省略号，供网格 cell 列对齐和 truncate 共用。
func truncateWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > maxW {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

// renderDetailContent 构建右侧详情面板内容。
// 顺序：当前章 → 大纲 → 角色 → 配角 → 简介 → 前提。
// 紧凑版式：区块标题带标尺线，区块间只留一行空行，长文本按宽度折行不截断。
func renderDetailContent(snap host.UISnapshot, contentW int) string {
	var b strings.Builder
	dim := lipgloss.NewStyle().Foreground(colorDim)
	section := func(title string) {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderRuledHeader(title, contentW))
		b.WriteString("\n")
	}

	// 当前章是用户最常需要确认的上下文：标题之外，把大纲里的核心事件也
	// 固定展示出来，避免用户只能在实时输出中猜这一轮要写什么。
	if current, ok := currentOutlineEntry(snap); ok {
		section(fmt.Sprintf("当前章 · 第 %d 章", current.Chapter))
		title := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		currentTitle := current.Title
		if currentTitle == "" {
			currentTitle = "未命名章节"
		}
		writeWrapped(&b, currentTitle, contentW, title)
		if current.CoreEvent != "" {
			writeWrapped(&b, "核心事件："+current.CoreEvent, contentW, cardContentStyle)
		}
	} else if snap.InProgressChapter > 0 || (snap.CurrentChapter > 0 && snap.Phase == "writing") {
		chapter := snap.InProgressChapter
		if chapter <= 0 {
			chapter = snap.CurrentChapter
		}
		section(fmt.Sprintf("当前章 · 第 %d 章", chapter))
		writeWrapped(&b, "尚未生成章节大纲", contentW, dim.Italic(true))
	}

	// 大纲
	if len(snap.Outline) > 0 {
		header := "大纲"
		if snap.Layered {
			header = "大纲 " + snap.CurrentVolumeArc
		}
		section(header)
		b.WriteString(renderOutlineSection(snap, contentW))
		if snap.Layered {
			compass := dim.Italic(true)
			if snap.NextVolumeTitle != "" {
				writeWrapped(&b, "┄ 下一卷 "+snap.NextVolumeTitle, contentW, compass)
			}
			if snap.CompassDirection != "" {
				direction := "→ 终局 " + snap.CompassDirection
				if snap.CompassScale != "" {
					direction += "（" + snap.CompassScale + "）"
				}
				writeWrapped(&b, direction, contentW, compass)
			}
		}
	}

	// 角色
	if len(snap.Characters) > 0 {
		section(fmt.Sprintf("角色 %d", len(snap.Characters)))
		for _, c := range snap.Characters {
			writeBulletWrapped(&b, c, contentW, cardContentStyle)
		}
	}

	// 配角生态：总数进标题，最近活跃名单内联折行
	if snap.SupportingCount > 0 {
		section(fmt.Sprintf("配角 %d", snap.SupportingCount))
		if len(snap.RecentSupporting) > 0 {
			writeWrapped(&b, "近期 "+strings.Join(snap.RecentSupporting, " · "), contentW, cardContentStyle)
		}
	}

	if snap.Synopsis != "" {
		section("简介")
		writeWrapped(&b, snap.Synopsis, contentW, dim)
	}

	if snap.Premise != "" {
		section("前提")
		writeWrapped(&b, snap.Premise, contentW, dim)
	}

	// 最近提交 / 最近审阅 / 章节摘要是运行时信息，随左栏状态一起展示（renderStateContent），
	// 右栏只放大纲、角色、简介、前提这些相对稳定的设定事实。
	return b.String()
}

func currentOutlineEntry(snap host.UISnapshot) (host.OutlineSnapshot, bool) {
	chapter := snap.InProgressChapter
	if chapter <= 0 {
		chapter = snap.CurrentChapter
	}
	if chapter <= 0 {
		return host.OutlineSnapshot{}, false
	}
	for _, entry := range snap.Outline {
		if entry.Chapter == chapter {
			return entry, true
		}
	}
	return host.OutlineSnapshot{}, false
}

// renderRuledHeader 渲染"标题 ────"式区块头：标题强调色，其后用细线填满剩余宽度。
// 左右两栏共用，替代旧的 ":: 标题" 与带竖线的卡片。
func renderRuledHeader(title string, width int) string {
	lineW := max(0, width-lipgloss.Width(title)-1)
	return panelTitleStyle.Render(title) + " " +
		lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", lineW))
}

// writeWrapped 按视觉宽度折行写入一段文本，每行独立渲染样式。
func writeWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for _, line := range wrapStreamText(text, max(8, contentW)) {
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
}

// writeBulletWrapped 写入一个"· "条目：按视觉宽度折行，续行以两列空格悬挂缩进。
func writeBulletWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for i, line := range wrapStreamText(text, max(8, contentW-2)) {
		prefix := "· "
		if i > 0 {
			prefix = "  "
		}
		b.WriteString(style.Render(prefix + line))
		b.WriteString("\n")
	}
}
