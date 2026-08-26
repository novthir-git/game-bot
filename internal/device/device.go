package device

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/vision"
)

// Device 是一台模拟器实例。
//
// 所有对外坐标都是「基准坐标」（config 里的 display.base_width/height，默认 1280x720），
// 内部再换算到设备实际分辨率。这样模板图和任务代码里的坐标只需要一套，
// 换分辨率时不必重截所有模板。
type Device struct {
	adb *ADB
	cfg *config.Device
	log *slog.Logger

	realW, realH int

	mu          sync.Mutex
	lastCapture time.Time
}

// Open 连接模拟器并探测实际分辨率。
func Open(ctx context.Context, cfg *config.Device, log *slog.Logger) (*Device, error) {
	adb, err := NewADB(cfg.ADB.Binary, cfg.Serial(), cfg.ConnectTimeout())
	if err != nil {
		return nil, err
	}
	if !adb.UsingConfiguredBinary(cfg.ADB.Binary) {
		log.Warn("未使用配置指定的 adb，已回退到 PATH 中的版本；"+
			"若连接反复掉线，请把 MuMu 自带的 adb 路径写进 config/local.yaml",
			"配置路径", cfg.ADB.Binary, "实际使用", adb.Binary())
	}
	if err := adb.Connect(ctx); err != nil {
		return nil, err
	}

	d := &Device{adb: adb, cfg: cfg, log: log}
	if err := d.detectSize(ctx); err != nil {
		return nil, err
	}
	if d.realW != cfg.Display.BaseWidth || d.realH != cfg.Display.BaseHeight {
		log.Warn("模拟器分辨率与模板基准不一致，截图会被缩放后再匹配；"+
			"建议在 MuMu「设置-显示」里改成手机版基准分辨率以获得最佳识别率",
			"实际", fmt.Sprintf("%dx%d", d.realW, d.realH),
			"基准", fmt.Sprintf("%dx%d", cfg.Display.BaseWidth, cfg.Display.BaseHeight))
	}
	return d, nil
}

// ADB 暴露底层控制器，供 doctor 这类诊断命令使用。
func (d *Device) ADB() *ADB { return d.adb }

// RealSize 返回设备实际分辨率。
func (d *Device) RealSize() (int, int) { return d.realW, d.realH }

// BaseSize 返回模板基准分辨率。
func (d *Device) BaseSize() (int, int) {
	return d.cfg.Display.BaseWidth, d.cfg.Display.BaseHeight
}

var sizeRe = regexp.MustCompile(`(\d+)x(\d+)`)

func (d *Device) detectSize(ctx context.Context) error {
	out, err := d.adb.Shell(ctx, "wm", "size")
	if err != nil {
		return fmt.Errorf("读取屏幕尺寸: %w", err)
	}
	// 输出形如：
	//   Physical size: 1280x720
	//   Override size: 720x1280      <- 存在时以它为准
	var phys, override string
	for _, line := range strings.Split(string(out), "\n") {
		m := sizeRe.FindString(line)
		if m == "" {
			continue
		}
		if strings.Contains(line, "Override") {
			override = m
		} else if strings.Contains(line, "Physical") {
			phys = m
		}
	}
	pick := override
	if pick == "" {
		pick = phys
	}
	if pick == "" {
		return fmt.Errorf("无法从 `wm size` 输出解析分辨率: %q", strings.TrimSpace(string(out)))
	}
	parts := strings.SplitN(pick, "x", 2)
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	if w <= 0 || h <= 0 {
		return fmt.Errorf("解析出的分辨率不合法: %s", pick)
	}
	d.realW, d.realH = w, h
	return nil
}

// toReal 把基准坐标换算成设备实际坐标。
func (d *Device) toReal(x, y int) (int, int) {
	bw, bh := d.BaseSize()
	if bw == d.realW && bh == d.realH {
		return x, y
	}
	return x * d.realW / bw, y * d.realH / bh
}

// Screencap 截一帧，并归一化到基准分辨率。
//
// 两次截图之间会补足 capture.min_interval_ms 的间隔——不是返回缓存的旧帧，
// 而是真的等到间隔满足再截。返回陈旧画面会让「点击后确认结果」这类判断出错，
// 是排查起来最痛苦的一类 bug。
func (d *Device) Screencap(ctx context.Context) (*vision.Frame, error) {
	d.mu.Lock()
	if wait := d.cfg.MinCaptureInterval() - time.Since(d.lastCapture); wait > 0 {
		d.mu.Unlock()
		if err := sleepCtx(ctx, wait); err != nil {
			return nil, err
		}
		d.mu.Lock()
	}
	d.lastCapture = time.Now()
	d.mu.Unlock()

	raw, err := d.adb.ExecOut(ctx, "screencap")
	if err != nil {
		return nil, fmt.Errorf("截图: %w", err)
	}
	f, err := DecodeScreencap(raw)
	if err != nil {
		return nil, err
	}
	bw, bh := d.BaseSize()
	return f.ResizeTo(bw, bh), nil
}

