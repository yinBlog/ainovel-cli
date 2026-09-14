package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/library"
)

// libraryState 是书架类只读面板（/books /chapters /read）的共用状态：
// 一个标题、一段按宽度重排的正文、一个可滚动 viewport。
type libraryState struct {
	title    string
	render   func(width int) string
	renderW  int
	viewport viewport.Model

	// 章节阅读态：dir + chapter 非零时 n / p（或 ←→）可翻到相邻章，内容原地替换。
	dir     string
	chapter int

	// 列表态：rows（章节）或 books（书架）非空时 ↑↓ 移动光标、Enter 进入下一级；
	// listStart 是列表首行在正文中的行号，rowHeight 是每个条目占的行数（书架条目两行）。
	info      *library.BookInfo
	rows      []library.ChapterInfo
	books     []library.Book
	hits      []library.SearchHit // /search 结果；query/truncated 只在这个模式下有意义
	query     string
	truncated bool
	scanned   int             // 扫过的章数：没命中时用它区分"书还没写"和"真的没有"
	inUse     map[string]bool // 被别的进程占着的小说目录，不能切过去
	// 新开书输入态：列表末行的“＋ 新开一本书”按下后进入，整块内容让位给一行路径输入。
	// 让位而不是在列表里内嵌输入框，是为了不打乱按固定行高滚动的光标数学。
	adding    bool
	input     textinput.Model
	addPrefix string // 输入框预填的父目录，用户只需补书名
	cursor    int
	listStart int
	rowHeight int
	// rowOffsets 是每个条目首行在正文里的行号，由渲染函数登记。有它之后条目可以
	// 不定高（展开态），光标滚动不再依赖 listStart+i*rowHeight 这套固定行高假设。
	// 为空时退回固定行高，老面板不受影响。
	rowOffsets []int
	// expanded 为 true 时，光标所在条目就地展开成完整内容（不再用 … 吞掉）。
	expanded bool

	// back 非空表示本面板是从另一个面板进入的（列表 → 阅读），Esc 回到它而不是关闭。
	back *libraryState
}

func newLibraryState(width, height int, title string, render func(width int) string) *libraryState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	s := &libraryState{title: title, render: render, viewport: viewport.New(contentW, boxH-4)}
	s.setContent(contentW)
	return s
}

func (s *libraryState) setContent(contentW int) {
	s.renderW = contentW
	s.viewport.SetContent(s.render(contentW))
}

// showChapter 把面板切到第 n 章：读不到（未写 / 不存在）时保持当前章不动，返回 false。
func (s *libraryState) showChapter(n int) bool {
	if s.dir == "" || n <= 0 {
		return false
	}
	view, err := library.ReadChapter(s.dir, n)
	if err != nil {
		return false
	}
	s.chapter = n
	s.title = chapterViewTitle(view)
	s.render = func(w int) string { return renderChapterText(view, w) }
	s.setContent(s.renderW)
	s.viewport.GotoTop()
	return true
}

func chapterViewTitle(view *library.ChapterView) string {
	title := fmt.Sprintf("第 %d 章", view.Chapter)
	if view.Title != "" {
		title += " " + view.Title
	}
	return title
}

func (s *libraryState) hint() string {
	esc := "Esc 关闭"
	if s.back != nil {
		esc = "Esc 返回上级"
	}
	switch {
	case s.chapter > 0:
		return "  n/p 或 ←→ 上下章 · ↑↓ 滚动 · PgUp/PgDn 翻页 · " + esc
	case len(s.rows) > 0:
		return "  ↑↓ 选章 · Enter 阅读 · Tab " + expandLabel(s.expanded) + " · PgUp/PgDn 翻页 · " + esc
	case len(s.hits) > 0:
		return "  ↑↓ 选命中 · Enter 跳到该章 · Tab " + expandLabel(s.expanded) + " · " + esc
	case s.adding:
		return "  Enter 创建并切过去 · Esc 返回书架"
	case len(s.books) > 0:
		return "  ↑↓ 选书 · Enter 切到该书创作 · → 只看章节 · 末行新开一本 · " + esc
	}
	return "  ↑↓ 滚动 · PgUp/PgDn 翻页 · Home/End 首尾 · " + esc
}

