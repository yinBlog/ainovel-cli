package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/library"
)

// newSwitchTestBook 造一本最小的书，返回启动目录与小说目录。
func newSwitchTestBook(t *testing.T) (launchDir, bookDir string) {
	t.Helper()
	launchDir = t.TempDir()
	bookDir = filepath.Join(launchDir, "output", "novel", "meta")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bookDir, "progress.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return launchDir, filepath.Dir(bookDir)
}

// LaunchDirOf 是切书的路径换算：书架记的是小说目录，per-book 配置和规则挂在启动目录上。
func TestLaunchDirOfRoundTrips(t *testing.T) {
	launch := t.TempDir()
	bookDir := filepath.Join(launch, "output", "novel")
	if got := library.LaunchDirOf(bookDir); got != launch {
		t.Fatalf("LaunchDirOf(%q) = %q，期望 %q", bookDir, got, launch)
	}
	// 不是标准小说目录就返回空串——不猜路径，宁可让切书带着原因失败。
	if got := library.LaunchDirOf(filepath.Join(launch, "somewhere")); got != "" {
		t.Fatalf("非标准目录应返回空串，得到 %q", got)
	}
}

// 切到不存在 / 非标准的目录必须被拒，且当前这本书不能被关掉。
func TestSwitchBookRefusesNonBookDir(t *testing.T) {
	m := NewModel(nil, "")
	next, _ := m.switchBook(filepath.Join(t.TempDir(), "not-a-book"))
	model := next.(Model)
	if model.runtime != nil {
		t.Fatal("本来就没有 runtime，不该凭空建一个")
	}
	if len(model.events) == 0 || !strings.Contains(model.events[len(model.events)-1].Summary, "不是标准小说目录") {
		t.Fatalf("应给出“不是标准小说目录”的原因：%+v", model.events)
	}
}

// 被别的进程占着的书在列表里标出来，按 Enter 也要被拦住，不能等切到一半才撞锁。
func TestSwitchToCursorBookRefusesInUse(t *testing.T) {
	launch := t.TempDir()
	bookDir := filepath.Join(launch, "output", "novel")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	books := []library.Book{{Info: &library.BookInfo{Title: "占用中的书"}}}
	books[0].Dir = bookDir

	m := NewModel(nil, "")
	m.width, m.height = 120, 40
	m.library = newBooksState(books, "", map[string]bool{bookDir: true}, 120, 40)

	next, _ := m.handleLibraryKey(tea.KeyMsg{Type: tea.KeyEnter})
	model := next.(Model)
	if model.library == nil {
		t.Fatal("被拒后面板应留在书架上")
	}
	if len(model.events) == 0 || !strings.Contains(model.events[len(model.events)-1].Summary, "占用") {
		t.Fatalf("应给出被占用的原因：%+v", model.events)
	}
}

// 书架要把“别的终端正在写”的书标出来，但绝不能把当前这本标成占用——
// flock 按打开的文件判定，自己持有的锁探测起来同样是“已占用”。
func TestProbeBooksInUseSkipsCurrentBook(t *testing.T) {
	dir := t.TempDir()
	books := []library.Book{{}, {Missing: true}}
	books[0].Dir = dir
	books[1].Dir = filepath.Join(dir, "gone")

	inUse := probeBooksInUse(books, dir)
	if inUse[dir] {
		t.Fatal("当前这本书不该被标成使用中")
	}
	if inUse[books[1].Dir] {
		t.Fatal("目录不存在的书不该被标成使用中")
	}
}

