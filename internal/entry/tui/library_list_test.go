package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/library"
)

func TestChapterListCursorEnterAndBack(t *testing.T) {
	dir := newNavBook(t) // 1、2 章终稿，无第 3 章内容；大纲未写，所以 rows 只有已完成章
	state, err := newChaptersState(dir, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.rows) != 2 {
		t.Fatalf("rows = %+v", state.rows)
	}
	// 默认光标落在最后一个已完成章。
	if state.cursor != 1 {
		t.Fatalf("default cursor = %d, want last completed", state.cursor)
	}
	if !strings.Contains(state.hint(), "Enter 阅读") {
		t.Fatalf("list hint should mention Enter: %q", state.hint())
	}
	body := ansi.Strip(state.viewport.View())
	if !strings.Contains(body, "›") {
		t.Fatalf("cursor pointer missing:\n%s", body)
	}

	m := NewModel(nil, "")
	m.width, m.height = 120, 40
	m.library = state

	next, _ := m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.library.cursor != 0 {
		t.Fatalf("↑ should move cursor to 0, got %d", m.library.cursor)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.library.cursor != 0 {
		t.Fatalf("cursor must clamp at 0, got %d", m.library.cursor)
	}

	// Enter 进入第 1 章阅读，面板带 back。
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.library.chapter != 1 || m.library.back == nil || len(m.library.rows) != 0 {
		t.Fatalf("Enter should open chapter 1 reader with back link: %+v", m.library)
	}
	if !strings.Contains(m.library.hint(), "返回上级") {
		t.Fatalf("reader opened from list should say 返回上级: %q", m.library.hint())
	}
	// 阅读态翻到第 2 章，再 Esc 回列表：光标同步到第 2 章。
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if m.library.chapter != 2 {
		t.Fatalf("n should go to chapter 2, got %d", m.library.chapter)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.library == nil || len(m.library.rows) != 2 {
		t.Fatalf("Esc from reader should return to the list, got %+v", m.library)
	}
	if m.library.cursor != 1 {
		t.Fatalf("cursor should follow the chapter just read (2 → index 1), got %d", m.library.cursor)
	}
	// 列表上 Esc 才真正关闭。
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.library != nil {
		t.Fatal("Esc on the list should close the panel")
	}
}

func TestBooksListEntersChaptersAndBack(t *testing.T) {
	dir := newNavBook(t)
	books := []library.Book{
		{Missing: true},
		{Info: &library.BookInfo{Title: "光斑", Phase: "writing", Completed: 2}},
	}
	books[0].Dir = "/tmp/gone"
	books[1].Dir = dir
	state := newBooksState(books, dir, nil, 120, 40)
	if state.cursor != 1 || state.rowHeight != 2 {
		t.Fatalf("cursor should start on the current book: %+v", state)
	}
	// 书架的主用途是换书：Enter 是切过去创作，只读浏览让给 →。
	if !strings.Contains(state.hint(), "Enter 切到该书创作") {
		t.Fatalf("books hint: %q", state.hint())
	}

	m := NewModel(nil, "")
	m.width, m.height = 120, 40
	m.library = state

	// 目录缺失的书：Enter（切书）被拒，面板留在原地。
	next, _ := m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(m.library.books) != 2 || m.library.cursor != 0 {
		t.Fatalf("Enter on a missing dir must stay on the shelf: %+v", m.library)
	}
	// 回到真实的书，→ 只看章节（带 back），再 Enter → 阅读，Esc 两次回到书架。
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if len(m.library.rows) != 2 || m.library.back == nil || m.library.dir != dir {
		t.Fatalf("→ should open that book’s chapter list: %+v", m.library)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.library.chapter == 0 {
		t.Fatalf("Enter on a chapter should open the reader: %+v", m.library)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if len(m.library.rows) != 2 {
		t.Fatalf("Esc from reader should return to chapters: %+v", m.library)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if len(m.library.books) != 2 || m.library.cursor != 1 {
		t.Fatalf("Esc from chapters should return to the shelf: %+v", m.library)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.library != nil {
		t.Fatal("Esc on the shelf should close")
	}
}

func TestSidebarCapsWidthAndDetailAbsorbsIt(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 200
	if m.sidebarWidth() != sidebarMaxWidth {
		t.Fatalf("sidebar should cap at %d, got %d", sidebarMaxWidth, m.sidebarWidth())
	}
	if m.sidebarWidth()+m.eventFlowWidth()+m.detailWidth()+2 != m.width {
		t.Fatalf("columns must sum to width: %d + %d + %d + 2 != %d", m.sidebarWidth(), m.eventFlowWidth(), m.detailWidth(), m.width)
	}
	if m.detailWidth() <= m.width*25/100 {
		t.Fatalf("detail should absorb the sidebar's leftover: %d", m.detailWidth())
	}
	m.width = 120
	if m.sidebarWidth() != 30 || m.detailWidth() != 30 {
		t.Fatalf("at 120 cols both sides stay at 25%%: %d / %d", m.sidebarWidth(), m.detailWidth())
	}
}

func TestStreamShareAdjustsWithinBounds(t *testing.T) {
	m := NewModel(nil, "")
	m.width, m.height = 120, 40
	m.mode = modeRunning
	m.resizeTextarea()
	m.updateViewportSize()
	bodyH := m.bodyHeight()
	e0, s0 := m.splitHeights(bodyH)
	if e0 >= s0 {
		t.Fatalf("default split should favour the stream panel: event %d stream %d", e0, s0)
	}
	for i := 0; i < 5; i++ {
		next, _ := m.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlUp})
		m = next.(Model)
	}
	if m.streamSharePct() != maxStreamShare {
		t.Fatalf("share should clamp at %d, got %d", maxStreamShare, m.streamSharePct())
	}
	e1, s1 := m.splitHeights(bodyH)
	if s1 <= s0 || e1 >= e0 || e1+s1+1 != bodyH {
		t.Fatalf("Ctrl+↑ should grow stream: before %d/%d after %d/%d (body %d)", e0, s0, e1, s1, bodyH)
	}
	for i := 0; i < 10; i++ {
		next, _ := m.handleBaseKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlDown})
		m = next.(Model)
	}
	if m.streamSharePct() != minStreamShare {
		t.Fatalf("share should clamp at %d, got %d", minStreamShare, m.streamSharePct())
	}
	if m.streamVP.Height <= 0 || m.viewport.Height <= 0 {
		t.Fatalf("viewports must stay positive: event %d stream %d", m.viewport.Height, m.streamVP.Height)
	}
}

func TestEventTimestampIsMinuteResolution(t *testing.T) {
	out := ansi.Strip(renderEventLine(host.Event{
		Time: time.Date(2026, 8, 9, 12, 34, 56, 0, time.UTC), Category: "SYSTEM", Summary: "ok",
	}, 80, 0))
	if !strings.HasPrefix(out, "12:34 ") || strings.Contains(out, "12:34:56") {
		t.Fatalf("timestamp should be HH:MM, got %q", out)
	}
}