// itemCount 返回当前列表的条目数（章节或书架）。
func (s *libraryState) itemCount() int {
	if len(s.rows) > 0 {
		return len(s.rows)
	}
	if len(s.hits) > 0 {
		return len(s.hits)
	}
	if len(s.books) > 0 {
		return len(s.books) + 1 // 末行是“＋ 新开一本书”
	}
	return 0
}

// moveCursor 在列表里移动光标并让光标条目保持在可视区内。
func (s *libraryState) moveCursor(delta int) {
	n := s.itemCount()
	if n == 0 {
		return
	}
	s.cursor = min(max(0, s.cursor+delta), n-1)
	s.setContent(s.renderW)
	first, last := s.cursorLines()
	top := s.viewport.YOffset
	h := max(1, s.viewport.Height)
	switch {
	case first < top:
		s.viewport.SetYOffset(first)
	case last >= top+h:
		s.viewport.SetYOffset(last - h + 1)
	}
}

// openCursorBook 从书架进入光标所在书的章节列表；目录缺失或未开书返回 nil。
func (s *libraryState) openCursorBook(width, height int) *libraryState {
	if len(s.books) == 0 {
		return nil
	}
	bk := s.books[s.cursor]
	if bk.Missing || bk.Info == nil {
		return nil
	}
	child, err := newChaptersState(bk.Dir, width, height)
	if err != nil {
		return nil
	}
	child.back = s
	return child
}

// openCursorChapter 从列表进入光标所在章的阅读面板；返回 nil 表示该章还没有可读内容。
func (s *libraryState) openCursorChapter(width, height int) *libraryState {
	if len(s.rows) == 0 {
		return nil
	}
	ch := s.rows[s.cursor].Chapter
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

// syncCursorTo 把列表光标对到指定章（从阅读面板翻章后返回时用）。
func (s *libraryState) syncCursorTo(chapter int) {
	for i, r := range s.rows {
		if r.Chapter == chapter {
			s.moveCursor(i - s.cursor)
			return
		}
	}
}

func renderLibraryModal(width, height int, state *libraryState) string {
	if state == nil {
		return ""
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	if state.viewport.Width != contentW {
		state.viewport.Width = contentW
	}
	if state.viewport.Height != boxH-4 {
		state.viewport.Height = boxH - 4
	}
	if state.renderW != contentW {
		state.setContent(contentW)
	}
	modal := renderPaddedModalFrame(
		boxW, boxH, state.title,
		state.hint(),
		strings.Split(state.viewport.View(), "\n"),
	)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) handleLibraryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.library
	if s == nil {
		return m, nil
	}
	// 判定顺序必须与 itemCount 一致：一个面板只会是其中一种模式，但两处顺序不同
	// 是个等着被踩的陷阱——光标算的是一张表、按键走的是另一张。
	if len(s.rows) > 0 {
		return m.handleChapterListKey(msg)
	}
	if len(s.hits) > 0 {
		return m.handleSearchListKey(msg)
	}
	if len(s.books) > 0 {
		return m.handleBooksListKey(msg)
	}
	switch msg.Type {
	case tea.KeyEsc:
		if s.back != nil {
			// 从阅读回到列表：光标对到刚读的那一章。
			s.back.syncCursorTo(s.chapter)
			m.library = s.back
			return m, nil
		}
		m.library = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		s.viewport.ScrollUp(1)
	case tea.KeyDown:
		s.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		s.viewport.HalfPageUp()
	case tea.KeyPgDown:
		s.viewport.HalfPageDown()
	case tea.KeyHome:
		s.viewport.GotoTop()
	case tea.KeyEnd:
		s.viewport.GotoBottom()
	case tea.KeyRight:
		s.showChapter(s.chapter + 1)
	case tea.KeyLeft:
		s.showChapter(s.chapter - 1)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "n", "j":
			s.showChapter(s.chapter + 1)
		case "p", "k":
			s.showChapter(s.chapter - 1)
		}
	}
	return m, nil
}

