package library

import (
	"fmt"
	"sort"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ── 全文检索 ──
//
// 长篇写到几百章之后，"这个设定我在哪章埋的"、"这个配角上次露面是什么时候"是日常
// 动作。这里直接扫产物文件，不建索引：一本 500 章的书正文约几 MB，一次全扫远快于
// 维护索引的复杂度和不一致风险（章节会被返工、被 /sync 手工改写）。
//
// 与 library 其余部分一样是只读观察者：不加目录锁，挂机写作时也能查。

// SearchScope 限定搜索范围。
type SearchScope string

const (
	ScopeAll       SearchScope = "all"
	ScopeChapters  SearchScope = "chapters"  // 正文（终稿，无终稿则草稿）
	ScopeOutline   SearchScope = "outline"   // 大纲条目
	ScopeSummaries SearchScope = "summaries" // 章节摘要
)

// SearchSource 标明命中来自哪类产物。
type SearchSource string

const (
	SourceChapter SearchSource = "正文"
	SourceDraft   SearchSource = "草稿"
	SourceOutline SearchSource = "大纲"
	SourceSummary SearchSource = "摘要"
)

// SearchHit 是一条命中。Excerpt 已按 rune 截取到可读长度，MatchAt/MatchLen 是
// 关键词在 Excerpt 里的 rune 区间，供 TUI 高亮——调用方不必再自己找一遍。
type SearchHit struct {
	Chapter  int
	Source   SearchSource
	Title    string
	Excerpt  string
	MatchAt  int
	MatchLen int
}

// SearchOptions 是一次检索的参数。Query 为空直接返回空结果。
type SearchOptions struct {
	Query         string
	Scope         SearchScope
	CaseSensitive bool
	Limit         int // 命中上限，<=0 用 defaultSearchLimit
	Context       int // 摘录里关键词前后各保留多少个 rune，<=0 用 defaultSearchContext
}

const (
	defaultSearchLimit   = 200
	defaultSearchContext = 28
)

// SearchResult 是一次检索的完整结果。Truncated 表示命中数达到上限被截断——
// 让用户知道"还有更多"，而不是误以为只有这些。
type SearchResult struct {
	Hits      []SearchHit
	Scanned   int // 实际扫过的章数
	Truncated bool
}

// Search 在一本书里检索关键词。
func Search(dir string, opts SearchOptions) (*BookInfo, *SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, nil, fmt.Errorf("检索词不能为空")
	}
	if opts.Scope == "" {
		opts.Scope = ScopeAll
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultSearchLimit
	}
	if opts.Context <= 0 {
		opts.Context = defaultSearchContext
	}

	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	// 大纲只读一次：OutlineStore.GetChapterOutline 每次调用都会重读并解析整份大纲，
	// 逐章调它会把 500 章书的一次检索拖成几秒。
	outline, _ := s.Outline.LoadOutline()
	titles := newTitleResolver(s, outline)

	m := newMatcher(query, opts.CaseSensitive)
	result := &SearchResult{}
	if opts.Scope == ScopeAll || opts.Scope == ScopeOutline {
		searchOutline(outline, m, opts, result)
	}
	if opts.Scope == ScopeAll || opts.Scope == ScopeChapters || opts.Scope == ScopeSummaries {
		searchChapters(s, titles, m, opts, result)
	}
	sortHits(result.Hits)
	return info, result, nil
}

// matcher 把大小写折叠一次算完，避免每行都 ToLower 一遍整段正文。
type matcher struct {
	needle []rune
	fold   bool
}

func newMatcher(query string, caseSensitive bool) matcher {
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	return matcher{needle: []rune(needle), fold: !caseSensitive}
}

// length 是关键词的 rune 数，用于回报高亮区间。
func (m matcher) length() int { return len(m.needle) }

