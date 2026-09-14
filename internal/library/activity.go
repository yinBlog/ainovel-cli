package library

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
)

// ── 书架的成本与活跃度 ──
//
// 能在多本书之间切换之后，"该继续哪本"就成了每天要做的判断。光看书名和章数不够：
// 这本烧了多少钱、多久没动了、最近一周推进了几章，才是决定回哪本的依据。
//
// 只在书架列表（Registry.List）这条路径上算，不并进 inspect——/chapters、/search
// 这些面板不需要这些数字，不该替它们多付 IO。

// recentWindow 是"最近活跃"的统计窗口。一周是长篇写作的自然节奏单位。
const recentWindow = 7 * 24 * time.Hour

// Activity 是一本书的花费与活跃度。零值表示这本书还没有可统计的痕迹。
type Activity struct {
	CostUSD        float64   // 累计花费（meta/usage.json 的 overall）
	LastWrite      time.Time // 最近一次产出章节的时间
	RecentChapters int       // 最近 7 天新增的已完成章数
}

// Idle 返回距最近一次产出多久；从没写过返回 0 和 false。
func (a Activity) Idle() (time.Duration, bool) {
	if a.LastWrite.IsZero() {
		return 0, false
	}
	return time.Since(a.LastWrite), true
}

// LoadActivity 读取一本书的花费与活跃度。任何一项读不到都只是留零值——
// 书架是观察者，缺数据不该让整行报错。
func LoadActivity(dir string) Activity {
	var a Activity
	s := store.NewStore(dir)
	if state, err := s.Usage.Load(); err == nil && state != nil {
		a.CostUSD = state.Overall.Cost
	}
	if state, err := s.Usage.Load(); err == nil && state != nil {
		// 最近一次活动取用量的落盘时间：它覆盖所有 LLM 调用（写作、评审、返工），
		// 比章节文件时间更贴近"这本书多久没动了"。返工早期章节时章节文件时间会乱，
		// 用量时间不会。
		a.LastWrite = state.UpdatedAt
	}
	progress, err := s.Progress.Load()
	if err != nil {
		return a
	}
	newest, recent := chapterActivity(dir, progress.CompletedChapters)
	a.RecentChapters = recent
	if a.LastWrite.IsZero() {
		a.LastWrite = newest // 没有用量记录（导入来的书）时退回章节文件时间
	}
	return a
}

// chapterActivity 统计落在最近窗口内的已完成章数，并返回最新的章节文件时间。
//
// 全量 stat 而不是"倒序走到第一个超窗口的章就停"：章节大体按顺序写，但返工会把
// 早期章节的文件时间改新，提前停会把那次返工整个漏掉。一本 500 章的书就是 500 次
// stat，只在打开书架时算一次，值这个钱。
func chapterActivity(dir string, completed []int) (time.Time, int) {
	if len(completed) == 0 {
		return time.Time{}, 0
	}
	cutoff := time.Now().Add(-recentWindow)
	var newest time.Time
	recent := 0
	for _, ch := range completed {
		st, err := os.Stat(chapterFilePath(dir, ch))
		if err != nil {
			continue
		}
		mod := st.ModTime()
		if mod.After(newest) {
			newest = mod
		}
		if !mod.Before(cutoff) {
			recent++
		}
	}
	return newest, recent
}

// chapterFilePath 是章节终稿的落盘路径，与 store.DraftStore.SaveFinalChapter 一致。
func chapterFilePath(dir string, chapter int) string {
	return filepath.Join(dir, "chapters", fmt.Sprintf("%02d.md", chapter))
}
