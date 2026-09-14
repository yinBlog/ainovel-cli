package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RegistryFileName 是书架登记文件名，落在 ~/.ainovel/ 下。
const RegistryFileName = "books.json"

// Registry 记录本机开过的小说目录。它只是"最近打开"式的索引：
// 每次入口层拿到小说目录租约后登记一次；书名、进度等全部在列出时从各目录现读，
// 不在登记文件里缓存，避免陈旧。
type Registry struct {
	path string
}

// NewRegistry 用给定文件路径构造登记表。path 为空时登记表退化为空操作（无家目录环境）。
func NewRegistry(path string) *Registry {
	return &Registry{path: path}
}

// DefaultRegistry 返回 <configDir>/books.json 上的登记表；configDir 为空（取不到家目录）
// 时返回空操作登记表，调用方无需再判空。
func DefaultRegistry(configDir string) *Registry {
	if strings.TrimSpace(configDir) == "" {
		return NewRegistry("")
	}
	return NewRegistry(filepath.Join(configDir, RegistryFileName))
}

// Entry 是登记表中的一条记录。
type Entry struct {
	Dir        string    `json:"dir"`
	LastOpened time.Time `json:"last_opened"`
}

// Book 是 List 的输出：登记信息 + 现场读取的概览。
// Info 为 nil 表示该目录尚未开书或已不存在（Missing 区分两者）。
type Book struct {
	Entry
	Info     *BookInfo
	Activity Activity // 花费与活跃度，只在书架这条路径上读
	Missing  bool
	Err      error // 读取概览时的非"未开书"错误（如 progress.json 损坏），仅用于展示
}

type registryFile struct {
	Version int     `json:"version"`
	Books   []Entry `json:"books"`
}

const registryVersion = 1

// Record 登记（或刷新）一个小说目录的最近打开时间。目录规范化为绝对路径。
func (r *Registry) Record(dir string) error {
	if r == nil || r.path == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	entries, err := r.load()
	if err != nil {
		return err
	}
	now := time.Now()
	found := false
	for i := range entries {
		if sameDir(entries[i].Dir, abs) {
			entries[i].Dir = abs
			entries[i].LastOpened = now
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, Entry{Dir: abs, LastOpened: now})
	}
	return r.save(entries)
}

// Forget 从登记表移除一个目录（不动磁盘上的小说数据）。
func (r *Registry) Forget(dir string) error {
	if r == nil || r.path == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	entries, err := r.load()
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if !sameDir(e.Dir, abs) {
			kept = append(kept, e)
		}
	}
	return r.save(kept)
}

// Entries 返回原始登记项，按最近打开时间倒序。
func (r *Registry) Entries() ([]Entry, error) {
	if r == nil || r.path == "" {
		return nil, nil
	}
	entries, err := r.load()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].LastOpened.After(entries[j].LastOpened) })
	return entries, nil
}

// List 返回书架：每个登记目录现场读取概览。目录不存在标 Missing；
// 目录存在但还没开书（欢迎页退出）Info 为 nil 且 Missing 为 false。
func (r *Registry) List() ([]Book, error) {
	entries, err := r.Entries()
	if err != nil {
		return nil, err
	}
	books := make([]Book, 0, len(entries))
	for _, e := range entries {
		b := Book{Entry: e}
		if st, err := os.Stat(e.Dir); err != nil || !st.IsDir() {
			b.Missing = true
		} else if info, err := Inspect(e.Dir); err == nil {
			b.Info = info
			b.Activity = LoadActivity(e.Dir)
		} else if !errors.Is(err, ErrNotBook) {
			b.Err = err
		}
		books = append(books, b)
	}
	return books, nil
}

func (r *Registry) load() ([]Entry, error) {
	data, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取书架登记 %s：%w", r.path, err)
	}
	var f registryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析书架登记 %s：%w", r.path, err)
	}
	return f.Books, nil
}

// save 原子写入：tmp + rename，与 store 单文件写语义一致。
func (r *Registry) save(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("创建书架登记目录：%w", err)
	}
	data, err := json.MarshalIndent(registryFile{Version: registryVersion, Books: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), filepath.Base(r.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, r.path)
}

// sameDir 比较两个路径是否指向同一目录：先做 Clean，再在大小写不敏感的
// 文件系统（Windows）上忽略大小写。
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return false
}
