package library

import (
	"fmt"
	"sort"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ── 时间线与违规台账 ──
//
// 这两份事实一直在被写入（timeline.jsonl / rule_violations.jsonl），但此前没有任何
// 只读入口。长篇最容易崩的就是故事内时间——"三天后"一路累加，几十章后没人对得上；
// 违规台账则是"我配的规则到底有没有生效"的唯一凭据。

// TimelineRow 是时间线上的一条，Revisited 标记这个故事时间在别的时间之后又回来了。
type TimelineRow struct {
	domain.TimelineEvent
	// Revisited 为 true 表示这条的故事时间之前出现过，中间还隔着别的时间——
	// 也就是时间往回跳了。可能是合法回叙，也可能是真写乱了，这里只报事实。
	//
	// 刻意不标"与上一条时间相同"：一个场景跨章continue 是长篇里最常见的正常写法，
	// 标它只会把真正的问题淹掉。
	Revisited bool
}

// Timeline 返回一本书的故事时间线，按章号升序。
func Timeline(dir string) (*BookInfo, []TimelineRow, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	events, err := s.World.LoadTimeline()
	if err != nil {
		return info, nil, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Chapter < events[j].Chapter })

	// 故事内时间是自由文本（"三日后"/"次年春"），没法可靠比较先后。能确定的只有一件事：
	// 某个时间串出现过、被别的时间接替、然后又回来了——那是往回跳。连续同一时间不算
	// （跨章连场），更强的判断需要语义，归 diag/Arbiter，不是只读视图该做的。
	rows := make([]TimelineRow, 0, len(events))
	seen := make(map[string]bool, len(events))
	prev := ""
	for _, e := range events {
		row := TimelineRow{TimelineEvent: e}
		if e.Time != "" && e.Time != prev {
			row.Revisited = seen[e.Time]
			seen[e.Time] = true
			prev = e.Time
		}
		rows = append(rows, row)
	}
	return info, rows, nil
}

// ViolationRow 是违规台账的一行：某章当前仍存在的机械违规。
type ViolationRow struct {
	Chapter    int
	Violations []rules.Violation
}

// Violations 返回全书仍有机械违规的章节，按章号升序。
//
// 违规是**派生事实**，不是存下来的账：对每章的接纳正文现跑一遍机械检查，
// 判定与 novel_context 的 buildRuleViolations 同口径（Lint + Check），
// 只读视图和创作侧不会看到两套结论。
//
// 这样返工修好的章会自动从台账消失，不依赖有没有补写一条"已清空"的记录；
// 代价是看不到历史——只回答"现在哪些章还留着问题"。
func Violations(dir string) (*BookInfo, []ViolationRow, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return info, nil, fmt.Errorf("读取进度失败：%w", err)
	}
	// 没有用户规则快照时只跑 Lint（markdown 残留、非中文片段这类与规则无关的检查）。
	var structured rules.Structured
	if snap, err := s.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}

	chapters := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(chapters)
	rows := make([]ViolationRow, 0, len(chapters))
	for _, ch := range chapters {
		// 逐章读、缺记录就跳过：老项目或半迁移的书可能有已完成章却没有接纳记录，
		// 只读视图不该因此整个打不开（与 novel_context 的 record == nil 早返回同口径）。
		rec, err := s.ChapterRecords.Load(ch)
		if err != nil || rec == nil {
			continue
		}
		violations := append(rules.Lint(rec.Content), rules.Check(rec.Content, structured)...)
		if len(violations) == 0 {
			continue
		}
		rows = append(rows, ViolationRow{Chapter: ch, Violations: violations})
	}
	return info, rows, nil
}
