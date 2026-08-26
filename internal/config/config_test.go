package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这个测试直接加载仓库里真实的配置文件。
// 它的作用是把「YAML 写错字段名」和「Go 结构体改了字段但 YAML 没跟上」
// 这两类问题挡在提交之前，而不是等到连上模拟器才发现。
func gameDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "games", "my_garden_world"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadRealConfig(t *testing.T) {
	b, err := Load(gameDir(t))
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	if got := b.Device.Serial(); got != "127.0.0.1:7555" {
		t.Errorf("serial = %q，期望 127.0.0.1:7555", got)
	}
	if b.Device.Display.BaseWidth != 1280 || b.Device.Display.BaseHeight != 720 {
		t.Errorf("基准分辨率 = %dx%d，期望 1280x720",
			b.Device.Display.BaseWidth, b.Device.Display.BaseHeight)
	}
	if b.Game.OCR.Enabled {
		t.Error("本项目不含 OCR 实现，ocr.enabled 必须为 false")
	}
	if b.Game.Matching.DefaultThreshold != 0.78 {
		t.Errorf("默认阈值 = %v，期望 0.78", b.Game.Matching.DefaultThreshold)
	}
	if !b.Tasks.Scheduler.SingleAccountOnly {
		t.Error("single_account_only 必须为 true")
	}

	// P0 的三个任务应当默认开启
	for _, name := range []string{"flower_rack_cycle", "pearl_harvest", "waterwheel_collect"} {
		task, ok := b.Tasks.Tasks[name]
		if !ok {
			t.Errorf("tasks.yaml 缺少任务 %s", name)
			continue
		}
		if !task.Enabled {
			t.Errorf("任务 %s 应默认开启", name)
		}
		if task.Priority != 0 {
			t.Errorf("任务 %s 优先级 = %d，期望 0", name, task.Priority)
		}
	}
	// 广告任务必须保持关闭
	if b.Tasks.Tasks["watch_ads"].Enabled {
		t.Error("watch_ads 不应被开启：本项目不实现广告自动播放")
	}
	if lo, hi := b.Tasks.JitterRange(); lo <= 0 || hi <= lo {
		t.Errorf("idle_jitter_sec 区间不合法: %v ~ %v", lo, hi)
	}
	if _, err := os.Stat(b.TemplatesDir()); err != nil {
		t.Errorf("模板目录不存在: %v", err)
	}
}

// ocr.enabled 被误开时必须拒绝启动，而不是假装 OCR 在工作。
func TestOCREnabledRejected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(gameDir(t), "config")
	dst := filepath.Join(dir, "config")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"device.yaml", "game.yaml", "tasks.yaml"} {
		data, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dst, "local.yaml"),
		[]byte("ocr:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("ocr.enabled=true 时应当报错")
	}
}

// local.yaml 覆盖层要能改端口，且不因为字段不属于其他结构体而报错。
func TestLocalOverride(t *testing.T) {
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
	os.WriteFile(filepath.Join(dst, "local.yaml"),
		[]byte("adb:\n  port: 16384\n  binary: /usr/bin/adb\n"), 0o644)

	b, err := Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if b.Device.Serial() != "127.0.0.1:16384" {
		t.Errorf("覆盖后 serial = %q", b.Device.Serial())
	}
	if b.Device.ADB.Binary != "/usr/bin/adb" {
		t.Errorf("覆盖后 binary = %q", b.Device.ADB.Binary)
	}
	// 未在 local.yaml 中出现的字段应保持原值
	if b.Device.Display.BaseWidth != 1280 {
		t.Errorf("未覆盖字段被改动: base_width = %d", b.Device.Display.BaseWidth)
	}
}

// local.yaml 覆盖某个任务的一个字段时，该任务其余字段必须保持不变。
//
// 这是一次真实缺陷的回归测试：yaml.v3 解码 map 的 value 时会新建零值
// 整体替换那条 entry，导致 local.yaml 里只写 target_count 就把
// enabled 清成了 false——任务被静默跳过，全程没有任何报错。
func TestLocalOverrideKeepsOtherTaskFields(t *testing.T) {
	dir := copyConfig(t)
	os.WriteFile(filepath.Join(dir, "config", "local.yaml"),
		[]byte("tasks:\n  flower_rack_cycle:\n    target_count: 200\n"), 0o644)

	b, err := Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	got := b.Tasks.Tasks["flower_rack_cycle"]
	if got.TargetCount != 200 {
		t.Errorf("target_count 应被覆盖为 200，实际 %d", got.TargetCount)
	}
	if !got.Enabled {
		t.Error("enabled 被覆盖操作清零了：任务会被静默跳过")
	}
	if got.RelistIntervalSec != 255 {
		t.Errorf("relist_interval_sec 应保持 255，实际 %d", got.RelistIntervalSec)
	}
	if !got.ResetDaily {
		t.Error("reset_daily 应保持 true")
	}
	if !got.PreferHighestPrice {
		t.Error("prefer_highest_price 应保持 true")
	}
	// 未被提及的任务完全不受影响
	if p := b.Tasks.Tasks["pearl_harvest"]; !p.Enabled || p.IntervalSec != 7200 {
		t.Errorf("未被覆盖的任务受到了影响: %+v", p)
	}
}

// local.yaml 走宽松解码，写错的字段名不会报错只会被忽略，
// 这是最难排查的一类配置问题，必须在加载时拦下来。
func TestLocalOverrideRejectsUnknownKey(t *testing.T) {
	dir := copyConfig(t)
	os.WriteFile(filepath.Join(dir, "config", "local.yaml"),
		[]byte("adb:\n  prot: 16384\n"), 0o644) // port 拼成了 prot

	_, err := Load(dir)
	if err == nil {
		t.Fatal("local.yaml 里的拼写错误应当被拦下")
	}
	if !strings.Contains(err.Error(), "adb.prot") {
		t.Errorf("报错信息应指出具体是哪个键: %v", err)
	}
}

// 合法的跨文件覆盖不应被上一条的检查误伤。
func TestLocalOverrideAcrossFiles(t *testing.T) {
	dir := copyConfig(t)
	os.WriteFile(filepath.Join(dir, "config", "local.yaml"), []byte(
		"adb:\n  port: 16384\nmatching:\n  default_threshold: 0.85\n"+
			"tasks:\n  pearl_harvest:\n    interval_sec: 3600\n"), 0o644)

	b, err := Load(dir)
	if err != nil {
		t.Fatalf("跨文件覆盖不应报错: %v", err)
	}
	if b.Device.ADB.Port != 16384 {
		t.Errorf("device 侧覆盖失效: %d", b.Device.ADB.Port)
	}
	if b.Game.Matching.DefaultThreshold != 0.85 {
		t.Errorf("game 侧覆盖失效: %v", b.Game.Matching.DefaultThreshold)
	}
	p := b.Tasks.Tasks["pearl_harvest"]
	if p.IntervalSec != 3600 || !p.Enabled {
		t.Errorf("tasks 侧覆盖失效: %+v", p)
	}
}

func copyConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(gameDir(t), "config")
	dst := filepath.Join(dir, "config")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"device.yaml", "game.yaml", "tasks.yaml"} {
		data, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
