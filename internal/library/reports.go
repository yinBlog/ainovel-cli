package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ── 评审 ──

// Reviews 返回一本书全部评审记录（章节 / 弧 / 全局），按章号升序、同章 chapter 在前。
// chapter > 0 时只返回落在该章的评审，以及把该章列入 AffectedChapters 的弧/全局评审。
func Reviews(dir string, chapter int) (*BookInfo, []domain.ReviewEntry, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	all, err := loadAllReviews(s)
	if err != nil {
		return nil, nil, err
	}
	if chapter > 0 {
		filtered := all[:0:0]
		for _, r := range all {
			if r.Chapter == chapter || containsInt(r.AffectedChapters, chapter) {
				filtered = append(filtered, r)
			}
		}
		all = filtered
	}
	return info, all, nil
}

func loadAllReviews(s *store.Store) ([]domain.ReviewEntry, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir(), "reviews"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 reviews 目录失败：%w", err)
	}
	var reviews []domain.ReviewEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir(), "reviews", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取 reviews/%s 失败：%w", e.Name(), err)
		}
		var r domain.ReviewEntry
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("解析 reviews/%s 失败：%w", e.Name(), err)
		}
		reviews = append(reviews, r)
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if reviews[i].Chapter != reviews[j].Chapter {
			return reviews[i].Chapter < reviews[j].Chapter
		}
		return scopeRank(reviews[i].Scope) < scopeRank(reviews[j].Scope)
	})
	return reviews, nil
}

