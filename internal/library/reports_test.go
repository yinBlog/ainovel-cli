package library

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func seedReports(t *testing.T, bookDir string) {
	t.Helper()
	s := store.NewStore(bookDir)
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept", Summary: "首章稳",
		Dimensions: []domain.DimensionScore{{Dimension: "节奏", Score: 82}}}); err != nil {
		t.Fatalf("save review 1: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "rewrite", Summary: "弧末塌了",
		AffectedChapters: []int{1, 2},
		Issues:           []domain.ConsistencyIssue{{Type: "pacing", Severity: "critical", Description: "节奏失速"}}}); err != nil {
		t.Fatalf("save review 2: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "global", Verdict: "polish", Summary: "全局尚可"}); err != nil {
		t.Fatalf("save global review: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "f-old", Description: "药铺地窖的暗门", PlantedAt: 1, Status: "planted"},
		{ID: "f-new", Description: "掌柜的旧伤", PlantedAt: 2, Status: "advanced"},
		{ID: "f-done", Description: "失踪的账本", PlantedAt: 1, Status: "resolved", ResolvedAt: 2},
	}); err != nil {
		t.Fatalf("save foreshadow: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "阿禾", Aliases: []string{"杂役少年"}, Role: "主角", Tier: "core"},
		{Name: "李掌柜", Role: "药铺掌柜", Tier: "important"},
		{Name: "影子", Role: "反派", Tier: "secondary"},
	}); err != nil {
		t.Fatalf("save characters: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 2, Title: "破晓", Characters: []string{"杂役少年", "李掌柜"}}); err != nil {
		t.Fatalf("save summary 2: %v", err)
	}
	// 第 1 章摘要已由 newBook 写入（无出场名单）；补上出场名单让阿禾出现两章。
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "雨夜归人", Characters: []string{"阿禾"}}); err != nil {
		t.Fatalf("save summary 1: %v", err)
	}
	// 配角名册不再持久化，由接纳记录派生（上游 c7cfdf5）：种子数据改成落章节记录，
	// 让 ProjectCast 自己算出更夫和赵捕头。
	acceptCast := func(chapter int, names []string, intros []domain.CastIntro) {
		t.Helper()
		if _, err := s.ChapterRecords.Accept(chapter, domain.ChapterOriginGenerated, "正文",
			domain.ChapterFacts{Characters: names, CastIntros: intros}, domain.StyleDelta{}); err != nil {
			t.Fatalf("accept ch%d: %v", chapter, err)
		}
	}
	acceptCast(1, []string{"阿禾", "更夫"}, nil)
	acceptCast(2, []string{"阿禾", "赵捕头"}, []domain.CastIntro{{Name: "赵捕头", BriefRole: "县衙捕头"}})
	if err := s.World.SaveRelationships([]domain.RelationshipEntry{{CharacterA: "阿禾", CharacterB: "李掌柜", Relation: "雇佣", Chapter: 1}}); err != nil {
		t.Fatalf("save relationships: %v", err)
	}
}

func TestReviews(t *testing.T) {
	_, bookDir := newBook(t)
	seedReports(t, bookDir)

	_, all, err := Reviews(bookDir, 0)
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 reviews, got %d: %+v", len(all), all)
	}
	// 章号升序，同章 arc 在 global 前。
	if all[0].Chapter != 1 || all[1].Scope != "arc" || all[2].Scope != "global" {
		t.Fatalf("unexpected order: %+v", all)
	}

	_, ch1, err := Reviews(bookDir, 1)
	if err != nil {
		t.Fatalf("Reviews(1): %v", err)
	}
	// 第 1 章自己的评审 + 把第 1 章列入返工的弧评审。
	if len(ch1) != 2 || ch1[0].Scope != "chapter" || ch1[1].Scope != "arc" {
		t.Fatalf("chapter filter wrong: %+v", ch1)
	}

	_, none, err := Reviews(t.TempDir(), 0)
	if err == nil || none != nil {
		t.Fatal("non-book dir should fail")
	}
}

func TestReviews_EmptyDirIsNotError(t *testing.T) {
	_, bookDir := newBook(t)
	_, all, err := Reviews(bookDir, 0)
	if err != nil || len(all) != 0 {
		t.Fatalf("no reviews yet should be empty, got %v %v", all, err)
	}
}

func TestForeshadow(t *testing.T) {
	_, bookDir := newBook(t) // 已完成 1、2 章 → latest=2
	seedReports(t, bookDir)
	_, rep, err := Foreshadow(bookDir)
	if err != nil {
		t.Fatalf("Foreshadow: %v", err)
	}
	if rep.Latest != 2 {
		t.Fatalf("latest = %d", rep.Latest)
	}
	if len(rep.Open) != 2 || rep.Open[0].ID != "f-old" || rep.Open[0].Age != 1 || rep.Open[1].Age != 0 {
		t.Fatalf("open wrong: %+v", rep.Open)
	}
	if len(rep.Resolved) != 1 || rep.Resolved[0].ID != "f-done" || rep.Resolved[0].Age != 1 {
		t.Fatalf("resolved wrong: %+v", rep.Resolved)
	}
}

func TestCharacters(t *testing.T) {
	_, bookDir := newBook(t)
	seedReports(t, bookDir)
	_, rep, err := Characters(bookDir)
	if err != nil {
		t.Fatalf("Characters: %v", err)
	}
	if len(rep.Core) != 3 {
		t.Fatalf("want 3 core, got %+v", rep.Core)
	}
	// tier 排序：core → important → secondary。
	if rep.Core[0].Name != "阿禾" || rep.Core[1].Name != "李掌柜" || rep.Core[2].Name != "影子" {
		t.Fatalf("tier order wrong: %+v", rep.Core)
	}
	// 阿禾按本名出现在第 1 章、按别名出现在第 2 章。
	if a := rep.Core[0]; a.Appearances != 2 || a.FirstSeen != 1 || a.LastSeen != 2 {
		t.Fatalf("阿禾 stats wrong: %+v", a)
	}
	if b := rep.Core[1]; b.Appearances != 1 || b.LastSeen != 2 {
		t.Fatalf("李掌柜 stats wrong: %+v", b)
	}
	if c := rep.Core[2]; c.Appearances != 0 || c.LastSeen != 0 {
		t.Fatalf("影子 should be unseen: %+v", c)
	}
	if len(rep.Cast) != 2 || rep.Cast[0].Name != "赵捕头" {
		t.Fatalf("cast should be sorted by last seen desc: %+v", rep.Cast)
	}
	if len(rep.Relationships) != 1 || rep.Latest != 2 {
		t.Fatalf("relations/latest wrong: %+v %d", rep.Relationships, rep.Latest)
	}
}
