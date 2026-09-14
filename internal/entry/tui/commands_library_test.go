package tui

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/library"
)

func TestLibraryCommandsAreRegistered(t *testing.T) {
	registry := commandRegistryInstance()
	for _, name := range []string{"books", "chapters", "read"} {
		spec, ok := registry.Find(name)
		if !ok {
			t.Fatalf("/%s should be registered", name)
		}
		if spec.NeedsIdle {
			t.Fatalf("/%s is read-only and must be usable while the engine runs", name)
		}
	}
	read, _ := registry.Find("read")
	if read.AutoExecute {
		t.Fatal("/read takes an argument and must not auto-execute")
	}
	items := builtinCommandItems()
	for _, name := range []string{"books", "chapters", "read"} {
		if !hasPaletteItem(items, name) {
			t.Fatalf("/%s missing from palette", name)
		}
	}
}

func TestRenderChaptersText(t *testing.T) {
	info := &library.BookInfo{Title: "光斑", Phase: "writing", Completed: 1, WordCount: 3000}
	list := []library.ChapterInfo{
		{Chapter: 1, Title: "雨夜归人", Status: library.StatusCompleted, WordCount: 3000},
		{Chapter: 2, Title: "破晓", Status: library.StatusInProgress},
		{Chapter: 3, Status: library.StatusPlanned, CoreEvent: "少年入城"},
	}
	out, _ := renderChaptersText(info, list, 80, -1, false)
	for _, want := range []string{"《光斑》", "写作中", "雨夜归人", "已完成", "3000 字", "破晓", "（无标题）", "少年入城", "/read"} {
		if !strings.Contains(out, want) {
			t.Fatalf("chapters text missing %q:\n%s", want, out)
		}
	}
}

func TestRenderChapterText_MarksDraft(t *testing.T) {
	out := renderChapterText(&library.ChapterView{Chapter: 3, Body: "第一段。\n\n第二段。", WordCount: 8, Draft: true}, 40)
	if !strings.Contains(out, "草稿") || !strings.Contains(out, "第一段。") || !strings.Contains(out, "第二段。") {
		t.Fatalf("unexpected chapter text:\n%s", out)
	}
}

func TestReportCommandsAreRegistered(t *testing.T) {
	registry := commandRegistryInstance()
	for _, name := range []string{"reviews", "foreshadow", "characters"} {
		spec, ok := registry.Find(name)
		if !ok {
			t.Fatalf("/%s should be registered", name)
		}
		if spec.NeedsIdle || !spec.AutoExecute {
			t.Fatalf("/%s is a read-only one-shot panel: %+v", name, spec)
		}
	}
}

func TestRenderReviewsText(t *testing.T) {
	info := &library.BookInfo{Title: "光斑"}
	reviews := []domain.ReviewEntry{
		{Chapter: 2, Scope: "arc", Verdict: "rewrite", Summary: "弧末塌了", AffectedChapters: []int{1, 2},
			Dimensions: []domain.DimensionScore{{Dimension: "节奏", Score: 41}},
			Issues:     []domain.ConsistencyIssue{{Severity: "critical", Description: "节奏失速", Suggestion: "砍掉过渡"}}},
	}
	out := renderReviewsText(info, reviews, 0, 80)
	for _, want := range []string{"《光斑》", "第 2 章", "弧", "需重写", "影响 [1 2]", "节奏 41", "弧末塌了", "[critical]", "节奏失速", "砍掉过渡"} {
		if !strings.Contains(out, want) {
			t.Fatalf("reviews text missing %q:\n%s", want, out)
		}
	}
	if empty := renderReviewsText(info, nil, 7, 80); !strings.Contains(empty, "第 7 章暂无评审记录") {
		t.Fatalf("empty chapter filter text wrong:\n%s", empty)
	}
}

func TestRenderForeshadowAndCharactersText(t *testing.T) {
	info := &library.BookInfo{Title: "光斑"}
	fs := &library.ForeshadowReport{Latest: 9,
		Open:     []library.ForeshadowItem{{ForeshadowEntry: domain.ForeshadowEntry{ID: "f-door", Description: "地窖暗门", PlantedAt: 1, Status: "planted"}, Age: 8}},
		Resolved: []library.ForeshadowItem{{ForeshadowEntry: domain.ForeshadowEntry{ID: "f-book", PlantedAt: 2, Status: "resolved", ResolvedAt: 5}, Age: 3}},
	}
	out := renderForeshadowText(info, fs, 80)
	for _, want := range []string{"未回收 1", "已回收 1", "f-door", "已过 8 章", "地窖暗门", "f-book", "收于 ch5"} {
		if !strings.Contains(out, want) {
			t.Fatalf("foreshadow text missing %q:\n%s", want, out)
		}
	}

	cs := &library.CharacterReport{Latest: 9,
		Core: []library.CharacterStat{
			{Character: domain.Character{Name: "阿禾", Aliases: []string{"杂役少年"}, Tier: "core", Role: "主角"}, Appearances: 7, FirstSeen: 1, LastSeen: 9,
				Snapshot: &domain.CharacterSnapshot{Volume: 1, Arc: 2, Status: "负伤", Power: "炼气三层"}},
			{Character: domain.Character{Name: "影子", Tier: "secondary"}},
		},
		Cast:          []domain.CastEntry{{Name: "赵捕头", BriefRole: "县衙捕头", AppearanceCount: 2, FirstSeenChapter: 3, LastSeenChapter: 8}},
		Relationships: []domain.RelationshipEntry{{CharacterA: "阿禾", CharacterB: "李掌柜", Relation: "雇佣", Chapter: 1}},
	}
	out = renderCharactersText(info, cs, 80)
	for _, want := range []string{"阿禾", "杂役少年", "核心", "出场 7 章", "最近 ch9", "负伤", "炼气三层", "影子", "尚未在任何章节摘要出现", "赵捕头", "县衙捕头", "阿禾 — 李掌柜", "雇佣"} {
		if !strings.Contains(out, want) {
			t.Fatalf("characters text missing %q:\n%s", want, out)
		}
	}
}

func TestRenderBooksText_MarksCurrent(t *testing.T) {
	books := []library.Book{
		{Info: &library.BookInfo{Title: "光斑", Phase: "complete", Completed: 12}},
		{Missing: true},
	}
	books[0].Dir = "/tmp/a/output/novel"
	books[1].Dir = "/tmp/gone"
	out := renderBooksText(books, "/tmp/a/output/novel", nil, 80, -1)
	for _, want := range []string{"光斑", "已完结", "← 当前", "目录已不存在"} {
		if !strings.Contains(out, want) {
			t.Fatalf("books text missing %q:\n%s", want, out)
		}
	}
}
