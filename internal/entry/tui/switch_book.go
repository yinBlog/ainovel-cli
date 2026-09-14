package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/library"
)

// 会话内换书。一个进程同一时刻仍然只服务一本书（架构 §10 禁的是进程内并发多书：
// 并行 worker、多个 Engine 循环），这里是顺序切换：停掉当前这本、释放目录锁，
// 再把整个会话重建到另一本上。checkpoint 保证停机无损，切回来能原地续上。

// switchBook 把整个会话切到另一本书。bookDir 是小说目录（<启动目录>/output/novel）。
//
// 顺序刻意是"先验证目标 → 再关旧 → 最后开新"：目录锁和文件日志都是进程级独占的，
// 新旧 Host 不能并存，所以关旧之后才能开新；而关旧是不可逆的，所以凡是能提前查的
// （目录合法性、被别的进程占用、配置能不能读）都在关旧之前查完。
// 万一新书仍然开不起来，就地把旧书重新打开——会话不能停在"没有书"的状态。
func (m Model) switchBook(bookDir string) (tea.Model, tea.Cmd) {
	if m.runtime != nil && m.runtime.Dir() == bookDir {
		m.library = nil
		return m, m.textarea.Focus() // 已经在这本书上
	}
	launchDir := library.LaunchDirOf(bookDir)
	if launchDir == "" {
		return m.libraryError("不是标准小说目录：" + library.DisplayPath(bookDir))
	}
	return m.openBookAt(launchDir, bookDir)
}

// startNewBook 在一个还没有书的目录上开一本新书，并把会话切过去。
//
// 这是 switchBook 的另一半：能切到已有的书，也得能新建一本，否则"开新书"还是只能
// 退出进程、mkdir、cd、重启。目录不存在就建；已经是一本书则拒绝——覆盖别人的书
// 是不可逆的，那种情况用户要的是 /books 切过去。
//
// 开完落在新书的欢迎页：新 Host 没有进度，Resume 返回空，Model 停在 modeNew，
// 直接输创作需求即可。
func (m Model) startNewBook(launchDir string) (tea.Model, tea.Cmd) {
	abs, err := prepareNewBookDir(launchDir)
	if err != nil {
		return m.libraryError(err.Error())
	}
	return m.openBookAt(abs, filepath.Join(abs, filepath.FromSlash(library.DefaultBookSubdir)))
}

// prepareNewBookDir 校验并准备新书的启动目录，返回绝对路径。
//
// 从 startNewBook 拆出来是为了能单独测：完整流程会真的开一个 Host，那会读用户的
// 真实配置、占目录锁、并把目录登记进书架——测试不该碰这些。
func prepareNewBookDir(launchDir string) (string, error) {
	launchDir = strings.TrimSpace(launchDir)
	if launchDir == "" {
		return "", errors.New("用法：/newbook <目录路径>")
	}
	abs, err := filepath.Abs(launchDir)
	if err != nil {
		return "", fmt.Errorf("解析目录失败：%w", err)
	}
	if existing, err := library.ResolveBookDir(abs); err == nil {
		return "", fmt.Errorf("这个目录已经有一本书了，用 /books 切过去：%s", library.DisplayPath(existing))
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败：%w", err)
	}
	return abs, nil
}

// openBookAt 是换书与新建书的共同尾段：验证目标 → 停当前并释放锁 → 开新的 → 重建会话。
func (m Model) openBookAt(launchDir, bookDir string) (tea.Model, tea.Cmd) {
	if host.BookInUse(bookDir) {
		return m.libraryError("这本书正被另一个 ainovel-cli 占用：" + library.DisplayPath(bookDir))
	}
	// 目标配置先跑一遍：它坏掉不该连累当前这本被关。
	if _, err := bootstrap.LoadConfigFor(launchDir); err != nil {
		return m.libraryError("读取该书配置失败：" + err.Error())
	}

	oldBookDir, leaving := "", ""
	if m.runtime != nil {
		oldBookDir = m.runtime.Dir()
		// 引擎在跑时切书会中断当前这一章。checkpoint 保证无损，但用户得知道
		// 自己刚刚把什么停下了——旧书的事件流随 Close 一起没了，所以先把话留下来。
		if m.snapshot.IsRunning {
			leaving = bookLabel(m.snapshot.BookTitle, oldBookDir)
		}
		m.runtime.Close() // 停引擎（checkpoint 无损）、释放目录锁、收掉文件日志
	}

	rt, cfg, err := startup.OpenBook(launchDir, m.build)
	if err != nil {
		return m.reopenAfterFailedSwitch(oldBookDir, "打开 "+library.DisplayPath(bookDir)+" 失败："+err.Error())
	}
	next, cmd := m.adoptBook(rt, cfg)
	if leaving == "" {
		return next, cmd
	}
	model := next.(Model)
	model.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Level: "warn",
		Summary: "已暂停《" + leaving + "》并切到本书；原书进度已存档，切回即可续写",
	})
	model.refreshEventViewport()
	return model, cmd
}