// findAll 返回 text 中每个命中的 rune 起始下标。
//
// 逐 rune 比较而不是每个位置 string(runes[i:j]) == needle：后者虽然不分配
// （编译器会消掉比较用的转换），但每个位置仍要做一次转换，实测慢一倍多。
func (m matcher) findAll(text string) []int {
	hay := text
	if m.fold {
		hay = strings.ToLower(hay)
	}
	// 大小写折叠可能改变字节长度（少见但存在），所以命中位置统一按 rune 下标算，
	// 再由调用方按 rune 切片——按字节切会把中文切碎。
	runes := []rune(hay)
	if len(m.needle) == 0 || len(runes) < len(m.needle) {
		return nil
	}
	var at []int
	for i := 0; i+len(m.needle) <= len(runes); i++ {
		if runesHavePrefix(runes[i:], m.needle) {
			at = append(at, i)
			i += len(m.needle) - 1
		}
	}
	return at
}

func runesHavePrefix(hay, needle []rune) bool {
	for i, r := range needle {
		if hay[i] != r {
			return false
		}
	}
	return true
}

// titleResolver 惰性解析章节标题：只有真的命中了才去查。摘要标题自带缓存，
// 大纲则用调用方一次读入的那份，避免逐章重读。
type titleResolver struct {
	store   *store.Store
	outline map[int]string
	cache   map[int]string
}

func newTitleResolver(s *store.Store, outline []domain.OutlineEntry) *titleResolver {
	byChapter := make(map[int]string, len(outline))
	for _, e := range outline {
		if e.Chapter > 0 {
			byChapter[e.Chapter] = e.Title
		}
	}
	return &titleResolver{store: s, outline: byChapter, cache: make(map[int]string)}
}

func (r *titleResolver) title(chapter int) string {
	if title, ok := r.cache[chapter]; ok {
		return title
	}
	title := ""
	if t, err := r.store.Summaries.LoadSummaryTitle(chapter); err == nil && strings.TrimSpace(t) != "" {
		title = t
	} else {
		title = r.outline[chapter]
	}
	r.cache[chapter] = title
	return title
}

func searchChapters(s *store.Store, titles *titleResolver, m matcher, opts SearchOptions, result *SearchResult) {
	progress, err := s.Progress.Load()
	if err != nil {
		return
	}
	chapters := append([]int(nil), progress.CompletedChapters...)
	if progress.CurrentChapter > 0 && !containsInt(chapters, progress.CurrentChapter) {
		chapters = append(chapters, progress.CurrentChapter)
	}
	sort.Ints(chapters)

	for _, ch := range chapters {
		if result.Truncated {
			return
		}
		result.Scanned++
		if opts.Scope == ScopeAll || opts.Scope == ScopeChapters {
			source := SourceChapter
			text, err := s.Drafts.LoadChapterText(ch)
			if err != nil || strings.TrimSpace(text) == "" {
				if draft, derr := s.Drafts.LoadDraft(ch); derr == nil && strings.TrimSpace(draft) != "" {
					text, source = draft, SourceDraft
				}
			}
			collectHits(result, m, opts, ch, source, func() string { return titles.title(ch) }, text)
		}
		if opts.Scope == ScopeAll || opts.Scope == ScopeSummaries {
			if summary, err := s.Summaries.LoadSummary(ch); err == nil && summary != nil {
				// 摘要的正文、关键事件都要能搜到：查“某件事发生在哪章”时，
				// 关键事件往往比正文更早命中。
				parts := append([]string{summary.Summary}, summary.KeyEvents...)
				collectHits(result, m, opts, ch, SourceSummary, func() string { return titles.title(ch) }, strings.Join(parts, "　"))
			}
		}
	}
}

func searchOutline(outline []domain.OutlineEntry, m matcher, opts SearchOptions, result *SearchResult) {
	for _, entry := range outline {
		if result.Truncated {
			return
		}
		text := strings.TrimSpace(strings.Join([]string{entry.Title, entry.CoreEvent}, "　"))
		// 大纲条目自带标题，不用回头去解析。
		title := entry.Title
		collectHits(result, m, opts, entry.Chapter, SourceOutline, func() string { return title }, text)
	}
}

