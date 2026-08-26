package vision

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Store 按名字加载并缓存模板图。
//
// 名字即相对 root 的路径，例如 "common/btn_close.png"。
// 模板一旦加载就常驻内存——本游戏总共只有几十张小图，占用可以忽略，
// 而每次匹配都去读盘会让长时间运行的进程无谓地反复触碰磁盘。
type Store struct {
	root string

	mu    sync.RWMutex
	cache map[string]*Gray
}

func NewStore(root string) *Store {
	return &Store{root: root, cache: make(map[string]*Gray)}
}

func (s *Store) Root() string { return s.root }

// Get 返回模板的灰度图。首次调用时读盘，之后走缓存。
func (s *Store) Get(name string) (*Gray, error) {
	s.mu.RLock()
	g, ok := s.cache[name]
	s.mu.RUnlock()
	if ok {
		return g, nil
	}

	path := filepath.Join(s.root, filepath.FromSlash(name))
	f, err := LoadPNG(path)
	if err != nil {
		return nil, fmt.Errorf("加载模板 %s: %w", name, err)
	}
	g = f.Gray()

	s.mu.Lock()
	s.cache[name] = g
	s.mu.Unlock()
	return g, nil
}

// Missing 返回 names 中在磁盘上不存在的那些。
// 用于启动前一次性报告「还缺哪些模板图」，而不是等任务跑到一半才失败。
func (s *Store) Missing(names []string) []string {
	var out []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(n))); err != nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// List 列出 root 下所有 .png 模板，路径以斜杠分隔。
func (s *Store) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".png") {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}
