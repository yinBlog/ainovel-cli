package library

import (
	"os"
	"path/filepath"
	"strings"
)

// DisplayPath 把家目录前缀缩写为 "~"，供表格与面板展示；不改变语义，只省宽度。
func DisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = filepath.Clean(home)
	clean := filepath.Clean(path)
	if clean == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(clean, prefix) || (filepath.Separator == '\\' && strings.HasPrefix(strings.ToLower(clean), strings.ToLower(prefix))) {
		return "~" + string(filepath.Separator) + clean[len(prefix):]
	}
	return path
}