// collectHits 把一段文本里的命中转成 SearchHit。同一段里的多处命中都保留——
// 查"某个道具出现过几次"时，次数本身就是答案。
//
// title 是惰性的：没命中就不该为了填一个用不上的标题去读文件。
func collectHits(result *SearchResult, m matcher, opts SearchOptions, chapter int, source SearchSource, title func() string, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	matches := m.findAll(text)
	if len(matches) == 0 {
		return
	}
	runes := []rune(text)
	resolved := title()
	for _, at := range matches {
		if len(result.Hits) >= opts.Limit {
			result.Truncated = true
			return
		}
		excerpt, matchAt := excerptAround(runes, at, m.length(), opts.Context)
		result.Hits = append(result.Hits, SearchHit{
			Chapter: chapter, Source: source, Title: resolved,
			Excerpt: excerpt, MatchAt: matchAt, MatchLen: m.length(),
		})
	}
}

// excerptAround 取命中前后各 context 个 rune，并回报关键词在摘录里的 rune 下标。
//
// 摘录必须是**一行**：正文里有换行和 markdown 标题，原样截出来会让一条命中渲染成
// 好几行，把按固定行高滚动的列表整个错开。所以内部空白串一律压成一个空格，
// 两端空白从切片边界收掉（绝不越过命中本身）。
//
// 下标不靠"先拼好再回头找"，而是分段构造：压缩后的前缀有多长，命中就在多长之后。
// 关键词本身原样保留，不参与压缩——它要被逐字高亮。
func excerptAround(runes []rune, at, matchLen, context int) (string, int) {
	start := at - context
	if start < 0 {
		start = 0
	}
	end := at + matchLen + context
	if end > len(runes) {
		end = len(runes)
	}
	for start < at && isExcerptSpace(runes[start]) {
		start++
	}
	for end > at+matchLen && isExcerptSpace(runes[end-1]) {
		end--
	}

	lead := ""
	if start > 0 {
		lead = "…"
	}
	trail := ""
	if end < len(runes) {
		trail = "…"
	}
	body, matchAt := collapseWindow(runes[start:end], at-start, matchLen)
	return lead + string(body) + trail, len([]rune(lead)) + matchAt
}

// collapseWindow 把窗口里的连续空白（含换行）压成一个半角空格，并在同一次遍历里
// 记下命中的新下标。
//
// 压缩和定位必须一起做：压完再回头找关键词，遇到窗口里重复出现的同一个词就会指错。
// 关键词本身原样保留，不参与压缩——它要被逐字高亮；而空白压成空格而不是删掉，
// 否则英文 "the cat sat" 搜 cat 会挤成 "thecatsat"。
func collapseWindow(win []rune, at, matchLen int) ([]rune, int) {
	out := make([]rune, 0, len(win))
	matchAt, pending := 0, false
	flush := func() {
		if pending && len(out) > 0 {
			out = append(out, ' ')
		}
		pending = false
	}
	for i, r := range win {
		if i == at {
			flush()
			matchAt = len(out)
		}
		if i >= at && i < at+matchLen {
			out = append(out, r)
			continue
		}
		if isExcerptSpace(r) {
			pending = true
			continue
		}
		flush()
		out = append(out, r)
	}
	return out, matchAt
}

func isExcerptSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\r', '\n', '　':
		return true
	}
	return false
}

// sortHits 按章号升序、同章按来源稳定排序：大纲 → 摘要 → 正文，
// 让用户先看到"这章是干什么的"再看正文细节。
func sortHits(hits []SearchHit) {
	rank := map[SearchSource]int{SourceOutline: 0, SourceSummary: 1, SourceChapter: 2, SourceDraft: 3}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Chapter != hits[j].Chapter {
			return hits[i].Chapter < hits[j].Chapter
		}
		return rank[hits[i].Source] < rank[hits[j].Source]
	})
}