// bookLabel 优先用书名，没有书名就退回目录名——用户得认得出自己刚停下的是哪本。
func bookLabel(title, bookDir string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return library.DisplayPath(bookDir)
}

// reopenAfterFailedSwitch 在新书开不起来时把旧书原样开回来。旧书的锁刚被自己释放，
// 正常情况下一定能拿回来；真拿不回来才让会话停下并把两条错误都摆给用户。
func (m Model) reopenAfterFailedSwitch(oldBookDir, reason string) (tea.Model, tea.Cmd) {
	// 旧书的启动目录必须能算出来才敢重开：LaunchDirOf 返回空串时 OpenBook("") 会去开
	// **当前工作目录**那本书，那是另一本书，比停在没有书的状态更糟。
	oldLaunchDir := library.LaunchDirOf(oldBookDir)
	if oldBookDir == "" || oldLaunchDir == "" {
		m.runtime = nil
		return m.libraryError(reason)
	}
	rt, cfg, err := startup.OpenBook(oldLaunchDir, m.build)
	if err != nil {
		m.runtime = nil
		return m.libraryError(reason + "；重开原书也失败：" + err.Error())
	}
	next, cmd := m.adoptBook(rt, cfg)
	model := next.(Model)
	model.err = errors.New(reason + "；已留在原书")
	return model, cmd
}

// adoptBook 用新 Host 重建整个会话。
//
// 重建而不是逐字段重置：Model 有几十个字段绑在当前 Host 上（事件流、各面板、导入
// 提示、流式缓冲、快照），漏掉任何一个都会把上一本书的状态串进新书。这里只把与书
// 无关的环境搬过去——终端尺寸、书架、版本号、更新检查开关。
func (m Model) adoptBook(rt *host.Host, cfg bootstrap.Config) (tea.Model, tea.Cmd) {
	next := NewModel(rt, m.version)
	next.build = m.build
	next.disableUpdateCheck = cfg.DisableUpdateCheck
	next.width, next.height = m.width, m.height

	next.registryDir = m.registryDir
	// 书架登记新书的最近打开时间，并刷新欢迎页的"最近的书"。失败不影响创作。
	registry := next.bookRegistry()
	_ = registry.Record(rt.Dir())
	if books, err := registry.List(); err == nil {
		next.recentBooks = books
	} else {
		next.recentBooks = m.recentBooks
	}

	cmds := next.sessionCmds()
	// 各 viewport 在 NewModel 里是默认尺寸，补一条窗口尺寸消息让常规布局路径把它们
	// 重新排一遍，而不是在这里复制一份布局逻辑。
	if m.width > 0 && m.height > 0 {
		cmds = append(cmds, syntheticResize(m.width, m.height))
	}
	cmds = append(cmds, next.textarea.Focus())
	return next, tea.Batch(cmds...)
}

func syntheticResize(width, height int) tea.Cmd {
	return func() tea.Msg { return tea.WindowSizeMsg{Width: width, Height: height} }
}

// bookRegistry 返回本会话使用的书架登记表。registryDir 只在测试里被指到临时目录，
// 生产路径永远落在全局 ~/.ainovel。
func (m Model) bookRegistry() *library.Registry {
	dir := m.registryDir
	if dir == "" {
		dir = bootstrap.DefaultConfigDir()
	}
	return library.DefaultRegistry(dir)
}
