package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/library"
	"github.com/voocel/ainovel-cli/internal/rules"
)

// ── /reviews ──

func (m Model) openReviews(args []string) (tea.Model, tea.Cmd) {
	chapter := 0
	if len(args) > 1 {
		return m.libraryError("用法：/reviews [章节号]")
	}
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			return m.libraryError("章节号必须是正整数：" + args[0])
		}
		chapter = n
	}
	info, reviews, err := library.Reviews(m.runtime.Dir(), chapter)
	if err != nil {
		return m.libraryError("读取评审失败：" + err.Error())
	}
	title := "评审记录"
	if chapter > 0 {
		title = fmt.Sprintf("评审记录 · 第 %d 章", chapter)
	}
	m.library = newLibraryState(m.width, m.height, title, func(w int) string {
		return renderReviewsText(info, reviews, chapter, w)
	})
	m.textarea.Blur()
	return m, nil
}

func renderReviewsText(info *library.BookInfo, reviews []domain.ReviewEntry, chapter int, width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)

	var b strings.Builder
	b.WriteString(titleStyle.Render(bookHeading(info)))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  评审 %d 条", len(reviews))))
	b.WriteString("\n")
	if len(reviews) == 0 {
		b.WriteString("\n")
		if chapter > 0 {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("第 %d 章暂无评审记录。", chapter)))
		} else {
			b.WriteString(mutedStyle.Render("暂无评审记录：Editor 在弧末或按评审间隔触发。"))
		}
		b.WriteString("\n")
		return b.String()
	}
	// 一条评审：首行 章号 [范围] 裁定 · 契约 · 影响 · 评分维度（同一行放得下就放）；
	// 次行结论；每个问题一行，建议用 → 接在同一行，超宽截断。
	for _, r := range reviews {
		verdictStyle := reviewVerdictStyle(r.Verdict)
		head := verdictStyle.Render(fmt.Sprintf("第 %d 章", r.Chapter)) +
			dimStyle.Render(" ["+reviewScopeText(r.Scope)+"] ") +
			verdictStyle.Render(reviewVerdictText(r.Verdict))
		if r.ContractStatus != "" {
			head += mutedStyle.Render(" · 契约 " + r.ContractStatus)
		}
		if len(r.AffectedChapters) > 0 {
			head += mutedStyle.Render(fmt.Sprintf(" · 影响 %v", r.AffectedChapters))
		}
		if len(r.Dimensions) > 0 {
			parts := make([]string, 0, len(r.Dimensions))
			for _, d := range r.Dimensions {
				parts = append(parts, fmt.Sprintf("%s %d", d.Dimension, d.Score))
			}
			dims := mutedStyle.Render("  " + strings.Join(parts, " · "))
			if lipgloss.Width(head)+lipgloss.Width(dims) <= width {
				head += dims
				b.WriteString(head)
				b.WriteString("\n")
			} else {
				// 七个维度的评分放不下一行就折行，别把后几维吞掉——
				// 被吞掉的往往正是分数最低、最该看的那几个。
				b.WriteString(head)
				b.WriteString("\n  ")
				writeIndented(&b, strings.Join(parts, " · "), width, 2, mutedStyle)
			}
		} else {
			b.WriteString(head)
			b.WriteString("\n")
		}
		if r.Summary != "" {
			b.WriteString("  ")
			writeIndented(&b, r.Summary, width, 2, bodyStyle)
		}
		// 问题描述和修改建议都是整句，截断了等于没说。严重度标签占首行，
		// 描述缩进折行；建议再另起一行，视觉上跟描述分得开。
		const issueIndent = 2 + 12
		for _, issue := range r.Issues {
			b.WriteString("  " + issueSeverityStyle(issue.Severity).Render(padCell("["+issue.Severity+"]", 12)))
			desc := issue.Description
			if len(issue.Chapters) > 0 {
				desc += fmt.Sprintf("（ch %v）", issue.Chapters)
			}
			writeIndented(&b, desc, width, issueIndent, bodyStyle)
			if issue.Suggestion != "" {
				b.WriteString(strings.Repeat(" ", issueIndent))
				writeIndented(&b, "→ "+issue.Suggestion, width, issueIndent+2,
					lipgloss.NewStyle().Foreground(colorAccent2))
			}
		}
	}
	return b.String()
}

func reviewScopeText(scope string) string {
	switch scope {
	case "chapter":
		return "章节"
	case "arc":
		return "弧"
	case "global":
		return "全局"
	}
	return scope
}

