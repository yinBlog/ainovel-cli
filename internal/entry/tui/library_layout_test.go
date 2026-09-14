package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/diag"
	"github.com/voocel/ainovel-cli/internal/library"
)

func layoutTestChapters() (*library.BookInfo, []library.ChapterInfo) {
	info := &library.BookInfo{
		Title: "光斑", Phase: "writing", Completed: 2, Total: 40, WordCount: 6000,
		Volume: 1, Arc: 2, InProgress: 3, PendingRewrites: []int{1},
		Synopsis: "一个很长的简介，用来验证左栏会按列宽换行而不是把整页撑开。",
	}
	list := []library.ChapterInfo{
		{Chapter: 1, Title: "雨夜归人", Status: library.StatusCompleted, WordCount: 3000},
		{Chapter: 2, Title: "破晓", Status: library.StatusCompleted, WordCount: 3000},
		{Chapter: 3, Title: "第三章", Status: library.StatusInProgress},
		{Chapter: 4, Status: library.StatusPlanned, CoreEvent: "少年入城"},
	}
	return info, list
}

// 章节页分两栏：左边书本详情、右边章节列表，同一行上都要有内容。
func TestChaptersPageIsTwoColumns(t *testing.T) {
	info, list := layoutTestChapters()
	text, _ := renderChaptersText(info, list, 110, 0, false)
	lines := strings.Split(ansi.Strip(text), "\n")
	if len(lines) < 3 {
		t.Fatalf("行数太少：%v", lines)
	}
	detailW := chaptersDetailWidth(110)
	// 第 1 行左边是进度、右边是第 1 章：两栏确实并排，而不是上下堆叠。
	row := lines[chaptersListStart]
	if !strings.Contains(row[:detailW], "写作中") {
		t.Fatalf("左栏该行应是书本详情：%q", row)
	}
	if !strings.Contains(row[detailW:], "雨夜归人") {
		t.Fatalf("右栏该行应是第 1 章：%q", row)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"《光斑》", "2/40 章", "卷 1 · 弧 2", "正在写 第 3 章", "待返工 1", "简介"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("左栏缺少 %q：\n%s", want, joined)
		}
	}
}

// chaptersListStart 是光标滚动数学的锚点：它必须真的等于第 1 章所在的行号，
// 否则 ↑↓ 选章时视口会整体错位。改版式最容易踩的就是这里。
func TestChaptersListStartMatchesFirstRow(t *testing.T) {
	info, list := layoutTestChapters()
	for _, width := range []int{64, 90, 130} {
		text, _ := renderChaptersText(info, list, width, -1, false)
		lines := strings.Split(ansi.Strip(text), "\n")
		if chaptersListStart >= len(lines) {
			t.Fatalf("width=%d：行数不足", width)
		}
		if !strings.Contains(lines[chaptersListStart], "雨夜归人") {
			t.Fatalf("width=%d：第 %d 行应是第 1 章，实际是 %q",
				width, chaptersListStart, lines[chaptersListStart])
		}
		// 第 i 章必须落在 listStart+i 行，rowHeight 才能当 1 用。
		if !strings.Contains(lines[chaptersListStart+2], "第三章") {
			t.Fatalf("width=%d：第 3 章没落在预期行：%q", width, lines[chaptersListStart+2])
		}
	}
}

