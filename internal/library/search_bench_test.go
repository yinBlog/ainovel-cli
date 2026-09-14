package library

import (
	"fmt"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newBigBook 造一本 200 章、每章约 3000 字的书，逼近真实长篇的检索规模。
func newBigBook(tb testing.TB, chapters int) string {
	tb.Helper()
	_, bookDir := newBook(tb)
	s := store.NewStore(bookDir)
	body := strings.Repeat("他握紧手里的东西，风从巷口灌进来，雨点砸在铁皮棚上。", 60)
	outline := make([]domain.OutlineEntry, 0, chapters)
	for ch := 1; ch <= chapters; ch++ {
		text := body
		if ch%20 == 0 {
			text += "青锋剑在鞘中震了一下。"
		}
		if err := s.Drafts.SaveFinalChapter(ch, text); err != nil {
			tb.Fatalf("save chapter %d: %v", ch, err)
		}
		if err := s.Progress.StartChapter(ch); err != nil {
			tb.Fatalf("start %d: %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, len([]rune(text)), "cliff", "main"); err != nil {
			tb.Fatalf("complete %d: %v", ch, err)
		}
		outline = append(outline, domain.OutlineEntry{Chapter: ch, Title: fmt.Sprintf("第 %d 章", ch)})
	}
	if err := s.Outline.SaveOutline(outline); err != nil {
		tb.Fatalf("save outline: %v", err)
	}
	return bookDir
}

// 检索是主功能，规模上来之后必须还能秒回。这个基准锁住数量级——
// 真正的陷阱不是扫文本，而是逐章去调 GetChapterOutline（每次重读整份大纲）
// 和为没命中的章解析标题。
func BenchmarkSearchWholeBook(b *testing.B) {
	dir := newBigBook(b, 200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Search(dir, SearchOptions{Query: "青锋剑"}); err != nil {
			b.Fatalf("search: %v", err)
		}
	}
}