func scopeRank(scope string) int {
	switch scope {
	case "chapter":
		return 0
	case "arc":
		return 1
	case "global":
		return 2
	}
	return 3
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ── 伏笔 ──

// ForeshadowItem 是伏笔台账的一行，附带"距最新已完成章多少章"的年龄。
type ForeshadowItem struct {
	domain.ForeshadowEntry
	Age int // 未回收：最新已完成章 - 埋设章；已回收：回收章 - 埋设章
}

// ForeshadowReport 是伏笔视图：未回收按年龄倒序（越久未动越靠前），已回收按回收章升序。
type ForeshadowReport struct {
	Open     []ForeshadowItem
	Resolved []ForeshadowItem
	Latest   int // 最新已完成章号，年龄的参照点
}

// Foreshadow 读取伏笔台账并按状态分组。是否"停滞"的裁定留给 /diag，这里只报年龄。
func Foreshadow(dir string) (*BookInfo, *ForeshadowReport, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	ledger, err := s.World.LoadForeshadowLedger()
	if err != nil {
		return nil, nil, fmt.Errorf("读取伏笔台账失败：%w", err)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("读取进度失败：%w", err)
	}
	latest := 0
	if progress != nil {
		for _, ch := range progress.CompletedChapters {
			if ch > latest {
				latest = ch
			}
		}
	}
	report := &ForeshadowReport{Latest: latest}
	for _, e := range ledger {
		item := ForeshadowItem{ForeshadowEntry: e}
		if e.Status == "resolved" {
			if e.ResolvedAt > 0 {
				item.Age = e.ResolvedAt - e.PlantedAt
			}
			report.Resolved = append(report.Resolved, item)
			continue
		}
		item.Age = latest - e.PlantedAt
		report.Open = append(report.Open, item)
	}
	sort.SliceStable(report.Open, func(i, j int) bool {
		if report.Open[i].Age != report.Open[j].Age {
			return report.Open[i].Age > report.Open[j].Age
		}
		return report.Open[i].PlantedAt < report.Open[j].PlantedAt
	})
	sort.SliceStable(report.Resolved, func(i, j int) bool {
		return report.Resolved[i].ResolvedAt < report.Resolved[j].ResolvedAt
	})
	return info, report, nil
}

// ── 角色 ──

// CharacterStat 是核心角色档案 + 从章节摘要统计出的出场情况 + 最新状态快照。
type CharacterStat struct {
	domain.Character
	Appearances int // 在多少个已完成章的摘要里出现（按名字或别名）
	FirstSeen   int
	LastSeen    int
	Snapshot    *domain.CharacterSnapshot // 最近一次弧末快照，可能为 nil
}

// CharacterReport 是角色视图：核心档案、配角名册、人物关系。
type CharacterReport struct {
	Core          []CharacterStat
	Cast          []domain.CastEntry // 按最近出场倒序
	Relationships []domain.RelationshipEntry
	Latest        int // 最新已完成章号，缺席章数的参照点
}

// Characters 读取角色档案、配角名册、关系与最新快照，并用章节摘要统计出场。
// 出场统计只是计数；"角色消失"之类的裁定留给 /diag。
func Characters(dir string) (*BookInfo, *CharacterReport, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	chars, err := s.Characters.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("读取角色档案失败：%w", err)
	}
	relations, err := s.World.LoadRelationships()
	if err != nil {
		return nil, nil, fmt.Errorf("读取人物关系失败：%w", err)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("读取进度失败：%w", err)
	}
	// 配角名册不再持久化，改由接纳记录派生（上游 c7cfdf5「派生状态投影」）。
	// 这里跟 Host 走同一条路径，避免只读视图和创作侧看到两套名册。
	var cast []domain.CastEntry
	if progress != nil {
		cast, err = s.BuildCast(progress.CompletedChapters)
		if err != nil {
			return nil, nil, fmt.Errorf("重建配角名册失败：%w", err)
		}
	}

	// 摘要里的出场名单 → 每个名字的出场章号集合。
	seen := make(map[string][]int)
	latest := 0
	if progress != nil {
		chapters := append([]int(nil), progress.CompletedChapters...)
		sort.Ints(chapters)
		for _, ch := range chapters {
			if ch > latest {
				latest = ch
			}
			sum, err := s.Summaries.LoadSummary(ch)
			if err != nil || sum == nil {
				continue
			}
			for _, name := range sum.Characters {
				name = strings.TrimSpace(name)
				if name != "" {
					seen[name] = append(seen[name], ch)
				}
			}
		}
	}
	snapshots := make(map[string]domain.CharacterSnapshot)
	if snaps, err := s.Characters.LoadLatestSnapshots(); err == nil {
		for _, sn := range snaps {
			snapshots[sn.Name] = sn
		}
	}

	report := &CharacterReport{Latest: latest, Relationships: relations}
	for _, c := range chars {
		stat := CharacterStat{Character: c}
		names := append([]string{c.Name}, c.Aliases...)
		chapterSet := make(map[int]struct{})
		for _, n := range names {
			for _, ch := range seen[n] {
				chapterSet[ch] = struct{}{}
			}
		}
		for ch := range chapterSet {
			stat.Appearances++
			if stat.FirstSeen == 0 || ch < stat.FirstSeen {
				stat.FirstSeen = ch
			}
			if ch > stat.LastSeen {
				stat.LastSeen = ch
			}
		}
		if sn, ok := snapshots[c.Name]; ok {
			snCopy := sn
			stat.Snapshot = &snCopy
		}
		report.Core = append(report.Core, stat)
	}
	sort.SliceStable(report.Core, func(i, j int) bool {
		ri, rj := tierRank(report.Core[i].Tier), tierRank(report.Core[j].Tier)
		if ri != rj {
			return ri < rj
		}
		return report.Core[i].Appearances > report.Core[j].Appearances
	})

	report.Cast = append([]domain.CastEntry(nil), cast...)
	sort.SliceStable(report.Cast, func(i, j int) bool {
		if report.Cast[i].LastSeenChapter != report.Cast[j].LastSeenChapter {
			return report.Cast[i].LastSeenChapter > report.Cast[j].LastSeenChapter
		}
		return report.Cast[i].AppearanceCount > report.Cast[j].AppearanceCount
	})
	return info, report, nil
}

func tierRank(tier string) int {
	switch tier {
	case "core":
		return 0
	case "important", "":
		return 1
	case "secondary":
		return 2
	}
	return 3
}
