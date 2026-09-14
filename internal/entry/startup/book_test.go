package startup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// seedBook 造一个启动目录，带它自己的 per-book 配置覆盖。
func seedBook(t *testing.T, model string) string {
	t.Helper()
	launch := t.TempDir()
	if err := os.MkdirAll(filepath.Join(launch, ".ainovel"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":"relay","model":"` + model + `","providers":{"relay":{` +
		`"type":"openai","api_key":"k","base_url":"https://example.com/v1",` +
		`"models":[{"name":"` + model + `"}]}}}`
	if err := os.WriteFile(filepath.Join(launch, ".ainovel", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return launch
}

// 顺序切书的核心契约：每本书吃自己的 per-book 配置覆盖、产物落自己的目录，
// 关掉之后目录锁必须释放，这样才能来回切。
func TestOpenBookIsPerBookAndReleasesLock(t *testing.T) {
	build := buildversion.Info{Version: "test"}
	launchA := seedBook(t, "model-a")
	launchB := seedBook(t, "model-b")

	rtA, cfgA, err := OpenBook(launchA, build)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	if cfgA.ModelName != "model-a" {
		t.Fatalf("A 应吃自己的项目配置，得到 %q", cfgA.ModelName)
	}
	if cfgA.LaunchDir != launchA {
		t.Fatalf("A 的 LaunchDir = %q", cfgA.LaunchDir)
	}
	wantDirA := filepath.Join(launchA, "output", "novel")
	if rtA.Dir() != wantDirA {
		t.Fatalf("A 的产物目录 = %q，期望 %q", rtA.Dir(), wantDirA)
	}
	if !host.BookInUse(wantDirA) {
		t.Fatal("打开期间目录锁应被持有")
	}

	// 切到 B：先关 A 释放锁，再开 B。
	rtA.Close()
	if host.BookInUse(wantDirA) {
		t.Fatal("关掉之后目录锁必须释放，否则切不回来")
	}

	rtB, cfgB, err := OpenBook(launchB, build)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	if cfgB.ModelName != "model-b" {
		t.Fatalf("B 必须吃 B 自己的项目配置，得到 %q（串书了）", cfgB.ModelName)
	}
	if rtB.Dir() != filepath.Join(launchB, "output", "novel") {
		t.Fatalf("B 的产物目录 = %q", rtB.Dir())
	}
	rtB.Close()

	// 再切回 A：还能拿到锁，配置仍是 A 自己的。
	rtA2, cfgA2, err := OpenBook(launchA, build)
	if err != nil {
		t.Fatalf("reopen A: %v", err)
	}
	defer rtA2.Close()
	if cfgA2.ModelName != "model-a" {
		t.Fatalf("切回 A 后配置 = %q", cfgA2.ModelName)
	}
}

// 同一本书被占着时第二次打开必须失败，而不是两个进程同时写一本。
func TestOpenBookRefusesLockedBook(t *testing.T) {
	build := buildversion.Info{Version: "test"}
	launch := seedBook(t, "model-a")

	rt, _, err := OpenBook(launch, build)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rt.Close()

	if _, _, err := OpenBook(launch, build); err == nil {
		t.Fatal("同一本书不该被打开两次")
	}
}
