package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/library"
)

// libraryCommands 是不经 Host、不取小说目录租约的只读子命令。
// 它们直接读工件，可以在另一个进程正在写作时使用。
var libraryCommands = map[string]bool{
	"search": true,
	"books":  true, "chapters": true, "read": true,
	"reviews": true, "foreshadow": true, "characters": true,
	"timeline": true, "violations": true,
}

// runLibraryCommand 执行 books / chapters / read，返回进程退出码：
// 0=成功，1=运行错误，2=用法错误。
func runLibraryCommand(name string, args []string, stdout, stderr io.Writer) int {
	switch name {
	case "books":
		if len(args) != 0 {
			fmt.Fprintln(stderr, "用法：ainovel-cli books")
			return 2
		}
		return runBooks(stdout, stderr)
	case "search":
		// 关键词在前，可选目录在后：search 青锋剑 / search 青锋剑 ./book
		if len(args) == 0 || len(args) > 2 {
			fmt.Fprintln(stderr, "用法：ainovel-cli search <关键词> [小说目录]")
			return 2
		}
		searchDir := ""
		if len(args) == 2 {
			searchDir = args[1]
		}
		return runSearch(args[0], searchDir, stdout, stderr)
	case "chapters":
		if len(args) > 1 {
			fmt.Fprintln(stderr, "用法：ainovel-cli chapters [小说目录]")
			return 2
		}
		dir := ""
		if len(args) == 1 {
			dir = args[0]
		}
		return runChapters(dir, stdout, stderr)
	case "read":
		if len(args) == 0 || len(args) > 2 {
			fmt.Fprintln(stderr, "用法：ainovel-cli read <章节号> [小说目录]")
			return 2
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			fmt.Fprintf(stderr, "章节号必须是正整数：%q\n", args[0])
			return 2
		}
		dir := ""
		if len(args) == 2 {
			dir = args[1]
		}
		return runRead(n, dir, stdout, stderr)
	case "reviews":
		// 可选章号在前、可选目录在后：reviews / reviews 5 / reviews ./book / reviews 5 ./book
		chapter, dir := 0, ""
		rest := args
		if len(rest) > 0 {
			if n, err := strconv.Atoi(rest[0]); err == nil {
				if n <= 0 {
					fmt.Fprintf(stderr, "章节号必须是正整数：%q\n", rest[0])
					return 2
				}
				chapter = n
				rest = rest[1:]
			}
		}
		if len(rest) > 1 {
			fmt.Fprintln(stderr, "用法：ainovel-cli reviews [章节号] [小说目录]")
			return 2
		}
		if len(rest) == 1 {
			dir = rest[0]
		}
		return runReviews(chapter, dir, stdout, stderr)
	case "timeline":
		if len(args) > 1 {
			fmt.Fprintln(stderr, "用法：ainovel-cli timeline [小说目录]")
			return 2
		}
		tlDir := ""
		if len(args) == 1 {
			tlDir = args[0]
		}
		return runTimeline(tlDir, stdout, stderr)
	case "violations":
		if len(args) > 1 {
			fmt.Fprintln(stderr, "用法：ainovel-cli violations [小说目录]")
			return 2
		}
		vDir := ""
		if len(args) == 1 {
			vDir = args[0]
		}
		return runViolations(vDir, stdout, stderr)
	case "foreshadow", "characters":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "用法：ainovel-cli %s [小说目录]\n", name)
			return 2
		}
		dir := ""
		if len(args) == 1 {
			dir = args[0]
		}
		if name == "foreshadow" {
			return runForeshadow(dir, stdout, stderr)
		}
		return runCharacters(dir, stdout, stderr)
	}
	fmt.Fprintf(stderr, "未知子命令：%s\n", name)
	return 2
}

