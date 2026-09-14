package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// 时间回跳的判定必须窄：跨章连场（连续同一时间）是长篇里最常见的正常写法，
// 标它只会把真正的问题淹掉；被别的时间隔开后又回来的才是回跳。
func TestTimelineFlagsOnlyRealRevisits(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	if err := s.World.SaveTimeline([]domain.TimelineEvent{
		{Chapter: 1, Time: "第一日清晨", Event: "启程"},
		{Chapter: 1, Time: "第一日正午", Event: "遇袭"},
		{Chapter: 2, Time: "第一日正午", Event: "脱身"}, // 跨章连场：不该标
		{Chapter: 2, Time: "第一日傍晚", Event: "投宿"},
		{Chapter: 3, Time: "第一日正午", Event: "回想"}, // 隔了傍晚又回来：该标
	}); err != nil {
		t.Fatalf("save timeline: %v", err)
	}

	_, rows, err := Timeline(bookDir)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("应有 5 条，得到 %d", len(rows))
	}
	for i, want := range []bool{false, false, false, false, true} {
		if rows[i].Revisited != want {
			t.Fatalf("第 %d 条 Revisited=%v，期望 %v（%+v）", i, rows[i].Revisited, want, rows[i].TimelineEvent)
		}
	}
}

func TestTimelineIsEmptyForFreshBook(t *testing.T) {
	_, bookDir := newBook(t)
	info, rows, err := Timeline(bookDir)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if info == nil || info.Title != "光斑" {
		t.Fatalf("应带回书籍概览：%+v", info)
	}
	if len(rows) != 0 {
		t.Fatalf("空时间线应返回 0 条，得到 %d", len(rows))
	}
}

// 违规台账只列"当前仍存在的"：同章追加式写入以最后一条为准，返工后清空的章要消失。
func TestViolationsKeepsLatestPerChapterAndDropsCleared(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	write := func(chapter int, vs ...rules.Violation) {
		t.Helper()
		if err := s.World.SaveRuleViolations(chapter, vs); err != nil {
			t.Fatalf("save violations ch%d: %v", chapter, err)
		}
	}
	write(2, rules.Violation{Rule: "forbidden_phrases", Target: "不禁", Severity: rules.SeverityWarning})
	write(1, rules.Violation{Rule: "fatigue_words", Target: "忽然", Severity: rules.SeverityWarning})
	// 第 1 章返工后重写，只剩一条别的问题——以最后一条为准。
	write(1, rules.Violation{Rule: "forbidden_chars", Target: "囧", Severity: rules.SeverityError})
	// 第 2 章返工后清干净了，不该再占版面。
	write(2)

	_, rows, err := Violations(bookDir)
	if err != nil {
		t.Fatalf("violations: %v", err)
	}
	if len(rows) != 1 || rows[0].Chapter != 1 {
		t.Fatalf("清空的章应消失、只剩第 1 章：%+v", rows)
	}
	if len(rows[0].Violations) != 1 || rows[0].Violations[0].Target != "囧" {
		t.Fatalf("同章应以最后一条为准：%+v", rows[0].Violations)
	}
	if rows[0].At == "" {
		t.Fatal("应带上记录时间")
	}
}