// handleBooksListKey 是书架面板的按键：↑↓ 选书、Enter 切到该书、→ 只看章节、
// 末行 Enter 新开一本书、Esc 关闭。
func (m Model) handleBooksListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.library
	if s.adding {
		return m.handleNewBookInputKey(msg)
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.library = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		s.moveCursor(-1)
	case tea.KeyDown:
		s.moveCursor(1)
	case tea.KeyPgUp:
		s.moveCursor(-max(1, s.viewport.Height/max(1, s.rowHeight)-1))
	case tea.KeyPgDown:
		s.moveCursor(max(1, s.viewport.Height/max(1, s.rowHeight)-1))
	case tea.KeyHome:
		s.moveCursor(-len(s.books))
	case tea.KeyEnd:
		s.moveCursor(len(s.books))
	case tea.KeyEnter:
		// 书架的主用途是换书：Enter 切过去创作，只读浏览让给 →。
		if s.cursor == len(s.books) {
			return m, s.beginNewBook()
		}
		return m.switchToCursorBook()
	case tea.KeyRight:
		if child := s.openCursorBook(m.width, m.height); child != nil {
			m.library = child
		}
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			s.moveCursor(1)
		case "k":
			s.moveCursor(-1)
		case "l":
			if child := s.openCursorBook(m.width, m.height); child != nil {
				m.library = child
			}
		}
	}
	return m, nil
}

// switchToCursorBook 把会话切到光标所在的书。被别的进程占着、目录已不存在的书
// 在这里拦下来——列表已经标过，按下去再报一次原因。
func (m Model) switchToCursorBook() (tea.Model, tea.Cmd) {
	s := m.library
	if s == nil || s.cursor < 0 || s.cursor >= len(s.books) {
		return m, nil
	}
	bk := s.books[s.cursor]
	if bk.Missing {
		return m.libraryError("目录已不存在：" + library.DisplayPath(bk.Dir))
	}
	if s.inUse[bk.Dir] {
		return m.libraryError("这本书正被另一个 ainovel-cli 占用：" + library.DisplayPath(bk.Dir))
	}
	return m.switchBook(bk.Dir)
}