// 窄栏放不下时整块省掉字数，而不是让它截成半截数字——状态决定这章要不要管。
// 用长标题构造真正挤的场景：短标题在 64 宽下字数本来就放得下。
func TestChaptersNarrowColumnDropsWordCountWhole(t *testing.T) {
	info, list := layoutTestChapters()
	list[0].Title = "第一章 一个相当长的章节标题用来把右栏挤满"
	narrowText, _ := renderChaptersText(info, list, 64, -1, false)
	out := ansi.Strip(narrowText)
	if !strings.Contains(out, "已完成") {
		t.Fatalf("窄栏也必须保住状态：\n%s", out)
	}
	if strings.Contains(out, "3000 字") {
		t.Fatalf("窄栏该省掉字数：\n%s", out)
	}
	if strings.Contains(out, "300...") || strings.Contains(out, "30 字") {
		t.Fatalf("字数不该被截成半截：\n%s", out)
	}
	// 宽栏则要带上。
	wideText, _ := renderChaptersText(info, list, 120, -1, false)
	wide := ansi.Strip(wideText)
	if !strings.Contains(wide, "3000 字") {
		t.Fatalf("宽栏应显示字数：\n%s", wide)
	}
}

// 书架末行是“＋ 新开一本书”，光标能停上去，itemCount 要把它算进去。
func TestBooksShelfHasNewBookRow(t *testing.T) {
	books := []library.Book{{Info: &library.BookInfo{Title: "甲", Phase: "writing"}}}
	books[0].Dir = "/tmp/a/output/novel"
	s := newBooksState(books, books[0].Dir, nil, 120, 40)

	if got := s.itemCount(); got != 2 {
		t.Fatalf("1 本书 + 新建行 = 2，得到 %d", got)
	}
	s.moveCursor(1)
	if s.cursor != 1 {
		t.Fatalf("光标应能停在新建行，得到 %d", s.cursor)
	}
	if out := ansi.Strip(s.render(96)); !strings.Contains(out, "＋ 新开一本书") {
		t.Fatalf("列表应有新建入口：\n%s", out)
	}
}

// 按下新建后让位给一行路径输入，预填父目录，Esc 退回列表。
func TestNewBookInputTakesOverAndRestores(t *testing.T) {
	books := []library.Book{{Info: &library.BookInfo{Title: "甲", Phase: "writing"}}}
	books[0].Dir = "/tmp/a/output/novel"
	s := newBooksState(books, books[0].Dir, nil, 120, 40)
	s.addPrefix = "/tmp/"
	s.cursor = 1
	s.beginNewBook()

	out := ansi.Strip(s.render(96))
	if !strings.Contains(out, "新开一本书") || !strings.Contains(out, "/tmp/") {
		t.Fatalf("应让位给预填好的路径输入：\n%s", out)
	}
	if strings.Contains(out, "甲") {
		t.Fatalf("输入态不该同时铺着列表：\n%s", out)
	}
	if !strings.Contains(s.hint(), "Esc 返回书架") {
		t.Fatalf("输入态的提示行 = %q", s.hint())
	}
	s.adding = false
	if out := ansi.Strip(s.render(96)); !strings.Contains(out, "甲") {
		t.Fatalf("退出输入态应回到列表：\n%s", out)
	}
}

// Tab 展开：光标那一章补出标题/核心事件全文，并登记行号让滚动跟得上。
func TestChaptersExpandShowsFullDetailAndOffsets(t *testing.T) {
	info, list := layoutTestChapters()
	list[3].CoreEvent = "少年入城，在城门口被守军拦下，交出了那封信；随后遇见旧识。"

	collapsed, offCollapsed := renderChaptersText(info, list, 90, 3, false)
	expanded, offExpanded := renderChaptersText(info, list, 90, 3, true)
	if strings.Contains(ansi.Strip(collapsed), "随后遇见旧识") {
		t.Fatalf("未展开时核心事件本来就该被列宽挡住：\n%s", ansi.Strip(collapsed))
	}
	if !strings.Contains(ansi.Strip(expanded), "随后遇见旧识") {
		t.Fatalf("展开后应看到核心事件全文：\n%s", ansi.Strip(expanded))
	}
	// 行号登记：展开前每章一行，展开后光标之后的章要整体下移。
	if len(offCollapsed) != len(list) || len(offExpanded) != len(list) {
		t.Fatalf("每章都要登记行号：%v / %v", offCollapsed, offExpanded)
	}
	for i := range offCollapsed {
		if offCollapsed[i] != chaptersListStart+i {
			t.Fatalf("未展开时第 %d 章应在第 %d 行，登记为 %d", i, chaptersListStart+i, offCollapsed[i])
		}
	}
	if offExpanded[3] != offCollapsed[3] {
		t.Fatalf("展开的是第 4 章，它自己的行号不该变：%d vs %d", offExpanded[3], offCollapsed[3])
	}
}