// Tap 点击基准坐标 (x,y)。
//
// 会加入 input.tap_jitter_px 范围内的随机抖动：连续几百次点在同一个像素上，
// 一旦那个像素恰好压在按钮边界，就会稳定地点空；抖动几个像素能让它落回按钮内部。
func (d *Device) Tap(ctx context.Context, x, y int) error {
	if j := d.cfg.Input.TapJitterPx; j > 0 {
		x += rand.IntN(2*j+1) - j
		y += rand.IntN(2*j+1) - j
	}
	rx, ry := d.toReal(x, y)
	if _, err := d.adb.Shell(ctx, "input", "tap", strconv.Itoa(rx), strconv.Itoa(ry)); err != nil {
		return fmt.Errorf("点击 (%d,%d): %w", x, y, err)
	}
	d.log.Debug("点击", "x", x, "y", y)
	return sleepCtx(ctx, d.cfg.PostClickDelay())
}

// TapRect 点击矩形中心，通常直接传模板匹配的结果。
func (d *Device) TapRect(ctx context.Context, r vision.Rect) error {
	x, y := r.Center()
	return d.Tap(ctx, x, y)
}

// Swipe 从 (x1,y1) 滑到 (x2,y2)，坐标为基准坐标。
func (d *Device) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration) error {
	rx1, ry1 := d.toReal(x1, y1)
	rx2, ry2 := d.toReal(x2, y2)
	ms := int(dur.Milliseconds())
	if ms <= 0 {
		ms = 300
	}
	_, err := d.adb.Shell(ctx, "input", "swipe",
		strconv.Itoa(rx1), strconv.Itoa(ry1), strconv.Itoa(rx2), strconv.Itoa(ry2), strconv.Itoa(ms))
	if err != nil {
		return fmt.Errorf("滑动: %w", err)
	}
	return sleepCtx(ctx, d.cfg.PostClickDelay())
}

// Back 按下返回键。
func (d *Device) Back(ctx context.Context) error {
	if _, err := d.adb.Shell(ctx, "input", "keyevent", "KEYCODE_BACK"); err != nil {
		return fmt.Errorf("返回键: %w", err)
	}
	return sleepCtx(ctx, d.cfg.PostClickDelay())
}

var focusRe = regexp.MustCompile(`([A-Za-z0-9_.]+)/([A-Za-z0-9_.$]+)`)

// CurrentFocus 返回当前前台的「包名/Activity」。
//
// 这是采集 game.yaml 里 app.package 和 app.main_activity 的正道：
// 手动把游戏切到前台，跑一次 `gardenbot doctor`，输出里就有。
func (d *Device) CurrentFocus(ctx context.Context) (pkg, activity string, err error) {
	out, err := d.adb.Shell(ctx, "dumpsys", "window")
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "mCurrentFocus") && !strings.Contains(line, "mFocusedApp") {
			continue
		}
		if m := focusRe.FindStringSubmatch(line); m != nil {
			return m[1], m[2], nil
		}
	}
	return "", "", fmt.Errorf("未能从 dumpsys window 中解析出前台应用（屏幕是否处于锁定状态？）")
}

// IsForeground 判断指定包名是否在前台。
func (d *Device) IsForeground(ctx context.Context, pkg string) (bool, error) {
	if pkg == "" {
		return false, fmt.Errorf("包名为空：请先用 `gardenbot doctor` 采集并填入 game.yaml 的 app.package")
	}
	cur, _, err := d.CurrentFocus(ctx)
	if err != nil {
		return false, err
	}
	return cur == pkg, nil
}

// LaunchApp 启动游戏。activity 为空时用 monkey 触发默认启动入口。
func (d *Device) LaunchApp(ctx context.Context, pkg, activity string) error {
	if pkg == "" {
		return fmt.Errorf("包名为空：请先用 `gardenbot doctor` 采集并填入 game.yaml 的 app.package")
	}
	var err error
	if activity != "" {
		_, err = d.adb.Shell(ctx, "am", "start", "-n", pkg+"/"+activity)
	} else {
		_, err = d.adb.Shell(ctx, "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	}
	if err != nil {
		return fmt.Errorf("启动 %s: %w", pkg, err)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
