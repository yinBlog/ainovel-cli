package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newNavBook 建一本两章终稿 + 第三章草稿的书，返回小说目录。
func newNavBook(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "output", "novel")
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatal(err)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: "光斑", Synopsis: "简介"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	for ch := 1; ch <= 2; ch++ {
		if err := s.Drafts.SaveFinalChapter(ch, "正文"+strings.Repeat("字", ch)); err != nil {
			t.Fatal(err)
		}
		if err := s.Progress.StartChapter(ch); err != nil {
			t.Fatal(err)
		}
		if err := s.Progress.MarkChapterComplete(ch, 10, "cliff", "main"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 2, Title: "破晓"}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadPanelNavigatesBetweenChapters(t *testing.T) {
	dir := newNavBook(t)
	m := NewModel(nil, "")
	m.width, m.height = 120, 40
	m.library = newLibraryState(m.width, m.height, "第 1 章", func(int) string { return "x" })
	m.library.dir, m.library.chapter = dir, 1
	if !strings.Contains(m.library.hint(), "n/p") {
		t.Fatalf("chapter view should advertise n/p: %q", m.library.hint())
	}

	next, _ := m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if m.library.chapter != 2 || !strings.Contains(m.library.title, "破晓") {
		t.Fatalf("n should move to chapter 2 with its summary title, got %d %q", m.library.chapter, m.library.title)
	}
	if body := ansi.Strip(m.library.viewport.View()); !strings.Contains(body, "正文字字") {
		t.Fatalf("panel should show chapter 2 body:\n%s", body)
	}

	// 第 3 章没有终稿也没有草稿：保持第 2 章不动。
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.library.chapter != 2 {
		t.Fatalf("missing next chapter must not move, got %d", m.library.chapter)
	}
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(Model)
	if m.library.chapter != 1 {
		t.Fatalf("← should go back to chapter 1, got %d", m.library.chapter)
	}
	// 第 0 章不存在：停在第 1 章。
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = next.(Model)
	if m.library.chapter != 1 {
		t.Fatalf("p below chapter 1 must not move, got %d", m.library.chapter)
	}
	// 非章节面板（如书架）不响应翻章键。
	m.library = newLibraryState(m.width, m.height, "书架", func(int) string { return "x" })
	next, _ = m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if m.library.chapter != 0 || strings.Contains(m.library.hint(), "n/p") {
		t.Fatalf("non-chapter panel must ignore n/p: %+v", m.library)
	}
}

func TestTopBarShowsProgressNotModel(t *testing.T) {
	out := ansi.Strip(renderTopBar(host.UISnapshot{
		Provider: "openrouter", ModelName: "test-model", BookTitle: "光斑",
		CompletedCount: 12, TotalChapters: 40, TotalWordCount: 48200, Style: "fantasy",
	}, 120, "", "v1.2.3"))
	for _, want := range []string{"ainovel-cli v1.2.3", "12/40 章", "48,200 字", "fantasy", "光斑"} {
		if !strings.Contains(out, want) {
			t.Fatalf("top bar missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "test-model") || strings.Contains(out, "openrouter") {
		t.Fatalf("top bar should leave model identity to the status bar: %q", out)
	}
}

func TestSidebarAgentNameCollapsesArchitectVariants(t *testing.T) {
	for _, in := range []string{"architect_long", "architect_short", "architect"} {
		if got := sidebarAgentName(in); got != "ARCHITECT" {
			t.Fatalf("sidebarAgentName(%q) = %q", in, got)
		}
	}
	if got := sidebarAgentName("writer"); got != "WRITER" {
		t.Fatalf("sidebarAgentName(writer) = %q", got)
	}
}