// handleChapterListKey 是章节列表面板的按键：↑↓ 选章、Enter 阅读、Esc 关闭或返回书架。
func (m Model) handleChapterListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.library
	switch msg.Type {
	case tea.KeyEsc:
		if s.back != nil {
			m.library = s.back
			return m, nil
		}
		m.library = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		s.moveCursor(-1)
	case tea.KeyDown:
		s.moveCursor(1)
	case tea.KeyPgUp:
		s.moveCursor(-max(1, s.viewport.Height-1))
	case tea.KeyPgDown:
		s.moveCursor(max(1, s.viewport.Height-1))
	case tea.KeyHome:
		s.moveCursor(-len(s.rows))
	case tea.KeyEnd:
		s.moveCursor(len(s.rows))
	case tea.KeyTab:
		s.toggleExpanded()
	case tea.KeyEnter:
		if child := s.openCursorChapter(m.width, m.height); child != nil {
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

// libraryError 把书架命令的失败写进事件流（与其它命令的错误回显一致）。
func (m Model) libraryError(summary string) (tea.Model, tea.Cmd) {
	m.applyEvent(host.Event{Time: time.Now(), Category: "ERROR", Summary: summary, Level: "error"})
	m.refreshEventViewport()
	return m, nil
}

// ── /books ──

func (m Model) openBooks() (tea.Model, tea.Cmd) {
	books, err := m.bookRegistry().List()
	if err != nil {
		return m.libraryError("读取书架失败：" + err.Error())
	}
	currentDir := ""
	if m.runtime != nil {
		currentDir = m.runtime.Dir()
	}
	state := newBooksState(books, currentDir, probeBooksInUse(books, currentDir), m.width, m.height)
	// 新开书的输入框预填当前这本书的同级目录，用户只需补一个书名——
	// 不预填的话只输名字会按进程 cwd 解析，那不是用户以为的地方。
	if launch := library.LaunchDirOf(currentDir); launch != "" {
		state.addPrefix = filepath.Dir(launch) + string(filepath.Separator)
	}
	m.library = state
	m.textarea.Blur()
	return m, nil
}

// newBooksState 构造书架面板：光标默认落在当前目录的书；每本书占两行。
// probeBooksInUse 在列出书架时试一次锁，标出"别的终端正在写"的书。
// 当前这本要排除：flock 按打开的文件判定，自己持有的锁也会被探成占用。
func probeBooksInUse(books []library.Book, currentDir string) map[string]bool {
	inUse := make(map[string]bool, len(books))
	for _, bk := range books {
		if bk.Dir == currentDir || bk.Missing {
			continue
		}
		if host.BookInUse(bk.Dir) {
			inUse[bk.Dir] = true
		}
	}
	return inUse
}

func newBooksState(books []library.Book, currentDir string, inUse map[string]bool, width, height int) *libraryState {
	s := &libraryState{books: books, inUse: inUse, title: "书架", listStart: 1, rowHeight: 2}
	for i, bk := range books {
		if bk.Dir == currentDir {
			s.cursor = i
			break
		}
	}
	s.render = func(w int) string {
		if s.adding {
			return renderNewBookInput(&s.input, w)
		}
		return renderBooksText(books, currentDir, inUse, w, s.cursor)
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	s.viewport = viewport.New(contentW, boxH-4)
	s.setContent(contentW)
	if len(books) > 0 {
		s.moveCursor(0)
	}
	return s
}

// renderBooksText 渲染书架；cursor 为光标条目下标，-1 表示无光标。
func renderBooksText(books []library.Book, currentDir string, inUse map[string]bool, width int, cursor int) string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)

	var b strings.Builder
	b.WriteString(titleStyle.Render("本机开过的小说"))
	b.WriteString(dimStyle.Render(fmt.Sprintf(" %d · Enter 切到该书创作 · → 只看章节", len(books))))
	b.WriteString("\n")
	cursorStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	if len(books) == 0 {
		b.WriteString(mutedStyle.Render("书架为空。"))
		b.WriteString("\n")
	}
	// 一本书两行：首行 光标 序号 书名 · 状态 · 章数字数 · 当前标记；次行 目录 · 最近打开。
	for i, bk := range books {
		if i == cursor {
			b.WriteString(cursorStyle.Render("›"))
			nameStyle = cursorStyle
		} else {
			b.WriteString(" ")
			nameStyle = lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("%2d. ", i+1)))
		switch {
		case bk.Missing:
			b.WriteString(mutedStyle.Render("（目录已不存在）"))
		case bk.Err != nil:
			b.WriteString(mutedStyle.Render("（读取失败：" + bk.Err.Error() + "）"))
		case bk.Info == nil:
			b.WriteString(mutedStyle.Render("（尚未开书）"))
		default:
			name := bk.Info.Title
			if name == "" {
				name = "（未命名）"
			}
			b.WriteString(nameStyle.Render(name))
			b.WriteString(bodyStyle.Render("  " + libraryPhaseLabel(bk.Info)))
			b.WriteString(mutedStyle.Render(fmt.Sprintf(" · %d 章 · %s 字", bk.Info.Completed, formatNumber(bk.Info.WordCount))))
		}
		if bk.Dir == currentDir {
			b.WriteString(lipgloss.NewStyle().Foreground(colorSuccess).Render("  ← 当前"))
		} else if inUse[bk.Dir] {
			b.WriteString(lipgloss.NewStyle().Foreground(colorReview).Render("  使用中"))
		}
		b.WriteString("\n     ")
		b.WriteString(dimStyle.Render(truncate(bookMetaLine(bk), width-5)))
		b.WriteString("\n")
	}
	// 末行“＋ 新开一本书”：同样占两行，列表的固定行高才不会被打乱。
	if cursor == len(books) {
		b.WriteString(cursorStyle.Render("›"))
		b.WriteString(cursorStyle.Render(" ＋ 新开一本书"))
	} else {
		b.WriteString(mutedStyle.Render("  ＋ 新开一本书"))
	}
	b.WriteString("\n     ")
	b.WriteString(dimStyle.Render("在新目录建一本，当前这本存档保留"))
	b.WriteString("\n")
	return b.String()
}

// renderNewBookInput 是按下“＋ 新开一本书”后的输入态：整块内容让位给一行路径输入。
// 让位而不是把输入框嵌进列表，是为了不打乱按固定行高滚动的光标数学。
func renderNewBookInput(input *textinput.Model, width int) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("新开一本书"))
	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("目录路径（不存在会自动创建）"))
	b.WriteString("\n")
	input.Width = max(8, width-4)
	b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Render("› ") + input.View())
	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Enter 创建并切过去 · Esc 返回书架"))
	b.WriteString("\n")
	return b.String()
}

