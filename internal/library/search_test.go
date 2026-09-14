package library

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newSearchBook 在标准测试书之上补一些可检索的内容：正文、摘要、大纲各埋一个词。
func newSearchBook(t *testing.T) string {
	t.Helper()
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	if err := s.Drafts.SaveFinalChapter(1, "青锋剑出鞘时，雨还没停。少年握紧了青锋剑。"); err != nil {
		t.Fatalf("save chapter 1: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, "他把剑收回鞘中，转身离开。"); err != nil {
		t.Fatalf("save chapter 2: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter: 2, Title: "离城", Summary: "少年带着青锋剑离开。",
		KeyEvents: []string{"青锋剑第一次饮血"},
	}); err != nil {
		t.Fatalf("save summary: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "雨夜", CoreEvent: "得到青锋剑"},
		{Chapter: 2, Title: "离城"},
		{Chapter: 3, Title: "大纲三"},
		{Chapter: 4, Title: "大纲四"},
	}); err != nil {
		t.Fatalf("save outline: %v", err)
	}
	return bookDir
}

// 正文、大纲、摘要都要能搜到；同一段里出现两次就该给两条命中——
// 查“这个道具出现过几次”时次数本身就是答案。
func TestSearchCoversEverySource(t *testing.T) {
	dir := newSearchBook(t)
	info, result, err := Search(dir, SearchOptions{Query: "青锋剑"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if info == nil || info.Title != "光斑" {
		t.Fatalf("应带回书籍概览：%+v", info)
	}
	bySource := map[SearchSource]int{}
	for _, hit := range result.Hits {
		bySource[hit.Source]++
	}
	if bySource[SourceChapter] != 2 {
		t.Fatalf("第 1 章正文里出现两次，应给两条命中：%+v", result.Hits)
	}
	if bySource[SourceOutline] != 1 {
		t.Fatalf("大纲核心事件应命中一次：%+v", result.Hits)
	}
	if bySource[SourceSummary] != 2 {
		t.Fatalf("摘要正文与关键事件各命中一次：%+v", result.Hits)
	}
}

// 命中按章升序、同章大纲→摘要→正文：先看这章是干什么的，再看细节。
func TestSearchOrdersByChapterThenSource(t *testing.T) {
	dir := newSearchBook(t)
	_, result, err := Search(dir, SearchOptions{Query: "青锋剑"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	lastChapter, lastRank := 0, -1
	rank := map[SearchSource]int{SourceOutline: 0, SourceSummary: 1, SourceChapter: 2, SourceDraft: 3}
	for _, hit := range result.Hits {
		if hit.Chapter < lastChapter {
			t.Fatalf("章号应升序：%+v", result.Hits)
		}
		if hit.Chapter == lastChapter && rank[hit.Source] < lastRank {
			t.Fatalf("同章应按 大纲→摘要→正文：%+v", result.Hits)
		}
		lastChapter, lastRank = hit.Chapter, rank[hit.Source]
	}
}

// MatchAt/MatchLen 是给 TUI 高亮用的：必须精确指向摘录里的关键词，
// 含省略号前缀时也要对得上，否则高亮会错位。
func TestSearchExcerptHighlightIsExact(t *testing.T) {
	dir := newSearchBook(t)
	_, result, err := Search(dir, SearchOptions{Query: "青锋剑", Context: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Hits) == 0 {
		t.Fatal("应有命中")
	}
	for _, hit := range result.Hits {
		runes := []rune(hit.Excerpt)
		if hit.MatchAt < 0 || hit.MatchAt+hit.MatchLen > len(runes) {
			t.Fatalf("高亮区间越界：%+v", hit)
		}
		if got := string(runes[hit.MatchAt : hit.MatchAt+hit.MatchLen]); got != "青锋剑" {
			t.Fatalf("高亮位置指向 %q，应是关键词：%+v", got, hit)
		}
	}
}

func TestSearchExcerptTrimsAndMarksTruncation(t *testing.T) {
	runes := []rune("　　少年握紧了青锋剑，转身。")
	at := 7 // “青”
	excerpt, matchAt := excerptAround(runes, at, 3, 3)
	if strings.HasPrefix(excerpt, "　") {
		t.Fatalf("摘录左端的全角空白应被收掉：%q", excerpt)
	}
	if !strings.HasPrefix(excerpt, "…") {
		t.Fatalf("左侧被截断应补省略号：%q", excerpt)
	}
	got := []rune(excerpt)
	if string(got[matchAt:matchAt+3]) != "青锋剑" {
		t.Fatalf("收掉空白后下标应仍然精确：%q@%d", excerpt, matchAt)
	}
}

// 大小写：默认折叠，显式要求区分时必须真的区分。
func TestSearchCaseSensitivity(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	if err := s.Drafts.SaveFinalChapter(1, "He called it Excalibur, not excalibur."); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, folded, err := Search(bookDir, SearchOptions{Query: "excalibur", Scope: ScopeChapters})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(folded.Hits) != 2 {
		t.Fatalf("默认应大小写不敏感，命中 2 次，得到 %d", len(folded.Hits))
	}
	_, exact, err := Search(bookDir, SearchOptions{Query: "excalibur", Scope: ScopeChapters, CaseSensitive: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(exact.Hits) != 1 {
		t.Fatalf("区分大小写时只应命中 1 次，得到 %d", len(exact.Hits))
	}
}

// 命中很多时要截断并明说，不能让用户以为只有这些。
func TestSearchTruncatesAndReports(t *testing.T) {
	dir := newSearchBook(t)
	_, result, err := Search(dir, SearchOptions{Query: "青锋剑", Limit: 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Hits) != 2 || !result.Truncated {
		t.Fatalf("应截断到 2 条并标记：%d 条 truncated=%v", len(result.Hits), result.Truncated)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	dir := newSearchBook(t)
	if _, _, err := Search(dir, SearchOptions{Query: "   "}); err == nil {
		t.Fatal("空检索词应报错，而不是把全书倒出来")
	}
}

// 摘录必须是一行：正文里有换行和 markdown 标题，原样截出来会让一条命中渲染成
// 好几行，把按固定行高滚动的列表整个错开。
func TestSearchExcerptIsSingleLine(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	if err := s.Drafts.SaveFinalChapter(1, "# 第1章 旧变频器\n\n“签了吧。”\n\n他拆检旧变频器，\n完成放电。"); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, result, err := Search(bookDir, SearchOptions{Query: "变频器", Scope: ScopeChapters})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Hits) == 0 {
		t.Fatal("应有命中")
	}
	for _, hit := range result.Hits {
		if strings.ContainsAny(hit.Excerpt, "\n\r\t") {
			t.Fatalf("摘录里不能有换行：%q", hit.Excerpt)
		}
		runes := []rune(hit.Excerpt)
		if got := string(runes[hit.MatchAt : hit.MatchAt+hit.MatchLen]); got != "变频器" {
			t.Fatalf("压缩空白后高亮下标应仍然精确，指到了 %q：%q", got, hit.Excerpt)
		}
	}
}

// 空白压成空格而不是删掉：英文里删掉会把词挤在一起。
func TestSearchExcerptKeepsWordBoundaries(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	if err := s.Drafts.SaveFinalChapter(1, "the   cat\n\nsat on the mat"); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, result, err := Search(bookDir, SearchOptions{Query: "cat", Scope: ScopeChapters})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("应有 1 条命中：%+v", result.Hits)
	}
	if got := result.Hits[0].Excerpt; got != "the cat sat on the mat" {
		t.Fatalf("空白应压成单个空格而不是删掉，得到 %q", got)
	}
	runes := []rune(result.Hits[0].Excerpt)
	hit := result.Hits[0]
	if string(runes[hit.MatchAt:hit.MatchAt+hit.MatchLen]) != "cat" {
		t.Fatalf("高亮下标错位：%q@%d", hit.Excerpt, hit.MatchAt)
	}
}
