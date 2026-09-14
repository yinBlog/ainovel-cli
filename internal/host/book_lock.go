package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

const bookLockFile = ".ainovel.lock"

// ErrBookInUse 表示同一小说目录已被另一个进程占用。
var ErrBookInUse = errors.New("小说目录已被另一个 ainovel-cli 实例占用")

// bookLease 在 Host 的完整生命周期内持有小说目录的跨进程独占权。
// 锁文件会保留在目录中；真正的占用状态由操作系统管理，进程异常退出也会自动释放。
type bookLease struct {
	lock *flock.Flock
}

func acquireBookLease(dir string) (*bookLease, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("解析小说目录: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建小说目录: %w", err)
	}
	fileLock := flock.New(filepath.Join(absDir, bookLockFile), flock.SetPermissions(0o600))
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, closeBookLockAfterFailure(fileLock, fmt.Errorf("占用小说目录 %q: %w", absDir, err))
	}
	if !locked {
		return nil, closeBookLockAfterFailure(fileLock, fmt.Errorf(
			"%w：%s；请关闭正在操作此目录的另一个终端，或使用不同的小说目录",
			ErrBookInUse,
			absDir,
		))
	}
	return &bookLease{lock: fileLock}, nil
}

func closeBookLockAfterFailure(fileLock *flock.Flock, cause error) error {
	if err := fileLock.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("关闭小说目录锁: %w", err))
	}
	return cause
}

func (l *bookLease) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	err := l.lock.Close()
	l.lock = nil
	return err
}

// BookInUse 探测某个小说目录是否已被占用。只试锁、立刻释放，不改变任何状态，
// 供书架在列出时把"别的终端正在写"的书标灰——避免用户切过去才撞 ErrBookInUse。
//
// 两个必须知道的前提：
//  1. flock 是按打开的文件而不是按进程判定的，所以**当前进程自己正在写的那本书
//     也会返回 true**。调用方必须先排除当前这本，否则会把自己标成"使用中"。
//  2. 探测本身会短暂持有锁（微秒级）。这个窗口里另一个进程的 acquireBookLease
//     会失败。flock 没有"只读锁状态"的可移植做法，所以只在用户主动打开书架时
//     探一次，不要放进任何轮询路径。
func BookInUse(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	if _, err := os.Stat(absDir); err != nil {
		return false // 目录不存在：交给上层的“目录已不存在”提示，不是占用
	}
	fileLock := flock.New(filepath.Join(absDir, bookLockFile), flock.SetPermissions(0o600))
	locked, err := fileLock.TryLock()
	if err != nil {
		_ = fileLock.Close()
		return false // 探测失败不阻断用户操作，真正的冲突留给切换时的 acquireBookLease
	}
	if locked {
		_ = fileLock.Unlock()
	}
	_ = fileLock.Close()
	return !locked
}
