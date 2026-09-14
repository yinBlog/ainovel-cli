package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newBook 在 t.TempDir() 之上建一本"写到第 2 章、第 3 章草稿中、第 4 章仅有大纲"的书，
// 返回启动目录（含 output/novel）。
func newBook(t testing.TB) (launchDir, bookDir string) {
	t.Helper()
	launchDir = t.TempDir()
	bookDir = filepath.Join(launchDir, "output", "novel")
	s := store.NewStore(bookDir)
	if err := s.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatalf("init progress: %v", err)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: "光斑", Synopsis: "一段简介。"}); err != nil {
		t.Fatalf("save book: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "大纲一"},
		{Chapter: 2, Title: "大纲二"},
		{Chapter: 3, Title: "大纲三"},
		{Chapter: 4, Title: "大纲四", CoreEvent: "少年入城"},
	}); err != nil {
		t.Fatalf("save outline: %v", err)
	}
	for ch := 1; ch <= 2; ch++ {
		if err := s.Drafts.SaveFinalChapter(ch, "第"+string(rune('0'+ch))+"章正文。\n\n第二段。"); err != nil {
			t.Fatalf("save chapter %d: %v", ch, err)
		}
		if err := s.Progress.StartChapter(ch); err != nil {
			t.Fatalf("start chapter %d: %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, 100*ch, "cliff", "main"); err != nil {
			t.Fatalf("mark complete %d: %v", ch, err)
		}
	}
	// 第 1 章摘要给出真实标题，应优先于大纲标题。
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "雨夜归人"}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	if err := s.Progress.StartChapter(3); err != nil {
		t.Fatalf("start chapter 3: %v", err)
	}
	if err := s.Drafts.SaveDraft(3, "第三章草稿。"); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	return launchDir, bookDir
}

func TestResolveBookDir(t *testing.T) {
	launchDir, bookDir := newBook(t)
	for _, in := range []string{launchDir, bookDir} {
		got, err := ResolveBookDir(in)
		if err != nil {
			t.Fatalf("ResolveBookDir(%q): %v", in, err)
		}
		if got != bookDir {
			t.Fatalf("ResolveBookDir(%q) = %q, want %q", in, got, bookDir)
		}
	}
	if _, err := ResolveBookDir(t.TempDir()); !errors.Is(err, ErrNotBook) {
		t.Fatalf("empty dir should be ErrNotBook, got %v", err)
	}
}

func TestInspect(t *testing.T) {
	_, bookDir := newBook(t)
	info, err := Inspect(bookDir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Title != "光斑" || info.Completed != 2 || info.WordCount != 300 || info.Phase != domain.PhaseWriting || info.InProgress != 3 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if _, err := Inspect(t.TempDir()); !errors.Is(err, ErrNotBook) {
		t.Fatalf("empty dir should be ErrNotBook, got %v", err)
	}
}

func TestChapters(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	if err := s.Progress.SetPendingRewrites([]int{2}, "test"); err != nil {
		t.Fatalf("pending rewrites: %v", err)
	}
	info, list, err := Chapters(bookDir)
	if err != nil {
		t.Fatalf("Chapters: %v", err)
	}
	if info.Title != "光斑" {
		t.Fatalf("info title = %q", info.Title)
	}
	want := []ChapterInfo{
		{Chapter: 1, Title: "雨夜归人", Status: StatusCompleted, WordCount: 100},
		{Chapter: 2, Title: "大纲二", Status: StatusPendingRework, WordCount: 200},
		{Chapter: 3, Title: "大纲三", Status: StatusInProgress},
		{Chapter: 4, Title: "大纲四", Status: StatusPlanned, CoreEvent: "少年入城"},
	}
	if len(list) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(list), len(want), list)
	}
	for i := range want {
		if list[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, list[i], want[i])
		}
	}
}

func TestReadChapter(t *testing.T) {
	_, bookDir := newBook(t)
	final, err := ReadChapter(bookDir, 1)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	if final.Draft || final.Title != "雨夜归人" || final.WordCount == 0 || final.Body == "" {
		t.Fatalf("unexpected final view: %+v", final)
	}
	draft, err := ReadChapter(bookDir, 3)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if !draft.Draft || draft.Title != "大纲三" || draft.Body != "第三章草稿。" {
		t.Fatalf("unexpected draft view: %+v", draft)
	}
	if _, err := ReadChapter(bookDir, 4); !errors.Is(err, ErrChapterNotFound) {
		t.Fatalf("planned chapter should be ErrChapterNotFound, got %v", err)
	}
	if _, err := ReadChapter(bookDir, 0); err == nil {
		t.Fatal("chapter 0 should error")
	}
}

func TestRegistry_RecordListForget(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "books.json")
	reg := NewRegistry(regPath)

	_, bookDir := newBook(t)
	emptyDir := t.TempDir() // 登记了但没开书
	missingDir := filepath.Join(t.TempDir(), "gone")

	for _, d := range []string{missingDir, emptyDir, bookDir} {
		if err := reg.Record(d); err != nil {
			t.Fatalf("record %s: %v", d, err)
		}
		time.Sleep(2 * time.Millisecond) // 让 last_opened 单调递增
	}
	// 重复登记同一目录只刷新时间，不新增条目。
	if err := reg.Record(bookDir); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	entries, err := reg.Entries()
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 3 || entries[0].Dir != bookDir {
		t.Fatalf("entries should be 3 with most recent first, got %+v", entries)
	}

	books, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(books) != 3 {
		t.Fatalf("got %d books", len(books))
	}
	if books[0].Info == nil || books[0].Info.Title != "光斑" || books[0].Missing {
		t.Fatalf("book entry wrong: %+v", books[0])
	}
	if books[1].Info != nil || books[1].Missing || books[1].Err != nil {
		t.Fatalf("empty dir should be not-yet-started: %+v", books[1])
	}
	if !books[2].Missing {
		t.Fatalf("gone dir should be Missing: %+v", books[2])
	}

	if err := reg.Forget(emptyDir); err != nil {
		t.Fatalf("forget: %v", err)
	}
	entries, _ = reg.Entries()
	if len(entries) != 2 {
		t.Fatalf("after forget expected 2 entries, got %+v", entries)
	}
	if _, err := os.Stat(regPath); err != nil {
		t.Fatalf("registry file should exist: %v", err)
	}
}

func TestRegistry_EmptyPathIsNoop(t *testing.T) {
	reg := DefaultRegistry("")
	if err := reg.Record(t.TempDir()); err != nil {
		t.Fatalf("noop record: %v", err)
	}
	books, err := reg.List()
	if err != nil || len(books) != 0 {
		t.Fatalf("noop list = %v, %v", books, err)
	}
}

func TestRegistry_CorruptFile(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "books.json")
	if err := os.WriteFile(regPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(regPath).List(); err == nil {
		t.Fatal("corrupt registry should surface an error, not silently reset")
	}
}
