package tui

import (
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/library"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// Run 启动 TUI。
// 启动模式分层约定：
// 1. 快速模式、共创模式属于“启动编排”；
// 2. 正式创作会话进入 host.Host；
// 3. 未来若新增“续写已有小说”等共享模式，统一落到 internal/entry/startup。
func Run(cfg bootstrap.Config, bundle assets.Bundle, build buildversion.Info) error {
	rt, err := host.New(cfg, bundle, host.WithFileLog("tui.log", false,
		slog.String("version", build.Version),
		slog.String("commit", build.Commit),
		slog.String("built", build.Date),
	))
	if err != nil {
		return err
	}
	defer rt.Close()
	// 登记书架（~/.ainovel/books.json）：只记目录，书名进度在列出时现读。失败不影响创作。
	registry := library.DefaultRegistry(bootstrap.DefaultConfigDir())
	if err := registry.Record(rt.Dir()); err != nil {
		slog.Warn("书架登记失败", "module", "tui", "dir", rt.Dir(), "err", err)
	}

	m := NewModel(rt, build.Version)
	m.build = build // 会话内切书要用它重开新书的 Host
	m.disableUpdateCheck = cfg.DisableUpdateCheck
	// 欢迎页"最近的书"：启动时读一次书架，失败只记日志。
	if books, err := registry.List(); err != nil {
		slog.Warn("读取书架失败", "module", "tui", "err", err)
	} else {
		m.recentBooks = books
	}
	if logErr := rt.FileLogError(); logErr != nil {
		logWarning := fmt.Errorf("文件日志不可用，已继续使用终端日志：%w", logErr)
		m.err = logWarning
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: logWarning.Error(), Detail: logWarning.Error(),
		})
	}
	// 不在启动时全局开启鼠标上报：欢迎页用不到鼠标，关闭上报可保留终端原生
	// 拖拽选中复制。进入创作工作台（modeRunning）时再由 enterRunning 打开上报，
	// 以支持点击切面板 / 滚轮 / 拖拽侧边栏。
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