// cursorLines 有 rowOffsets 就按实际行号算，没有才退回固定行高——
// 不定高的展开态全靠这条，退化路径也不能坏（老面板还在用）。
func TestCursorLinesPrefersRowOffsets(t *testing.T) {
	s := &libraryState{rows: make([]library.ChapterInfo, 3), listStart: 1, rowHeight: 1}
	s.cursor = 2
	if first, last := s.cursorLines(); first != 3 || last != 3 {
		t.Fatalf("固定行高退化路径：first=%d last=%d", first, last)
	}
	// 第 1 条展开成 4 行：后面的条目行号跟着后移，末条的高度按剩余推断。
	s.rowOffsets = []int{1, 5, 6}
	if first, last := s.cursorLines(); first != 6 || last != 6 {
		t.Fatalf("按登记行号：first=%d last=%d", first, last)
	}
	s.cursor = 0
	if first, last := s.cursorLines(); first != 1 || last != 4 {
		t.Fatalf("展开条目应占到下一条之前：first=%d last=%d", first, last)
	}
}

// 折行块的续行必须跟着缩进。先写两个空格再 wrapText 只能缩进首行，
// 后面几行会顶到最左，整块看起来像断了——/diag 的证据与建议曾经就是这样。
func TestReportFindingIndentsWrappedLines(t *testing.T) {
	var b strings.Builder
	renderFinding(&b, diag.Finding{
		Severity:   diag.SevWarning,
		Title:      "伏笔停滞",
		Evidence:   strings.Repeat("证据很长", 20),
		Suggestion: strings.Repeat("建议也很长", 20),
	}, 60)
	lines := strings.Split(strings.TrimRight(ansi.Strip(b.String()), "\n"), "\n")
	if len(lines) < 5 {
		t.Fatalf("长文本应该折成多行：%v", lines)
	}
	for i, line := range lines[1:] {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("第 %d 行没有缩进，整块会看起来断开：%q", i+1, line)
		}
	}
}

// 阅读页头部要给出全书位置、状态和 Editor 判定；弧/全局评审的长结论不挂在每章上。
func TestChapterTextHeaderCarriesContext(t *testing.T) {
	chapterScoped := renderChapterText(&library.ChapterView{
		Chapter: 3, Total: 40, WordCount: 3200, Status: library.StatusPendingRework,
		Body:   "正文。",
		Review: &library.ChapterReview{Scope: "chapter", Verdict: "polish", Summary: "中段偏拖。", Issues: 2},
	}, 80)
	out := ansi.Strip(chapterScoped)
	for _, want := range []string{"第 3 / 40 章", "3200 字", "待返工", "需打磨", "2 个问题", "中段偏拖。"} {
		if !strings.Contains(out, want) {
			t.Fatalf("头部缺少 %q：\n%s", want, out)
		}
	}

	arcScoped := ansi.Strip(renderChapterText(&library.ChapterView{
		Chapter: 3, Total: 40, Body: "正文。",
		Review: &library.ChapterReview{Scope: "arc", Verdict: "polish", Summary: strings.Repeat("很长的弧结论。", 30)},
	}, 80))
	if strings.Contains(arcScoped, "很长的弧结论。") {
		t.Fatalf("弧评审的长结论不该挂在每章头部：\n%s", arcScoped)
	}
	if !strings.Contains(arcScoped, "/reviews") {
		t.Fatalf("应指路 /reviews 看完整结论：\n%s", arcScoped)
	}
}