// ── /chapters ──

func (m Model) openChapters() (tea.Model, tea.Cmd) {
	state, err := newChaptersState(m.runtime.Dir(), m.width, m.height)
	if err != nil {
		return m.libraryError("读取章节失败：" + err.Error())
	}
	m.library = state
	m.textarea.Blur()
	return m, nil
}

// newChaptersState 构造章节列表面板：光标默认落在写作中的章（没有则是最后一个已完成章）。
func newChaptersState(dir string, width, height int) (*libraryState, error) {
	info, list, err := library.Chapters(dir)
	if err != nil {
		return nil, err
	}
	s := &libraryState{dir: dir, info: info, rows: list}
	s.listStart = chaptersListStart
	for i, c := range list {
		switch c.Status {
		case library.StatusInProgress:
			s.cursor = i
		case library.StatusCompleted, library.StatusPendingRework:
			if s.cursor == 0 {
				s.cursor = i
			}
		}
	}
	s.render = func(w int) string {
		text, offsets := renderChaptersText(info, list, w, s.cursor, s.expanded)
		s.rowOffsets = offsets
		return text
	}
	s.title = "章节明细"
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	s.viewport = viewport.New(contentW, boxH-4)
	s.setContent(contentW)
	s.moveCursor(0)
	return s, nil
}

// renderChaptersText 渲染章节列表；cursor 为光标所在行下标，-1 表示无光标（CLI / 测试）。
// chaptersDetailWidth 是章节页左栏（书本详情）的宽度。太窄放不下简介，太宽挤掉
// 右栏的标题列——按总宽取 2/5 再夹在 [24,40]。
func chaptersDetailWidth(width int) int {
	return min(max(width*2/5, 24), 40)
}

// chaptersListStart 是章节列表首行在正文里的行号。右栏第一行是表头，所以是 1。
// 与 libraryState.listStart 必须一致，否则光标滚动会整体错位。
const chaptersListStart = 1

// renderChaptersText 把章节页排成两栏：左边书本详情，右边章节列表。
//
// 两栏是逐行拼起来再交给同一个 viewport 滚的，左栏比右栏短得多，滚过之后左侧自然
// 留白。用一个 viewport 而不是两个，是因为光标滚动的数学只认一份行号。
// 返回值还带上每章首行的行号：两栏是逐行拼的，右栏的行号就是整页的行号。
func renderChaptersText(info *library.BookInfo, list []library.ChapterInfo, width, cursor int, expanded bool) (string, []int) {
	detailW := chaptersDetailWidth(width)
	listW := max(20, width-detailW-2)
	left := chaptersDetailLines(info, detailW)
	right, offsets := chaptersListLines(list, listW, cursor, expanded)

	var b strings.Builder
	for i := 0; i < max(len(left), len(right)); i++ {
		cell := ""
		if i < len(left) {
			cell = left[i]
		}
		b.WriteString(lipgloss.NewStyle().Width(detailW).Render(cell))
		b.WriteString("  ")
		if i < len(right) {
			b.WriteString(right[i])
		}
		b.WriteString("\n")
	}
	return b.String(), offsets
}

// chaptersDetailLines 是左栏：书名、阶段进度、字数、卷弧、在写/待返工，然后简介。
func chaptersDetailLines(info *library.BookInfo, width int) []string {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)

	name := info.Title
	if name == "" {
		name = "（未命名）"
	}
	lines := []string{titleStyle.Render(truncate("《"+name+"》", width))}

	progress := libraryPhaseLabel(info)
	if info.Total > 0 {
		progress += fmt.Sprintf(" · %d/%d 章", info.Completed, info.Total)
	} else {
		progress += fmt.Sprintf(" · %d 章", info.Completed)
	}
	lines = append(lines, bodyStyle.Render(truncate(progress, width)))
	lines = append(lines, mutedStyle.Render(formatNumber(info.WordCount)+" 字"))
	if info.Volume > 0 || info.Arc > 0 {
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("卷 %d · 弧 %d", info.Volume, info.Arc)))
	}
	if info.InProgress > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorAccent).
			Render(fmt.Sprintf("正在写 第 %d 章", info.InProgress)))
	}
	if len(info.PendingRewrites) > 0 {
		labels := make([]string, 0, len(info.PendingRewrites))
		for _, ch := range info.PendingRewrites {
			labels = append(labels, strconv.Itoa(ch))
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(colorReview).
			Render(truncate("待返工 "+strings.Join(labels, "、"), width)))
	}
	if strings.TrimSpace(info.Synopsis) != "" {
		lines = append(lines, "", mutedStyle.Render("简介"))
		for _, line := range strings.Split(wrapText(info.Synopsis, width), "\n") {
			lines = append(lines, dimStyle.Render(line))
		}
	}
	return lines
}

