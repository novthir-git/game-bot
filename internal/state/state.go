// Package state 为任务提供跨进程重启的持久化状态。
//
// 存在的理由很具体：花架循环要连续跑九个多小时，期间进程可能因为
// 断线重连、手动重启、机器休眠而中断。若计数只存在内存里，
// 每次重启都从零开始，这个任务就永远刷不满。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store 是一个极简的 JSON 键值存储，每次写入都整体落盘。
//
// 没有做增量写或缓冲：状态量只有几十字节，写入频率是分钟级，
// 为此引入复杂度不划算。反过来，每次都完整落盘意味着任何时刻
// 断电都不会留下写了一半的状态。
type Store struct {
	path string

	mu   sync.Mutex
	data map[string]json.RawMessage
}

// Open 打开（或新建）状态文件。文件不存在不算错误——首次运行本来就没有状态。
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: make(map[string]json.RawMessage)}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读取状态文件 %s: %w", path, err)
	}
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		// 状态文件损坏时不要让整个程序起不来：它只是进度记录，
		// 大不了从头开始，比彻底跑不起来强。
		return nil, fmt.Errorf("状态文件 %s 已损坏（可直接删除后重跑）: %w", path, err)
	}
	return s, nil
}

// Path 返回状态文件路径。
func (s *Store) Path() string { return s.path }

// Get 把 key 对应的值解码进 v，返回该 key 是否存在。
func (s *Store) Get(key string, v any) (bool, error) {
	s.mu.Lock()
	raw, ok := s.data[key]
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, fmt.Errorf("解析状态 %q: %w", key, err)
	}
	return true, nil
}

// Set 写入 key 并立即落盘。
func (s *Store) Set(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("编码状态 %q: %w", key, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = raw
	return s.flushLocked()
}

// flushLocked 先写临时文件再改名。
// 直接覆写原文件的话，进程在写入途中被杀会留下一个半截的 JSON，
// 下次启动就连不上状态了。
func (s *Store) flushLocked() error {
	out, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("写入状态文件: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("提交状态文件: %w", err)
	}
	return nil
}
