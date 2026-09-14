package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/library"
)

// /search：全书检索。写到几百章之后，"这个设定我在哪章埋的"是日常动作，
// 靠 /chapters 扫标题或一章章 /read 都不现实。
//
// 和其余 library 面板一样只读、不占目录锁，挂机写作时也能查。

// searchRowHeight 是每条命中占的行数：首行 章号·来源·标题，次行摘录。
const searchRowHeight = 2

func (m Model) openSearch(query string) (tea.Model, tea.Cmd) {
	state, err := newSearchState(m.runtime.Dir(), query, m.width, m.height)
	if err != nil {
		return m.libraryError("检索失败：" + err.Error())
	}
	m.library = state
	m.textarea.Blur()
	return m, nil
}

func newSearchState(dir, query string, width, height int) (*libraryState, error) {
	info, result, err := library.Search(dir, library.SearchOptions{Query: query})
	if err != nil {
		return nil, err
	}
	s := &libraryState{
		dir: dir, info: info, title: "检索 " + query,
		hits: result.Hits, query: query, truncated: result.Truncated, scanned: result.Scanned,
		listStart: 2, rowHeight: searchRowHeight,
	}
	s.render = func(w int) string {
		text, offsets := renderSearchText(s, w)
		s.rowOffsets = offsets
		return text
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	s.viewport = viewport.New(contentW, boxH-4)
	s.setContent(contentW)
	return s, nil
}

// handleSearchListKey 是检索结果面板的按键：↑↓ 选命中、Enter 读那一章、Esc 关闭。
func (m Model) handleSearchListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.library
	switch msg.Type {
	case tea.KeyEsc:
		m.library = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		s.moveCursor(-1)
	case tea.KeyDown:
		s.moveCursor(1)
	case tea.KeyPgUp:
		s.moveCursor(-max(1, s.viewport.Height/searchRowHeight-1))
	case tea.KeyPgDown:
		s.moveCursor(max(1, s.viewport.Height/searchRowHeight-1))
	case tea.KeyHome:
		s.moveCursor(-len(s.hits))
	case tea.KeyEnd:
		s.moveCursor(len(s.hits))
	case tea.KeyTab:
		s.toggleExpanded()
	case tea.KeyEnter:
		if child := s.openCursorHit(m.width, m.height); child != nil {
			m.library = child
		}
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			s.moveCursor(1)
		case "k":
			s.moveCursor(-1)
		}
	}
	return m, nil
}

// openCursorHit 从命中跳到那一章的阅读面板。命中可能落在大纲上（该章还没写），
// 这种情况读不到正文，保持停在结果列表。
func (s *libraryState) openCursorHit(width, height int) *libraryState {
	if s.cursor < 0 || s.cursor >= len(s.hits) {
		return nil
	}
	ch := s.hits[s.cursor].Chapter
	if ch <= 0 {
		return nil
	}
	view, err := library.ReadChapter(s.dir, ch)
	if err != nil {
		return nil
	}
	child := newLibraryState(width, height, chapterViewTitle(view), func(w int) string {
		return renderChapterText(view, w)
	})
	child.dir, child.chapter, child.back = s.dir, ch, s
	return child
}

// 返回值还带上每条命中首行的行号：展开的那条会多占几行，光标滚动得按实际行高算。
func renderSearchText(s *libraryState, width int) (string, []int) {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	hitStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	cursorStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	var b strings.Builder
	b.WriteString(titleStyle.Render("检索 “" + s.query + "”"))
	summary := fmt.Sprintf(" %d 处命中", len(s.hits))
	if s.truncated {
		summary += "（已达上限，还有更多）"
	}
	b.WriteString(dimStyle.Render(summary))
	b.WriteString("\n\n")
	if len(s.hits) == 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("没有找到。已扫 %d 章的正文与摘要，以及全部大纲。", s.scanned)))
		b.WriteString("\n")
		return b.String(), nil
	}
	offsets := make([]int, 0, len(s.hits))
	line := 2 // 标题行 + 空行

	for i, hit := range s.hits {
		offsets = append(offsets, line)
		if i == s.cursor {
			b.WriteString(cursorStyle.Render("›"))
		} else {
			b.WriteString(" ")
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf(" 第 %d 章 ", hit.Chapter)))
		b.WriteString(mutedStyle.Render(string(hit.Source)))
		if strings.TrimSpace(hit.Title) != "" {
			b.WriteString(bodyStyle.Render("  " + hit.Title))
		}
		b.WriteString("\n     ")
		line += 2
		if s.expanded && i == s.cursor {
			// 展开：摘录原样折行，不再让 … 把上下文吞掉——那点上下文正是判断
			// "是不是我要找的那处"的依据。
			wrapped := wrapIndented(hit.Excerpt, width, 5, bodyStyle)
			b.WriteString(strings.Join(wrapped, "\n"))
			b.WriteString("\n")
			line += len(wrapped) - 1
			continue
		}
		b.WriteString(renderSearchExcerpt(hit, width-6, bodyStyle, hitStyle))
		b.WriteString("\n")
	}
	return b.String(), offsets
}

// renderSearchExcerpt 把关键词按 MatchAt/MatchLen 高亮出来。library 已经算好了
// rune 区间，这里只负责切片和上色——不要在这里再找一遍关键词，那会和大小写折叠打架。
func renderSearchExcerpt(hit library.SearchHit, width int, body, highlight lipgloss.Style) string {
	runes := []rune(hit.Excerpt)
	at, end := hit.MatchAt, hit.MatchAt+hit.MatchLen
	if at < 0 || end > len(runes) || at > end {
		return body.Render(truncate(hit.Excerpt, width))
	}
	rendered := body.Render(string(runes[:at])) +
		highlight.Render(string(runes[at:end])) +
		body.Render(string(runes[end:]))
	if lipgloss.Width(rendered) <= width {
		return rendered
	}
	// 超宽时保底：宁可丢高亮也不能把整行顶出面板。
	return body.Render(truncate(hit.Excerpt, width))
}