// chaptersListLines 是右栏：一行表头 + 一章一行；展开的那一章多占几行放完整信息。
// 第二个返回值是每章首行在返回切片里的行号，供光标滚动按实际行高计算。
func chaptersListLines(list []library.ChapterInfo, width, cursor int, expanded bool) ([]string, []int) {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	cursorStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	header := dimStyle.Render("章节 " + strconv.Itoa(len(list)) + " 条 · Enter 看正文 · Tab 展开 · /read <章节号> 直达")
	if len(list) == 0 {
		return []string{header, mutedStyle.Render("尚无章节：大纲未生成。")}, nil
	}

	const untitled = "（无标题）"
	titleW := 0
	for _, c := range list {
		t := c.Title
		if t == "" {
			t = untitled
		}
		titleW = max(titleW, lipgloss.Width(t))
	}
	titleW = min(max(titleW, 8), max(8, width/2))

	lines := make([]string, 0, len(list)+1)
	offsets := make([]int, 0, len(list))
	lines = append(lines, header)
	for i, c := range list {
		offsets = append(offsets, len(lines))
		marker, style := chapterStatusStyle(c.Status)
		title := c.Title
		if title == "" {
			title = untitled
		}
		titleStyle, pointer := bodyStyle, " "
		if i == cursor {
			// 光标行：左侧 › 指示 + 标题强调色，不用反色（反色在亮暗主题下都刺眼）。
			pointer, titleStyle = cursorStyle.Render("›"), cursorStyle
		}
		head := pointer + style.Render(marker+" "+fmt.Sprintf("%3d", c.Chapter)) + "  " +
			titleStyle.Render(lipgloss.NewStyle().Width(titleW).Render(truncateWidth(title, titleW)))
		// 窄栏放不下就整块省掉字数，而不是让它截成半截数字：状态决定这章要不要管，
		// 字数只是参考。
		line := head + mutedStyle.Render("  "+chapterMeta(c, true))
		if lipgloss.Width(line) > width {
			line = head + mutedStyle.Render("  "+chapterMeta(c, false))
		}
		if c.Status == library.StatusPlanned && c.CoreEvent != "" {
			line += dimStyle.Render("  — " + c.CoreEvent)
		}
		lines = append(lines, fitInlineLine(line, width))
		// 展开的那一章：标题和核心事件都是会被列宽吃掉的部分，这里原样折行补上。
		if expanded && i == cursor {
			lines = append(lines, chapterExpandedLines(c, width)...)
		}
	}
	return lines, offsets
}

// chapterExpandedLines 是展开态补充的完整信息：标题全文与大纲核心事件。
func chapterExpandedLines(c library.ChapterInfo, width int) []string {
	const indent = 8
	dim := lipgloss.NewStyle().Foreground(colorDim)
	var out []string
	if c.Title != "" {
		out = append(out, prefixed(indent, wrapIndented("标题　"+c.Title, width, indent+4, dim))...)
	}
	if c.CoreEvent != "" {
		out = append(out, prefixed(indent, wrapIndented("核心　"+c.CoreEvent, width, indent+4, dim))...)
	}
	if c.WordCount > 0 {
		out = append(out, prefixed(indent, []string{dim.Render(fmt.Sprintf("字数　%d", c.WordCount))})...)
	}
	return out
}