func reviewVerdictText(v string) string {
	switch v {
	case "accept":
		return "通过"
	case "polish":
		return "需打磨"
	case "rewrite":
		return "需重写"
	}
	return v
}

func reviewVerdictStyle(v string) lipgloss.Style {
	switch v {
	case "accept":
		return lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	case "polish":
		return lipgloss.NewStyle().Foreground(colorReview).Bold(true)
	case "rewrite":
		return lipgloss.NewStyle().Foreground(colorError).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
}

func issueSeverityStyle(sev string) lipgloss.Style {
	switch sev {
	case "critical":
		return lipgloss.NewStyle().Foreground(colorError).Bold(true)
	case "error":
		return lipgloss.NewStyle().Foreground(colorReview)
	}
	return lipgloss.NewStyle().Foreground(colorDim)
}

// ── /foreshadow ──

func (m Model) openForeshadow() (tea.Model, tea.Cmd) {
	info, rep, err := library.Foreshadow(m.runtime.Dir())
	if err != nil {
		return m.libraryError("读取伏笔失败：" + err.Error())
	}
	m.library = newLibraryState(m.width, m.height, "伏笔台账", func(w int) string {
		return renderForeshadowText(info, rep, w)
	})
	m.textarea.Blur()
	return m, nil
}

func renderForeshadowText(info *library.BookInfo, rep *library.ForeshadowReport, width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	openStyle := lipgloss.NewStyle().Foreground(colorReview)
	doneStyle := lipgloss.NewStyle().Foreground(colorSuccess)

	var b strings.Builder
	b.WriteString(titleStyle.Render(bookHeading(info)))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  未回收 %d · 已回收 %d · 参照最新章 %d", len(rep.Open), len(rep.Resolved), rep.Latest)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("是否停滞由 /diag 裁定，这里只列年龄。"))
	b.WriteString("\n")
	if len(rep.Open) == 0 && len(rep.Resolved) == 0 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("伏笔台账为空。"))
		b.WriteString("\n")
		return b.String()
	}
	// 一条伏笔：首行 标记 ID  埋于/已过/状态，描述缩进折行另起。
	// 描述是判断这条伏笔还能不能收的唯一依据，不能被截断。ID 列按最长 ID 对齐。
	idW := 0
	for _, f := range rep.Open {
		idW = max(idW, lipgloss.Width(f.ID))
	}
	for _, f := range rep.Resolved {
		idW = max(idW, lipgloss.Width(f.ID))
	}
	idW = min(max(idW, 6), 24)
	idCell := func(id string) string {
		return bodyStyle.Render(lipgloss.NewStyle().Width(idW).Render(truncateWidth(id, idW)))
	}
	descIndent := 2 + idW + 2
	if len(rep.Open) > 0 {
		b.WriteString(openStyle.Bold(true).Render("未回收"))
		b.WriteString("\n")
		for _, f := range rep.Open {
			line := openStyle.Render("○ ") + idCell(f.ID) +
				mutedStyle.Render(fmt.Sprintf("  埋于 ch%d · 已过 %d 章 · %s", f.PlantedAt, f.Age, f.Status))
			b.WriteString(fitInlineLine(line, width))
			b.WriteString("\n")
			if f.Description != "" {
				b.WriteString(strings.Repeat(" ", descIndent))
				writeIndented(&b, f.Description, width, descIndent, dimStyle)
			}
		}
	}
	if len(rep.Resolved) > 0 {
		b.WriteString(doneStyle.Bold(true).Render("已回收"))
		b.WriteString("\n")
		for _, f := range rep.Resolved {
			line := doneStyle.Render("● ") + idCell(f.ID) +
				mutedStyle.Render(fmt.Sprintf("  埋于 ch%d · 收于 ch%d · 跨 %d 章", f.PlantedAt, f.ResolvedAt, f.Age))
			b.WriteString(fitInlineLine(line, width))
			b.WriteString("\n")
			if f.Description != "" {
				b.WriteString(strings.Repeat(" ", descIndent))
				writeIndented(&b, f.Description, width, descIndent, dimStyle)
			}
		}
	}
	return b.String()
}

// ── /characters ──