func runReviews(chapter int, dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, reviews, err := library.Reviews(bookDir, chapter)
	if err != nil {
		fmt.Fprintf(stderr, "读取评审失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	if len(reviews) == 0 {
		if chapter > 0 {
			fmt.Fprintf(stdout, "第 %d 章暂无评审记录。\n", chapter)
		} else {
			fmt.Fprintln(stdout, "暂无评审记录。")
		}
		return 0
	}
	for i, r := range reviews {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "第 %d 章 [%s] %s", r.Chapter, reviewScopeLabel(r.Scope), reviewVerdictLabel(r.Verdict))
		if r.ContractStatus != "" {
			fmt.Fprintf(stdout, " · 契约 %s", r.ContractStatus)
		}
		if len(r.AffectedChapters) > 0 {
			fmt.Fprintf(stdout, " · 影响 %v", r.AffectedChapters)
		}
		fmt.Fprintln(stdout)
		if len(r.Dimensions) > 0 {
			parts := make([]string, 0, len(r.Dimensions))
			for _, d := range r.Dimensions {
				parts = append(parts, fmt.Sprintf("%s %d", d.Dimension, d.Score))
			}
			fmt.Fprintf(stdout, "  评分：%s\n", strings.Join(parts, " · "))
		}
		if r.Summary != "" {
			fmt.Fprintf(stdout, "  %s\n", r.Summary)
		}
		for _, issue := range r.Issues {
			fmt.Fprintf(stdout, "  - [%s] %s", issue.Severity, issue.Description)
			if len(issue.Chapters) > 0 {
				fmt.Fprintf(stdout, " (ch %v)", issue.Chapters)
			}
			fmt.Fprintln(stdout)
			if issue.Suggestion != "" {
				fmt.Fprintf(stdout, "    -> %s\n", issue.Suggestion)
			}
		}
	}
	return 0
}