func TestViolationsIsEmptyForCleanBook(t *testing.T) {
	_, bookDir := newBook(t)
	_, rows, err := Violations(bookDir)
	if err != nil {
		t.Fatalf("violations: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("没有违规记录时应返回 0 条，得到 %d", len(rows))
	}
}

// 书架的花费与活跃度：没写过的书留零值，不该让整行报错。
func TestLoadActivityOnFreshBook(t *testing.T) {
	_, bookDir := newBook(t)
	a := LoadActivity(bookDir)
	if a.CostUSD != 0 {
		t.Fatalf("还没花过钱：%v", a.CostUSD)
	}
	// newBook 写了第 1、2 章终稿，文件是刚建的，应落在最近窗口里。
	if a.RecentChapters != 2 {
		t.Fatalf("近七日应有 2 章，得到 %d", a.RecentChapters)
	}
	idle, ok := a.Idle()
	if !ok {
		t.Fatal("写过章节就该有最近写作时间")
	}
	if idle > recentWindow {
		t.Fatalf("刚写的书不该显示为长期未动：%v", idle)
	}
}

// 返工会把早期章节的文件时间改新。倒序"走到第一个超窗口的章就停"会把那次返工整个
// 漏掉——这里把第 1 章做成刚返工过、第 2 章做成很旧，统计必须仍然看见第 1 章。
func TestChapterActivityCountsReworkedEarlyChapters(t *testing.T) {
	_, bookDir := newBook(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(chapterFilePath(bookDir, 2), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// 第 1 章保持刚写的时间（newBook 刚建的），第 2 章改成一个月前。
	newest, recent := chapterActivity(bookDir, []int{1, 2})
	if recent != 1 {
		t.Fatalf("第 1 章在窗口内、第 2 章不在，应记 1 章，得到 %d", recent)
	}
	if newest.Before(time.Now().Add(-time.Hour)) {
		t.Fatalf("最新章节时间应是刚才，得到 %v", newest)
	}
}

// 最近一次活动优先取用量落盘时间：它覆盖评审、返工等不产出新章的活动。
func TestLoadActivityPrefersUsageTimestamp(t *testing.T) {
	_, bookDir := newBook(t)
	s := store.NewStore(bookDir)
	stamp := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := s.Usage.Save(domain.UsageState{
		Schema: 1, UpdatedAt: stamp,
		Overall: domain.AgentUsageTotals{Cost: 1.25},
	}); err != nil {
		t.Fatalf("save usage: %v", err)
	}
	a := LoadActivity(bookDir)
	if a.CostUSD != 1.25 {
		t.Fatalf("花费 = %v", a.CostUSD)
	}
	if !a.LastWrite.Equal(stamp) {
		t.Fatalf("最近活动应取用量时间 %v，得到 %v", stamp, a.LastWrite)
	}
}

// 读一章时要能直接看到它在全书里的位置、状态和 Editor 判定，
// 否则读完还得另开 /chapters 和 /reviews 各看一次。
func TestReadChapterCarriesPositionStatusAndReview(t *testing.T) {
	_, bookDir := newBook(t)
	// 评审是一个目录下的独立 JSON，直接落一份即可——只读层不该依赖写入路径。
	reviewDir := filepath.Join(bookDir, "reviews")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", Verdict: "polish",
		Summary: "中段节奏偏拖。",
		Issues:  []domain.ConsistencyIssue{{Severity: "error", Description: "对话缺少动机"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reviewDir, "ch001.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := ReadChapter(bookDir, 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if view.Total != 4 {
		t.Fatalf("应带上全书章数（大纲 4 条），得到 %d", view.Total)
	}
	if view.Status != StatusCompleted {
		t.Fatalf("第 1 章已完成，状态 = %q", view.Status)
	}
	if view.Review == nil || view.Review.Verdict != "polish" || view.Review.Issues != 1 {
		t.Fatalf("应带上该章评审：%+v", view.Review)
	}
}

// 没有评审记录时不能因此读不出正文——阅读面板缺提示只是少几行。
func TestReadChapterWithoutReviewStillWorks(t *testing.T) {
	_, bookDir := newBook(t)
	view, err := ReadChapter(bookDir, 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if view.Review != nil {
		t.Fatalf("没有评审就该是 nil：%+v", view.Review)
	}
	if view.Body == "" {
		t.Fatal("正文不该因为缺评审而读不出来")
	}
	if view.Status != StatusCompleted || view.Total != 4 {
		t.Fatalf("位置与状态仍应填上：status=%q total=%d", view.Status, view.Total)
	}
}
