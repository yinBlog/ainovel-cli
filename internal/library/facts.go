package library

import (
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
	At         string
	Violations []rules.Violation
}

// Violations 返回全书仍有机械违规的章节，按章号升序。
// 返工后被清空的章不出现——它已经没有问题了。
func Violations(dir string) (*BookInfo, []ViolationRow, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	records := s.World.LoadAllRuleViolations()
	rows := make([]ViolationRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, ViolationRow{Chapter: rec.Chapter, At: rec.At, Violations: rec.Violations})
	}
	return info, rows, nil
}