func runForeshadow(dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, rep, err := library.Foreshadow(bookDir)
	if err != nil {
		fmt.Fprintf(stderr, "读取伏笔失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	fmt.Fprintf(stdout, "未回收 %d 条 · 已回收 %d 条 · 参照最新章 %d\n\n", len(rep.Open), len(rep.Resolved), rep.Latest)
	if len(rep.Open) > 0 {
		fmt.Fprintf(stdout, "%s %s %s %s\n", padRight("未回收", 16), padRight("埋于", 6), padRight("已过", 6), "描述")
		for _, f := range rep.Open {
			fmt.Fprintf(stdout, "%s %s %s %s\n", padRight(f.ID, 16), padRight(strconv.Itoa(f.PlantedAt), 6),
				padRight(fmt.Sprintf("%d 章", f.Age), 6), f.Description)
		}
	}
	if len(rep.Resolved) > 0 {
		if len(rep.Open) > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s %s %s %s\n", padRight("已回收", 16), padRight("埋于", 6), padRight("收于", 6), "描述")
		for _, f := range rep.Resolved {
			fmt.Fprintf(stdout, "%s %s %s %s\n", padRight(f.ID, 16), padRight(strconv.Itoa(f.PlantedAt), 6),
				padRight(strconv.Itoa(f.ResolvedAt), 6), f.Description)
		}
	}
	if len(rep.Open) == 0 && len(rep.Resolved) == 0 {
		fmt.Fprintln(stdout, "伏笔台账为空。")
	}
	return 0
}

func runCharacters(dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, rep, err := library.Characters(bookDir)
	if err != nil {
		fmt.Fprintf(stderr, "读取角色失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	if len(rep.Core) == 0 {
		fmt.Fprintln(stdout, "角色档案为空。")
	} else {
		fmt.Fprintf(stdout, "核心角色 %d 人（出场按章节摘要统计，参照最新章 %d）\n", len(rep.Core), rep.Latest)
		fmt.Fprintf(stdout, "%s %s %s %s %s\n", padRight("姓名", 12), padRight("层级", 10), padRight("出场", 6), padRight("最近", 6), "定位")
		for _, c := range rep.Core {
			last := "-"
			if c.LastSeen > 0 {
				last = strconv.Itoa(c.LastSeen)
			}
			fmt.Fprintf(stdout, "%s %s %s %s %s\n", padRight(c.Name, 12), padRight(tierLabel(c.Tier), 10),
				padRight(strconv.Itoa(c.Appearances), 6), padRight(last, 6), c.Role)
			if c.Snapshot != nil && c.Snapshot.Status != "" {
				fmt.Fprintf(stdout, "%s 状态：%s", strings.Repeat(" ", 12), c.Snapshot.Status)
				if c.Snapshot.Power != "" {
					fmt.Fprintf(stdout, " · %s", c.Snapshot.Power)
				}
				fmt.Fprintln(stdout)
			}
		}
	}
	if len(rep.Cast) > 0 {
		fmt.Fprintf(stdout, "\n配角名册 %d 人（按最近出场）\n", len(rep.Cast))
		fmt.Fprintf(stdout, "%s %s %s %s\n", padRight("姓名", 12), padRight("出场", 6), padRight("最近", 6), "定位")
		for _, c := range rep.Cast {
			fmt.Fprintf(stdout, "%s %s %s %s\n", padRight(c.Name, 12), padRight(strconv.Itoa(c.AppearanceCount), 6),
				padRight(strconv.Itoa(c.LastSeenChapter), 6), c.BriefRole)
		}
	}
	if len(rep.Relationships) > 0 {
		fmt.Fprintf(stdout, "\n人物关系 %d 条\n", len(rep.Relationships))
		for _, r := range rep.Relationships {
			fmt.Fprintf(stdout, "  %s — %s：%s（ch %d）\n", r.CharacterA, r.CharacterB, r.Relation, r.Chapter)
		}
	}
	return 0
}

func reviewScopeLabel(scope string) string {
	switch scope {
	case "chapter":
		return "章节"
	case "arc":
		return "弧"
	case "global":
		return "全局"
	}
	return scope
}

func reviewVerdictLabel(v string) string {
	switch v {
	case "accept":
		return "通过"
	case "polish":
		return "需打磨"
	case "rewrite":
		return "需重写"
	}
	return v
}

func tierLabel(tier string) string {
	switch tier {
	case "core":
		return "核心"
	case "important", "":
		return "重要"
	case "secondary":
		return "次要"
	case "decorative":
		return "点缀"
	}
	return tier
}

func defaultRegistry() *library.Registry {
	return library.DefaultRegistry(bootstrap.DefaultConfigDir())
}

func runBooks(stdout, stderr io.Writer) int {
	books, err := defaultRegistry().List()
	if err != nil {
		fmt.Fprintf(stderr, "读取书架失败：%v\n", err)
		return 1
	}
	// 当前目录的书即使还没登记（例如手动拷贝来的目录）也要能看见。
	if dir, err := library.ResolveBookDir(""); err == nil {
		registered := false
		for _, b := range books {
			if b.Dir == dir {
				registered = true
				break
			}
		}
		if !registered {
			b := library.Book{}
			b.Dir = dir
			if info, err := library.Inspect(dir); err == nil {
				b.Info = info
				b.Activity = library.LoadActivity(dir)
			} else {
				b.Err = err
			}
			books = append([]library.Book{b}, books...)
		}
	}
	if len(books) == 0 {
		fmt.Fprintln(stdout, "书架为空：还没有在任何目录启动过 ainovel-cli。")
		return 0
	}
	fmt.Fprintf(stdout, "%-3s %s %s %s %s %s\n", "#",
		padRight("书名", 24), padRight("进度", 12), padRight("阶段", 9),
		padRight("花费/活跃", 20), "目录")
	for i, b := range books {
		fmt.Fprintf(stdout, "%-3d %s %s %s %s %s\n",
			i+1, padRight(bookTitle(b), 24), padRight(bookProgress(b), 12), padRight(bookPhase(b), 9),
			padRight(bookActivity(b), 20), library.DisplayPath(b.Dir))
	}
	return 0
}

func runChapters(dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, list, err := library.Chapters(bookDir)
	if err != nil {
		fmt.Fprintf(stderr, "读取章节失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	if len(list) == 0 {
		fmt.Fprintln(stdout, "（尚无章节：大纲未生成）")
		return 0
	}
	fmt.Fprintf(stdout, "%s %s %s %s\n", padRight("章", 5), padRight("状态", 8), padRight("字数", 7), "标题")
	for _, c := range list {
		words := "-"
		if c.WordCount > 0 {
			words = strconv.Itoa(c.WordCount)
		}
		fmt.Fprintf(stdout, "%-5d %s %-7s %s\n", c.Chapter, padRight(chapterStatusLabel(c.Status), 8), words, c.Title)
	}
	return 0
}

func runRead(n int, dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	view, err := library.ReadChapter(bookDir, n)
	if err != nil {
		if errors.Is(err, library.ErrChapterNotFound) {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stderr, "读取章节失败：%v\n", err)
		return 1
	}
	header := fmt.Sprintf("第 %d", view.Chapter)
	if view.Total > 0 {
		header += fmt.Sprintf(" / %d", view.Total)
	}
	header += " 章"
	if view.Title != "" {
		header += " " + view.Title
	}
	if view.Draft {
		header += "（草稿，未提交）"
	}
	fmt.Fprintf(stdout, "%s · %d 字", header, view.WordCount)
	if label := chapterStatusLabel(view.Status); label != "" && !view.Draft {
		fmt.Fprintf(stdout, " · %s", label)
	}
	fmt.Fprintln(stdout)
	// Editor 判定紧跟头部：读一章时最想知道的就是"这章过没过"。
	if r := view.Review; r != nil {
		fmt.Fprintf(stdout, "Editor · %s评审 %s", reviewScopeLabel(r.Scope), reviewVerdictLabel(r.Verdict))
		if r.Issues > 0 {
			fmt.Fprintf(stdout, " · %d 个问题", r.Issues)
		}
		fmt.Fprintln(stdout)
		if r.Summary != "" && r.Scope == "chapter" {
			fmt.Fprintf(stdout, "  %s\n", r.Summary)
		} else if r.Summary != "" {
			fmt.Fprintln(stdout, "  该评审覆盖多章，完整结论见 ainovel-cli reviews")
		}
	}
	fmt.Fprintln(stdout)
	body := view.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	fmt.Fprint(stdout, body)
	return 0
}

func printBookHeader(w io.Writer, info *library.BookInfo) {
	title := info.Title
	if title == "" {
		title = "（未命名）"
	}
	head := fmt.Sprintf("《%s》 %s · 已完成 %d 章 · %d 字", title, phaseLabel(info), info.Completed, info.WordCount)
	if len(info.PendingRewrites) > 0 {
		head += fmt.Sprintf(" · 待返工 %v", info.PendingRewrites)
	}
	fmt.Fprintln(w, head)
	if info.Synopsis != "" {
		fmt.Fprintln(w, info.Synopsis)
	}
	fmt.Fprintf(w, "%s\n\n", library.DisplayPath(info.Dir))
}

func bookTitle(b library.Book) string {
	switch {
	case b.Missing:
		return "（目录已不存在）"
	case b.Err != nil:
		return "（读取失败）"
	case b.Info == nil:
		return "（尚未开书）"
	case b.Info.Title == "":
		return "（未命名）"
	}
	return b.Info.Title
}

func bookProgress(b library.Book) string {
	if b.Info == nil {
		return "-"
	}
	if b.Info.Layered || b.Info.Total <= 0 {
		return fmt.Sprintf("%d 章", b.Info.Completed)
	}
	return fmt.Sprintf("%d/%d 章", b.Info.Completed, b.Info.Total)
}

func bookPhase(b library.Book) string {
	if b.Info == nil {
		return "-"
	}
	return phaseLabel(b.Info)
}

func phaseLabel(info *library.BookInfo) string {
	switch info.Phase {
	case "init":
		return "初始化"
	case "premise":
		return "前提已定"
	case "outline":
		return "大纲已定"
	case "writing":
		switch info.Flow {
		case "reviewing":
			return "评审中"
		case "rewriting":
			return "返工中"
		case "polishing":
			return "打磨中"
		case "steering":
			return "处理干预"
		}
		return "写作中"
	case "complete":
		return "已完结"
	}
	return string(info.Phase)
}

func chapterStatusLabel(s library.ChapterStatus) string {
	switch s {
	case library.StatusCompleted:
		return "已完成"
	case library.StatusPendingRework:
		return "待返工"
	case library.StatusInProgress:
		return "写作中"
	case library.StatusPlanned:
		return "待写"
	}
	return string(s)
}

// padRight 按终端显示宽度补齐（中文按 2 列计），让含中文的表格列对齐。
func padRight(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// runSearch 全书检索正文、大纲与摘要。与其余只读子命令一样不加载配置、不占目录锁，
// 另一个终端正在写作时也能查。
func runSearch(query, dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, result, err := library.Search(bookDir, library.SearchOptions{Query: query})
	if err != nil {
		fmt.Fprintf(stderr, "检索失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	if len(result.Hits) == 0 {
		fmt.Fprintf(stdout, "没有找到 %q。已扫 %d 章的正文与摘要，以及全部大纲。\n", query, result.Scanned)
		return 0
	}
	fmt.Fprintf(stdout, "%d 处命中", len(result.Hits))
	if result.Truncated {
		fmt.Fprint(stdout, "（已达上限，还有更多）")
	}
	fmt.Fprintln(stdout)
	for _, hit := range result.Hits {
		fmt.Fprintf(stdout, "%-5d %s %s\n", hit.Chapter, padRight(string(hit.Source), 6), hit.Title)
		fmt.Fprintf(stdout, "      %s\n", hit.Excerpt)
	}
	return 0
}

// bookActivity 是书架里的"花费 / 多久没动"一格。多本书之间选哪本继续时靠它判断。
func bookActivity(b library.Book) string {
	var parts []string
	if b.Activity.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", b.Activity.CostUSD))
	}
	if idle, ok := b.Activity.Idle(); ok {
		parts = append(parts, humanizeIdle(idle)+"前")
	}
	if b.Activity.RecentChapters > 0 {
		parts = append(parts, fmt.Sprintf("+%d", b.Activity.RecentChapters))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// humanizeIdle 把"多久没动了"说成人话：量级才是判断依据，精确到秒没有意义。
func humanizeIdle(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%d 个月", int(d.Hours()/24/30))
	}
}

// runTimeline 打印故事内时间线。
func runTimeline(dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, rows, err := library.Timeline(bookDir)
	if err != nil {
		fmt.Fprintf(stderr, "读取时间线失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "（时间线为空：还没有章节提取出时间事件）")
		return 0
	}
	fmt.Fprintf(stdout, "%s %s %s %s\n", padRight("", 2), padRight("章", 5), padRight("故事时间", 16), "事件")
	for _, r := range rows {
		mark := "  "
		if r.Revisited {
			mark = "<-"
		}
		event := r.Event
		if len(r.Characters) > 0 {
			event += "（" + strings.Join(r.Characters, "、") + "）"
		}
		fmt.Fprintf(stdout, "%s %-5d %s %s\n", mark, r.Chapter, padRight(r.Time, 16), event)
	}
	return 0
}

// runViolations 打印仍未处理的机械违规。
func runViolations(dir string, stdout, stderr io.Writer) int {
	bookDir, err := library.ResolveBookDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, rows, err := library.Violations(bookDir)
	if err != nil {
		fmt.Fprintf(stderr, "读取违规台账失败：%v\n", err)
		return 1
	}
	printBookHeader(stdout, info)
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "（没有未处理的机械违规）")
		return 0
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "第 %d 章  %d 条\n", row.Chapter, len(row.Violations))
		for _, v := range row.Violations {
			fmt.Fprintf(stdout, "  %s %s %s", padRight(string(v.Severity), 8), padRight(v.Rule, 18), v.Target)
			if v.Actual != nil {
				fmt.Fprintf(stdout, "  实际 %v", v.Actual)
			}
			if v.Limit != nil {
				fmt.Fprintf(stdout, "  阈值 %v", v.Limit)
			}
			fmt.Fprintln(stdout)
		}
	}
	return 0
}
