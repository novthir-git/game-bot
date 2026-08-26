package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sample struct {
	Date string    `json:"date"`
	Done int       `json:"done"`
	At   time.Time `json:"at"`
}

func TestRoundTripAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sample{Date: "2026-08-26", Done: 42, At: time.Now().Truncate(time.Second).UTC()}
	if err := s.Set("rack", want); err != nil {
		t.Fatal(err)
	}

	// 重新打开，模拟进程重启
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var got sample
	ok, err := s2.Get("rack", &got)
	if err != nil || !ok {
		t.Fatalf("重开后读取失败: ok=%v err=%v", ok, err)
	}
	if got.Done != want.Done || got.Date != want.Date || !got.At.Equal(want.At) {
		t.Errorf("重开后状态不一致: %+v != %+v", got, want)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "never-written.json"))
	if err != nil {
		t.Fatalf("首次运行没有状态文件属于正常，不应报错: %v", err)
	}
	var v sample
	if ok, err := s.Get("nope", &v); ok || err != nil {
		t.Errorf("不存在的 key 应返回 (false, nil)，实得 (%v, %v)", ok, err)
	}
}

func TestCorruptFileReportsClearly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte("{ 这不是 JSON"), 0o644)
	if _, err := Open(path); err == nil {
		t.Fatal("损坏的状态文件应当报错")
	}
}

// 写入后不应残留临时文件，否则目录会越来越脏。
func TestNoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, _ := Open(path)
	if err := s.Set("k", sample{Done: 1}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("残留了临时文件: %s", e.Name())
		}
	}
}

func TestOverwriteKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	s.Set("a", sample{Done: 1})
	s.Set("b", sample{Done: 2})
	s.Set("a", sample{Done: 9})

	s2, _ := Open(path)
	var a, b sample
	s2.Get("a", &a)
	s2.Get("b", &b)
	if a.Done != 9 {
		t.Errorf("a 应被覆盖为 9，实得 %d", a.Done)
	}
	if b.Done != 2 {
		t.Errorf("覆盖 a 不应影响 b，b = %d", b.Done)
	}
}