// prefixed 给每行前面补上缩进；首行的缩进由调用方一并算进 wrapIndented 的续行里。
func prefixed(indent int, lines []string) []string {
	pad := strings.Repeat(" ", indent)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, pad+line)
			continue
		}
		out = append(out, line)
	}
	return out
}
func chapterStatusStyle(s library.ChapterStatus) (string, lipgloss.Style) {
	switch s {
	case library.StatusCompleted:
		return "●", lipgloss.NewStyle().Foreground(colorSuccess)
	case library.StatusPendingRework:
		return "↺", lipgloss.NewStyle().Foreground(colorReview)
	case library.StatusInProgress:
		return "▸", lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	}
	return "○", lipgloss.NewStyle().Foreground(colorDim)
}

func chapterStatusText(s library.ChapterStatus) string {
	switch s {
	case library.StatusCompleted:
		return "已完成"
	case library.StatusPendingRework:
		return "待返工"
	case library.StatusInProgress:
		return "写作中"
	}
	return "待写"
}

// ── /read ──

func (m Model) openChapter(args []string) (tea.Model, tea.Cmd) {
	if len(args) != 1 {
		return m.libraryError("用法：/read <章节号>")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		return m.libraryError("章节号必须是正整数：" + args[0])
	}
	dir := m.runtime.Dir()
	view, err := library.ReadChapter(dir, n)
	if err != nil {
		return m.libraryError(err.Error())
	}
	m.library = newLibraryState(m.width, m.height, chapterViewTitle(view), func(w int) string {
		return renderChapterText(view, w)
	})
	m.library.dir = dir
	m.library.chapter = n
	m.textarea.Blur()
	return m, nil
}

func renderChapterText(view *library.ChapterView, width int) string {
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	bodyStyle := lipgloss.NewStyle().Foreground(bodyTextColor).Width(width)

	var b strings.Builder
	// 头一行回答"这章在全书哪儿、什么状态、多长"；读的时候不必另开 /chapters 再看一次。
	parts := []string{fmt.Sprintf("%d 字", view.WordCount)}
	if view.Total > 0 {
		parts = append([]string{fmt.Sprintf("第 %d / %d 章", view.Chapter, view.Total)}, parts...)
	}
	if label := chapterStatusText(view.Status); label != "" && !view.Draft {
		parts = append(parts, label)
	}
	if view.Draft {
		parts = append(parts, "草稿（未提交）")
	}
	b.WriteString(mutedStyle.Render(strings.Join(parts, " · ")))
	b.WriteString("\n")
	// Editor 的判定紧跟在头部：读一章时最想知道的就是"这章过没过"。
	// 完整评审仍归 /reviews，这里只给裁定和结论。
	if r := view.Review; r != nil {
		verdict := reviewVerdictStyle(r.Verdict).Render(reviewVerdictText(r.Verdict))
		head := "Editor · " + reviewScopeText(r.Scope) + "评审 " + verdict
		if r.Issues > 0 {
			head += mutedStyle.Render(fmt.Sprintf(" · %d 个问题", r.Issues))
		}
		b.WriteString(head)
		b.WriteString("\n")
		// 只有章节级评审的结论才是针对这一章的；弧/全局评审的结论覆盖一批章，
		// 挂在每章头部会把正文顶到屏幕外——那种只给裁定，完整结论去 /reviews。
		if r.Summary != "" && r.Scope == "chapter" {
			b.WriteString("  ")
			writeIndented(&b, r.Summary, width, 2, lipgloss.NewStyle().Foreground(colorDim))
		} else if r.Summary != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
				Render("  该评审覆盖多章，完整结论见 /reviews"))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	// 按段落逐段排版：lipgloss Width 会对无空格的中文做硬折行，段间空行原样保留。
	for _, para := range strings.Split(strings.TrimRight(view.Body, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(bodyStyle.Render(para))
		b.WriteString("\n")
	}
	return b.String()
}

func libraryPhaseLabel(info *library.BookInfo) string {
	switch info.Phase {
	case "init":
		return "初始化"
	case "premise":
		return "前提已定"
	case "outline":
		return "大纲已定"
	case "writing":
		switch info.Flow {
		case "reviewing":
			return "评审中"
		case "rewriting":
			return "返工中"
		case "polishing":
			return "打磨中"
		case "steering":
			return "处理干预"
		}
		return "写作中"
	case "complete":
		return "已完结"
	}
	return string(info.Phase)
}

// bookMetaLine 是书架每本书的第二行：目录 + 花费 + 活跃度。
// 能切书之后，"该继续哪本"靠的就是这行——光有书名和章数判断不了。
func bookMetaLine(bk library.Book) string {
	parts := []string{library.DisplayPath(bk.Dir)}
	if bk.Activity.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", bk.Activity.CostUSD))
	}
	if idle, ok := bk.Activity.Idle(); ok {
		parts = append(parts, "写于 "+humanizeIdle(idle)+"前")
	} else if !bk.LastOpened.IsZero() {
		// 还没写出章节的书只能报最近打开时间，别让这一格空着。
		parts = append(parts, "开于 "+bk.LastOpened.Format("01-02 15:04"))
	}
	if bk.Activity.RecentChapters > 0 {
		parts = append(parts, fmt.Sprintf("近七日 +%d 章", bk.Activity.RecentChapters))
	}
	return strings.Join(parts, " · ")
}