func (m Model) openCharacters() (tea.Model, tea.Cmd) {
	info, rep, err := library.Characters(m.runtime.Dir())
	if err != nil {
		return m.libraryError("读取角色失败：" + err.Error())
	}
	m.library = newLibraryState(m.width, m.height, "角色", func(w int) string {
		return renderCharactersText(info, rep, w)
	})
	m.textarea.Blur()
	return m, nil
}

func renderCharactersText(info *library.BookInfo, rep *library.CharacterReport, width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)

	var b strings.Builder
	b.WriteString(titleStyle.Render(bookHeading(info)))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  核心 %d · 配角 %d · 关系 %d", len(rep.Core), len(rep.Cast), len(rep.Relationships))))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("出场按章节摘要统计，参照最新章 %d；角色是否\"消失\"由 /diag 裁定。", rep.Latest)))
	b.WriteString("\n")

	if len(rep.Core) == 0 {
		b.WriteString(mutedStyle.Render("角色档案为空：Architect 尚未落盘。"))
		b.WriteString("\n")
	}
	// 一个核心角色一行：名（别名） 层级 · 定位 · 出场统计；有快照 / 描述时各追加一行（截断不折行）。
	for _, c := range rep.Core {
		line := nameStyle.Render(c.Name)
		if len(c.Aliases) > 0 {
			line += dimStyle.Render("（" + strings.Join(c.Aliases, "、") + "）")
		}
		line += mutedStyle.Render("  " + characterTierText(c.Tier))
		if c.Role != "" {
			line += mutedStyle.Render(" · " + c.Role)
		}
		if c.Appearances == 0 {
			line += dimStyle.Render("  尚未在任何章节摘要出现")
		} else {
			line += bodyStyle.Render(fmt.Sprintf("  出场 %d 章 · 首见 ch%d · 最近 ch%d", c.Appearances, c.FirstSeen, c.LastSeen))
		}
		b.WriteString(fitInlineLine(line, width))
		b.WriteString("\n")
		if c.Snapshot != nil {
			snap := c.Snapshot.Status
			if c.Snapshot.Power != "" {
				snap += " · " + c.Snapshot.Power
			}
			if c.Snapshot.Motivation != "" {
				snap += " · 动机 " + c.Snapshot.Motivation
			}
			if strings.TrimSpace(snap) != "" {
				// 状态、能力、动机连起来很容易过一行；这正是判断角色当前处境的信息，
				// 折行而不是截断。
				b.WriteString("  ")
				writeIndented(&b, fmt.Sprintf("快照 v%da%d  %s", c.Snapshot.Volume, c.Snapshot.Arc, snap),
					width, 2, dimStyle)
			}
		}
		if c.Description != "" {
			b.WriteString("  ")
			writeIndented(&b, c.Description, width, 2, dimStyle)
		}
	}

	if len(rep.Cast) > 0 {
		b.WriteString(titleStyle.Render("配角名册"))
		b.WriteString(dimStyle.Render("  按最近出场"))
		b.WriteString("\n")
		for _, c := range rep.Cast {
			line := "  " + bodyStyle.Render(c.Name) +
				mutedStyle.Render(fmt.Sprintf("  出场 %d · 首见 ch%d · 最近 ch%d", c.AppearanceCount, c.FirstSeenChapter, c.LastSeenChapter))
			if c.BriefRole != "" {
				line += dimStyle.Render(" · " + c.BriefRole)
			}
			b.WriteString(fitInlineLine(line, width))
			b.WriteString("\n")
		}
	}

	if len(rep.Relationships) > 0 {
		b.WriteString(titleStyle.Render("人物关系"))
		b.WriteString("\n")
		for _, r := range rep.Relationships {
			line := "  " + bodyStyle.Render(r.CharacterA+" — "+r.CharacterB) +
				mutedStyle.Render("："+r.Relation) + dimStyle.Render(fmt.Sprintf("（ch%d）", r.Chapter))
			b.WriteString(fitInlineLine(line, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func characterTierText(tier string) string {
	switch tier {
	case "core":
		return "核心"
	case "important", "":
		return "重要"
	case "secondary":
		return "次要"
	case "decorative":
		return "点缀"
	}
	return tier
}

// bookHeading 统一的《书名》抬头。
func bookHeading(info *library.BookInfo) string {
	name := info.Title
	if name == "" {
		name = "（未命名）"
	}
	return "《" + name + "》"
}

// ── /timeline ──

func (m Model) openTimeline() (tea.Model, tea.Cmd) {
	info, rows, err := library.Timeline(m.runtime.Dir())
	if err != nil {
		return m.libraryError("读取时间线失败：" + err.Error())
	}
	m.library = newLibraryState(m.width, m.height, "故事时间线", func(w int) string {
		return renderTimelineText(info, rows, w)
	})
	m.textarea.Blur()
	return m, nil
}

func renderTimelineText(info *library.BookInfo, rows []library.TimelineRow, width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	warnStyle := lipgloss.NewStyle().Foreground(colorReview)

	var b strings.Builder
	b.WriteString(titleStyle.Render(bookHeading(info)))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d 条", len(rows))))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↩ = 这个故事时间之前出现过、中间隔了别的时间，也就是往回跳了。回叙也会这样，是否合理由你判断。"))
	b.WriteString("\n\n")
	if len(rows) == 0 {
		b.WriteString(mutedStyle.Render("时间线为空：还没有章节提取出时间事件。"))
		b.WriteString("\n")
		return b.String()
	}

	timeW := 0
	for _, r := range rows {
		timeW = max(timeW, lipgloss.Width(r.Time))
	}
	// 故事时间和事件都是自由文本，长度没有上界（"首次穿越后，手机显示停在下午四点十七分"
	// 就有 38 列）。硬挤成两列的结果是要么截断、要么把事件列顶歪、续行还对不齐，
	// 所以一条一条来：首行是章号 + 时间，事件缩进折行另起。任意长度都对齐。
	const eventIndent = 7 // 对齐首行的 "  " + "%4d " 之后
	for _, r := range rows {
		mark := "  "
		if r.Revisited {
			mark = warnStyle.Render("↩ ")
		}
		b.WriteString(mark)
		b.WriteString(dimStyle.Render(fmt.Sprintf("%4d ", r.Chapter)))
		writeIndented(&b, r.Time, width, eventIndent, mutedStyle)

		event := r.Event
		if len(r.Characters) > 0 {
			event += "（" + strings.Join(r.Characters, "、") + "）"
		}
		b.WriteString(strings.Repeat(" ", eventIndent))
		writeIndented(&b, event, width, eventIndent, bodyStyle)
	}
	return b.String()
}

// ── /violations ──

func (m Model) openViolations() (tea.Model, tea.Cmd) {
	info, rows, err := library.Violations(m.runtime.Dir())
	if err != nil {
		return m.libraryError("读取违规台账失败：" + err.Error())
	}
	m.library = newLibraryState(m.width, m.height, "规则违规台账", func(w int) string {
		return renderViolationsText(info, rows, w)
	})
	m.textarea.Blur()
	return m, nil
}

func renderViolationsText(info *library.BookInfo, rows []library.ViolationRow, width int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	warnStyle := lipgloss.NewStyle().Foreground(colorReview)

	total := 0
	for _, row := range rows {
		total += len(row.Violations)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(bookHeading(info)))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d 章 · %d 条", len(rows), total)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("只列当前仍存在的：返工后清掉的章不再出现。规则来自 ~/.ainovel/rules 与 ./.ainovel/rules。"))
	b.WriteString("\n\n")
	if len(rows) == 0 {
		b.WriteString(mutedStyle.Render("没有未处理的机械违规。"))
		b.WriteString("\n")
		return b.String()
	}

	for _, row := range rows {
		b.WriteString(dimStyle.Render(fmt.Sprintf("第 %d 章", row.Chapter)))
		if row.At != "" {
			b.WriteString(dimStyle.Render(" · " + row.At))
		}
		b.WriteString("\n")
		for _, v := range row.Violations {
			style := warnStyle
			if v.Severity == rules.SeverityError {
				style = errStyle
			}
			b.WriteString("  ")
			b.WriteString(style.Render(padCell(string(v.Severity), 8)))
			b.WriteString(mutedStyle.Render(padCell(v.Rule, 18)))
			b.WriteString(" ")
			detail := v.Target
			if v.Actual != nil {
				detail += fmt.Sprintf("  实际 %v", v.Actual)
			}
			if v.Limit != nil {
				detail += fmt.Sprintf("  阈值 %v", v.Limit)
			}
			// 违规对象可能是一整个短语，截断了就不知道到底犯的是哪一条。
			writeIndented(&b, detail, width, 29, bodyStyle)
		}
	}
	return b.String()
}

// padCell 把一格补到指定显示宽度（CJK 按两列算），列才对得齐。
func padCell(value string, width int) string {
	if pad := width - lipgloss.Width(value); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}
