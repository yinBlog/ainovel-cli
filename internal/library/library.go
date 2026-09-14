// Package library 提供"书架"只读视图：列出本机开过的小说、查看某本书的章节
// 明细、读取某章正文。
//
// 与 diag 同属观察层：只读 store 工件，不产生事实、不改流程、不调 LLM。
// 它不持有小说目录租约，可以与正在写作的进程并行读取——单文件 tmp+rename
// 原子替换保证读到的永远是完整版本。
package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// DefaultBookSubdir 是启动目录下小说产物的固定相对路径（与 bootstrap.Config.FillDefaults 一致）。
const DefaultBookSubdir = "output/novel"

// ErrNotBook 表示目录里没有小说事实（缺 meta/progress.json）。
var ErrNotBook = errors.New("目录中没有小说数据")

// ErrChapterNotFound 表示章节既无终稿也无草稿。
var ErrChapterNotFound = errors.New("章节不存在")

// BookInfo 是一本书的概览，全部来自 meta/book.json + meta/progress.json。
type BookInfo struct {
	Dir             string
	Title           string
	Synopsis        string
	Phase           domain.Phase
	Flow            domain.FlowState
	Completed       int
	Total           int // 非分层模式为大纲章数；分层模式仅是内部容量估计
	Layered         bool
	WordCount       int
	InProgress      int
	PendingRewrites []int
	Volume, Arc     int
}

// ChapterStatus 是章节在书架视图里的状态标签。
type ChapterStatus string

const (
	StatusCompleted     ChapterStatus = "completed"
	StatusPendingRework ChapterStatus = "pending_rework" // 已完成但排在返工队列
	StatusInProgress    ChapterStatus = "in_progress"
	StatusPlanned       ChapterStatus = "planned" // 仅有大纲
)

// ChapterInfo 是章节列表里的一行。
type ChapterInfo struct {
	Chapter   int
	Title     string
	Status    ChapterStatus
	WordCount int
	CoreEvent string // 大纲核心事件（有则带上，便于扫目录时回忆剧情）
}

// ChapterView 是一章的可读内容。
type ChapterView struct {
	Chapter   int
	Title     string
	Body      string
	WordCount int
	Draft     bool // true 表示读到的是未提交草稿而非终稿

	// 读一章的时候最想知道的其实是它在全书里的位置和 Editor 的判定，
	// 否则读完还得另开 /chapters 和 /reviews 各看一次。
	Total  int            // 全书章数（大纲条数），0 = 还没有大纲
	Status ChapterStatus  // 已完成 / 待返工 / 写作中 / 待写
	Review *ChapterReview // 落在该章的最近一条评审；没有则为 nil
}

// ChapterReview 是阅读面板要用的评审摘要：裁定 + 结论 + 问题条数。
// 完整评审仍看 /reviews，这里只回答"这章过没过"。
type ChapterReview struct {
	Scope   string
	Verdict string
	Summary string
	Issues  int
}

// ResolveBookDir 把用户给的路径归一化为小说目录：
// 传启动目录（含 output/novel）或直接传小说目录都可以；空串表示当前目录。
func ResolveBookDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	nested := filepath.Join(abs, filepath.FromSlash(DefaultBookSubdir))
	if isBookDir(nested) {
		return nested, nil
	}
	if isBookDir(abs) {
		return abs, nil
	}
	return "", fmt.Errorf("%w：%s（也不存在 %s）", ErrNotBook, abs, filepath.FromSlash(DefaultBookSubdir))
}

func isBookDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "meta", "progress.json"))
	return err == nil && !st.IsDir()
}

// Inspect 读取一本书的概览。目录里没有 progress 时返回 ErrNotBook。
func Inspect(dir string) (*BookInfo, error) {
	s := store.NewStore(dir)
	return inspect(s)
}

func inspect(s *store.Store) (*BookInfo, error) {
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("读取进度失败：%w", err)
	}
	if progress == nil {
		return nil, fmt.Errorf("%w：%s", ErrNotBook, s.Dir())
	}
	info := &BookInfo{
		Dir:             s.Dir(),
		Phase:           progress.Phase,
		Flow:            progress.Flow,
		Completed:       len(progress.CompletedChapters),
		Total:           progress.TotalChapters,
		Layered:         progress.Layered,
		WordCount:       progress.TotalWordCount,
		InProgress:      progress.InProgressChapter,
		PendingRewrites: append([]int(nil), progress.PendingRewrites...),
		Volume:          progress.CurrentVolume,
		Arc:             progress.CurrentArc,
	}
	// 书名在规划期可能尚未生成；作品信息损坏也只降级为无标题，不阻断列表。
	if book, err := s.Book.Load(); err == nil && book != nil {
		info.Title = book.Title
		info.Synopsis = book.Synopsis
	}
	return info, nil
}

