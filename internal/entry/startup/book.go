package startup

import (
	"log/slog"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// OpenBook 按启动目录打开一本书：加载该书的 per-book 配置覆盖 → 归一化路径 →
// 加载该书的资产（含 <书目录>/style 的本书级文风覆盖）→ 建 Host（取目录独占锁）。
//
// 一本书的所有 per-book 状态（配置覆盖、规则目录、文风覆盖、产物目录、日志）都从
// launchDir 派生，所以运行中切书必须整条重来，不能只换产物目录——否则新书会继续
// 吃上一本的项目配置和文风。
//
// 失败时不会留下半开的 Host：host.New 失败会自己释放目录锁。
func OpenBook(launchDir string, build buildversion.Info) (*host.Host, bootstrap.Config, error) {
	cfg, err := bootstrap.LoadConfigFor(launchDir)
	if err != nil {
		return nil, cfg, err
	}
	cfg.FillDefaults()
	if err := cfg.ValidateBase(); err != nil {
		return nil, cfg, err
	}
	bundle := assets.Load(cfg.Style, assets.DefaultLoadOptions(cfg.OutputDir))
	rt, err := host.New(cfg, bundle, host.WithFileLog("tui.log", false,
		slog.String("version", build.Version),
		slog.String("commit", build.Commit),
		slog.String("built", build.Date),
	))
	if err != nil {
		return nil, cfg, err
	}
	return rt, cfg, nil
}