// 书架列表要能看出哪本在用、哪本被占。
func TestBooksTextMarksCurrentAndInUse(t *testing.T) {
	books := []library.Book{
		{Info: &library.BookInfo{Title: "甲", Phase: "writing"}},
		{Info: &library.BookInfo{Title: "乙", Phase: "writing"}},
	}
	books[0].Dir = "/tmp/a/output/novel"
	books[1].Dir = "/tmp/b/output/novel"

	out := renderBooksText(books, "/tmp/a/output/novel", map[string]bool{"/tmp/b/output/novel": true}, 100, -1)
	if !strings.Contains(out, "← 当前") {
		t.Fatalf("当前这本应标出来：%s", out)
	}
	if !strings.Contains(out, "使用中") {
		t.Fatalf("被别的进程占着的书应标出来：%s", out)
	}
	if !strings.Contains(out, "Enter 切到该书创作") {
		t.Fatalf("表头应说明 Enter 是换书：%s", out)
	}
}

// 新建书必须拒绝已经有书的目录：覆盖别人的书是不可逆的，那种情况用户要的是切过去。
func TestStartNewBookRefusesExistingBook(t *testing.T) {
	launch, book := newSwitchTestBook(t)
	_, err := prepareNewBookDir(launch)
	if err == nil {
		t.Fatal("已经有书的目录必须被拒，否则会覆盖别人的书")
	}
	if !strings.Contains(err.Error(), "已经有一本书") || !strings.Contains(err.Error(), "/books") {
		t.Fatalf("应说明已有书并指路 /books：%v", err)
	}
	if _, statErr := os.Stat(book); statErr != nil {
		t.Fatalf("原有的书不该被动过：%v", statErr)
	}
}

func TestStartNewBookRejectsEmptyPath(t *testing.T) {
	if _, err := prepareNewBookDir("   "); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("空路径应给出用法，得到 %v", err)
	}
}

// 目标目录还不存在时要能建出来——"开新书"就是从一个不存在的目录开始的。
func TestStartNewBookCreatesMissingDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "新书", "嵌套")
	if _, err := os.Stat(target); err == nil {
		t.Fatal("测试前置：目录不该已存在")
	}
	abs, err := prepareNewBookDir(target)
	if err != nil {
		t.Fatalf("准备新书目录失败：%v", err)
	}
	if st, statErr := os.Stat(abs); statErr != nil || !st.IsDir() {
		t.Fatalf("应已建出目标目录：%v", statErr)
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("应返回绝对路径：%q", abs)
	}
}

// 书架登记目录必须可注入：换书会往 books.json 写一笔，测试绝不能写进用户真实的书架。
// 这个测试同时是那条约定的护栏——有人把 registryDir 去掉就会红。
func TestBookRegistryHonorsInjectedDir(t *testing.T) {
	tmp := t.TempDir()
	m := Model{registryDir: tmp}
	if err := m.bookRegistry().Record(filepath.Join(tmp, "某书", "output", "novel")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "books.json")); err != nil {
		t.Fatalf("应写进注入的目录：%v", err)
	}
	books, err := m.bookRegistry().List()
	if err != nil || len(books) != 1 {
		t.Fatalf("注入的书架应只有这一条：%d %v", len(books), err)
	}
}

// sessionCmds 只能放绑 Host 的命令。UI 定时器靠处理器自我续期，切书后旧的那条仍然
// 活着并投递给新 Model，再从 sessionCmds 续一条，就会每切一次书多一个 350ms 定时器。
// 这个测试是那条约定的护栏：有人把 spinner 之类塞回 sessionCmds 就会红。
func TestSessionCmdsHoldOnlyHostBoundWork(t *testing.T) {
	m := NewModel(nil, "")
	if got := len(m.sessionCmds()); got != 5 {
		t.Fatalf("sessionCmds 应只有 5 条绑 Host 的命令（事件/完成/流式/快照/恢复），得到 %d 条——"+
			"新增的是绑 Host 的吗？与书无关的定时器请只放进 Init", got)
	}
	// Init 比 sessionCmds 多的是与书无关的：textarea.Blink 和 spinner（更新检查按配置可关）。
	withoutUpdateCheck := NewModel(nil, "")
	withoutUpdateCheck.disableUpdateCheck = true
	if withoutUpdateCheck.Init() == nil {
		t.Fatal("Init 应返回命令批次")
	}
}