// Chapters 返回一本书的章节明细：大纲已展开章 ∪ 已完成章 ∪ 进行中章，按章号升序。
// 标题优先取章节摘要（写完后的真实标题），其次取大纲标题。
func Chapters(dir string) (*BookInfo, []ChapterInfo, error) {
	s := store.NewStore(dir)
	info, err := inspect(s)
	if err != nil {
		return nil, nil, err
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("读取进度失败：%w", err)
	}
	outline, err := s.Outline.LoadOutline()
	if err != nil {
		return nil, nil, fmt.Errorf("读取大纲失败：%w", err)
	}

	rows := make(map[int]*ChapterInfo)
	row := func(ch int) *ChapterInfo {
		if r, ok := rows[ch]; ok {
			return r
		}
		r := &ChapterInfo{Chapter: ch, Status: StatusPlanned}
		rows[ch] = r
		return r
	}
	for _, e := range outline {
		if e.Chapter <= 0 {
			continue
		}
		r := row(e.Chapter)
		r.Title = e.Title
		r.CoreEvent = e.CoreEvent
	}
	completed := make(map[int]struct{}, len(progress.CompletedChapters))
	for _, ch := range progress.CompletedChapters {
		completed[ch] = struct{}{}
		r := row(ch)
		r.Status = StatusCompleted
		r.WordCount = progress.ChapterWordCounts[ch]
	}
	for _, ch := range progress.PendingRewrites {
		if _, ok := completed[ch]; ok {
			row(ch).Status = StatusPendingRework
		}
	}
	if ch := progress.InProgressChapter; ch > 0 {
		if _, ok := completed[ch]; !ok {
			row(ch).Status = StatusInProgress
		}
	}

	list := make([]ChapterInfo, 0, len(rows))
	for _, r := range rows {
		if r.Status == StatusCompleted || r.Status == StatusPendingRework {
			if title, err := s.Summaries.LoadSummaryTitle(r.Chapter); err == nil && strings.TrimSpace(title) != "" {
				r.Title = title
			}
		}
		list = append(list, *r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Chapter < list[j].Chapter })
	return info, list, nil
}

// ReadChapter 读取第 n 章。已完成章返回终稿；否则若有草稿返回草稿并标记 Draft；
// 两者皆无返回 ErrChapterNotFound。
func ReadChapter(dir string, n int) (*ChapterView, error) {
	if n <= 0 {
		return nil, fmt.Errorf("章节号必须大于 0：%d", n)
	}
	s := store.NewStore(dir)
	if _, err := inspect(s); err != nil {
		return nil, err
	}
	view := &ChapterView{Chapter: n}
	text, err := s.Drafts.LoadChapterText(n)
	if err != nil {
		return nil, fmt.Errorf("读取第 %d 章终稿失败：%w", n, err)
	}
	if strings.TrimSpace(text) == "" {
		draft, err := s.Drafts.LoadDraft(n)
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 章草稿失败：%w", n, err)
		}
		if strings.TrimSpace(draft) == "" {
			return nil, fmt.Errorf("%w：第 %d 章", ErrChapterNotFound, n)
		}
		text = draft
		view.Draft = true
	}
	view.Body = text
	view.WordCount = utf8.RuneCountInString(text)
	if title, err := s.Summaries.LoadSummaryTitle(n); err == nil && strings.TrimSpace(title) != "" {
		view.Title = title
	} else if entry, err := s.Outline.GetChapterOutline(n); err == nil && entry != nil {
		view.Title = entry.Title
	}
	annotateChapterView(s, view)
	return view, nil
}

// annotateChapterView 补上该章在全书里的位置、状态和 Editor 判定。
// 读不到就留零值——阅读面板缺这些只是少几行提示，不该让正文打不开。
func annotateChapterView(s *store.Store, view *ChapterView) {
	if outline, err := s.Outline.LoadOutline(); err == nil {
		view.Total = len(outline)
	}
	if progress, err := s.Progress.Load(); err == nil {
		view.Status = chapterStatusOf(progress, view.Chapter)
	}
	if reviews, err := loadAllReviews(s); err == nil {
		// 取最后一条落在该章的评审：同章可能被复评，最新的才代表当前结论。
		for i := len(reviews) - 1; i >= 0; i-- {
			r := reviews[i]
			if r.Chapter != view.Chapter && !containsInt(r.AffectedChapters, view.Chapter) {
				continue
			}
			view.Review = &ChapterReview{
				Scope: r.Scope, Verdict: r.Verdict, Summary: r.Summary, Issues: len(r.Issues),
			}
			break
		}
	}
}

// LaunchDirOf 由小说目录反推启动目录：小说目录固定是 <启动目录>/output/novel
// （与 bootstrap.Config.FillDefaults 一致）。书架登记的是小说目录，而 per-book 的
// ./.ainovel/config.json 和 ./.ainovel/rules/ 挂在启动目录上，切书时需要这一步换算。
// 传进来的路径不是标准小说目录时返回空串——不猜。
func LaunchDirOf(bookDir string) string {
	abs, err := filepath.Abs(bookDir)
	if err != nil {
		return ""
	}
	launch := abs
	for range strings.Split(DefaultBookSubdir, "/") {
		launch = filepath.Dir(launch)
	}
	if filepath.Join(launch, filepath.FromSlash(DefaultBookSubdir)) != abs {
		return ""
	}
	return launch
}

// chapterStatusOf 判定单章状态。与 Chapters 的整表推导同一套优先级：
// 待返工压过已完成，写作中只对还没完成的章成立。
func chapterStatusOf(progress *domain.Progress, chapter int) ChapterStatus {
	completed := false
	for _, ch := range progress.CompletedChapters {
		if ch == chapter {
			completed = true
			break
		}
	}
	if completed {
		if containsInt(progress.PendingRewrites, chapter) {
			return StatusPendingRework
		}
		return StatusCompleted
	}
	if progress.InProgressChapter == chapter {
		return StatusInProgress
	}
	return StatusPlanned
}