// humanizeIdle 把"多久没动了"说成人话。跨度可能从几分钟到几个月，
// 精确到秒没有意义，量级才是判断依据。
func humanizeIdle(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%d 个月", int(d.Hours()/24/30))
	}
}

// beginNewBook 进入新开书的路径输入态。预填同级目录，光标落在末尾，直接补书名即可。
func (s *libraryState) beginNewBook() tea.Cmd {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 0
	input.Placeholder = "新书所在目录"
	input.TextStyle = lipgloss.NewStyle().Foreground(bodyTextColor)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorDim)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(colorAccent)
	input.SetValue(s.addPrefix)
	input.CursorEnd()
	s.input = input
	s.adding = true
	s.setContent(s.renderW)
	return s.input.Focus()
}

// handleNewBookInputKey 处理路径输入：Enter 建书并切过去，Esc 退回列表。
func (m Model) handleNewBookInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.library
	switch msg.Type {
	case tea.KeyEsc:
		s.adding = false
		s.input.Blur()
		s.setContent(s.renderW)
		return m, nil
	case tea.KeyEnter:
		// 建书失败（目录已有书、被占用）由 startNewBook 报错；面板留在输入态，
		// 用户改个路径就能重试，不用从头再打开一次书架。
		path := s.input.Value()
		next, cmd := m.startNewBook(path)
		if model, ok := next.(Model); ok && model.library == s {
			s.setContent(s.renderW)
		}
		return next, cmd
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.setContent(s.renderW)
	return m, cmd
}

// chapterMeta 是章节行右侧的状态与字数。withWords=false 时只留状态，
// 供窄栏退让使用。
func chapterMeta(c library.ChapterInfo, withWords bool) string {
	meta := chapterStatusText(c.Status)
	if withWords && c.WordCount > 0 {
		meta += fmt.Sprintf(" · %d 字", c.WordCount)
	}
	return meta
}

// cursorLines 返回光标条目占据的首行和末行号。渲染函数登记过 rowOffsets 就按实际
// 行号算（条目可不定高），否则退回 listStart + i*rowHeight 的固定行高。
func (s *libraryState) cursorLines() (int, int) {
	if len(s.rowOffsets) > s.cursor && s.cursor >= 0 {
		first := s.rowOffsets[s.cursor]
		last := first
		if s.cursor+1 < len(s.rowOffsets) {
			last = s.rowOffsets[s.cursor+1] - 1
		} else {
			last = first + max(1, s.rowHeight) - 1
		}
		return first, max(first, last)
	}
	rh := max(1, s.rowHeight)
	first := s.listStart + s.cursor*rh
	return first, first + rh - 1
}

// toggleExpanded 展开/收起光标所在条目。展开后重排内容并把条目重新拉回视野，
// 否则展开的那几行可能正好落在视口下方，用户以为没反应。
func (s *libraryState) toggleExpanded() {
	s.expanded = !s.expanded
	s.setContent(s.renderW)
	s.moveCursor(0)
}

// expandLabel 是提示行里 Tab 的动作名——展开着就该提示收起，否则用户不知道怎么退回去。
func expandLabel(expanded bool) string {
	if expanded {
		return "收起"
	}
	return "展开"
}
