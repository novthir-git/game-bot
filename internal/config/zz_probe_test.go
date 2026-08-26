package config

import (
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T, localBody string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(gameDir(t), "config")
	dst := filepath.Join(dir, "config")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"device.yaml", "game.yaml", "tasks.yaml"} {
		data, _ := os.ReadFile(filepath.Join(src, f))
		os.WriteFile(filepath.Join(dst, f), data, 0o644)
	}
	os.WriteFile(filepath.Join(dst, "local.yaml"), []byte(localBody), 0o644)
	return dir
}

func TestProbeMapOverlay(t *testing.T) {
	dir := setup(t, "tasks:\n  flower_rack_cycle:\n    target_count: 200\n")
	b, err := Load(dir)
	if err != nil {
		t.Fatalf("Load err = %v", err)
	}
	tk := b.Tasks.Tasks["flower_rack_cycle"]
	t.Logf("flower_rack_cycle after overlay: %+v", tk)
	tk2 := b.Tasks.Tasks["pearl_harvest"]
	t.Logf("pearl_harvest (untouched): %+v", tk2)
}

func TestProbeEmptyLocal(t *testing.T) {
	dir := setup(t, "# 只有注释\n")
	_, err := Load(dir)
	t.Logf("comment-only local.yaml -> err = %v", err)

	dir2 := setup(t, "")
	_, err2 := Load(dir2)
	t.Logf("zero-byte local.yaml -> err = %v", err2)
}

func TestProbeAnchorsOverlay(t *testing.T) {
	dir := setup(t, "anchors:\n  daily_panel: \"x/y.png\"\n")
	b, err := Load(dir)
	t.Logf("anchors overlay -> err=%v anchors=%v", err, b.Game.Anchors)
}
