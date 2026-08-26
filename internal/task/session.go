// Package task 提供任务运行时：识别原语、调度器、异常恢复。
package task

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/device"
	"github.com/novthir-git/game-bot/internal/state"
	"github.com/novthir-git/game-bot/internal/vision"
)

// 约定俗成的通用模板名。放常量而不是散落在各处的字符串，
// 便于 doctor 一次性检查缺哪些图。
const (
	TplClose = "common/btn_close.png" // 弹窗右上角的关闭按钮
	TplBack  = "common/btn_back.png"  // 界面内的返回按钮
)

// Session 是任务运行时的上下文。
//
// 它只会被调度器的单一循环使用，因此内部状态无需加锁；
// 反过来说也不要把 Session 拿到别的 goroutine 里去用。
type Session struct {
	Dev *device.Device
	Tpl *vision.Store
	Cfg *config.Bundle
	Log *slog.Logger

	// State 持久化任务进度，可以为 nil（例如 doctor 只做检查，不需要状态）。
	// 任务读写前必须判空。
	State *state.Store

	missStreak int // 连续找不到目标的次数，用于触发恢复流程
}

func NewSession(dev *device.Device, tpl *vision.Store, cfg *config.Bundle, st *state.Store, log *slog.Logger) *Session {
	return &Session{Dev: dev, Tpl: tpl, Cfg: cfg, State: st, Log: log}
}

// FindOpt 是查找选项。
type FindOpt func(*vision.Options)

// WithROI 限定搜索区域。已知目标大致位置时务必带上，能快十几倍，
// 也能避免在别处误命中长得像的元素。
func WithROI(r vision.Rect) FindOpt {
	return func(o *vision.Options) { o.ROI = &r }
}

// WithThreshold 为单次查找覆盖默认阈值。
// 只在个别模板确实不稳时使用；不要去调全局阈值，那会引发大面积误匹配。
func WithThreshold(t float64) FindOpt {
	return func(o *vision.Options) { o.Threshold = t }
}

func (s *Session) options(opts []FindOpt) vision.Options {
	o := vision.Options{Threshold: s.Cfg.Game.Matching.DefaultThreshold}
	for _, f := range opts {
		f(&o)
	}
	return o
}

// Capture 截取一帧（已归一化到基准分辨率）。
//
// 需要在同一画面上判断多个元素时，取一帧后反复调用 FindOn，
// 而不是连着调好几次 Find——后者每次都会重新截图，既慢又可能截到不同时刻的画面。
func (s *Session) Capture(ctx context.Context) (*vision.Frame, error) {
	return s.Dev.Screencap(ctx)
}

// FindOn 在给定画面中查找模板。
func (s *Session) FindOn(f *vision.Frame, name string, opts ...FindOpt) (vision.Match, bool, error) {
	ndl, err := s.Tpl.Get(name)
	if err != nil {
		return vision.Match{}, false, err
	}
	m, ok := vision.Find(f.Gray(), ndl, s.options(opts))
	return m, ok, nil
}

// Find 截图并查找模板。
func (s *Session) Find(ctx context.Context, name string, opts ...FindOpt) (vision.Match, bool, error) {
	f, err := s.Capture(ctx)
	if err != nil {
		return vision.Match{}, false, err
	}
	m, ok, err := s.FindOn(f, name, opts...)
	if err != nil {
		return m, false, err
	}
	if ok {
		s.missStreak = 0
	} else {
		s.missStreak++
	}
	return m, ok, nil
}

// Click 找到目标就点它的中心，返回是否点到。
func (s *Session) Click(ctx context.Context, name string, opts ...FindOpt) (bool, error) {
	m, ok, err := s.Find(ctx, name, opts...)
	if err != nil || !ok {
		return false, err
	}
	s.Log.Debug("命中并点击", "模板", name, "分数", fmt.Sprintf("%.3f", m.Score))
	return true, s.Dev.TapRect(ctx, m.Rect)
}

// WaitFor 轮询等待目标出现。
func (s *Session) WaitFor(ctx context.Context, name string, timeout time.Duration, opts ...FindOpt) (vision.Match, bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		m, ok, err := s.Find(ctx, name, opts...)
		if err != nil {
			return m, false, err
		}
		if ok {
			return m, true, nil
		}
		if time.Now().After(deadline) {
			return m, false, nil
		}
		if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
			return m, false, err
		}
	}
}

// WaitVanish 轮询等待目标消失，用于等待动画或过场结束。
func (s *Session) WaitVanish(ctx context.Context, name string, timeout time.Duration, opts ...FindOpt) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		_, ok, err := s.Find(ctx, name, opts...)
		if err != nil {
			return false, err
		}
		if !ok {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
			return false, err
		}
	}
}

// ClickAndWait 点击一个目标，然后等待另一个目标出现，是最常用的一步转移。
func (s *Session) ClickAndWait(ctx context.Context, click, expect string, timeout time.Duration) (bool, error) {
	ok, err := s.Click(ctx, click)
	if err != nil || !ok {
		return false, err
	}
	_, ok, err = s.WaitFor(ctx, expect, timeout)
	return ok, err
}

// AtScreen 判断当前是否处于某个界面，界面名取自 game.yaml 的 anchors。
func (s *Session) AtScreen(ctx context.Context, screen string) (bool, error) {
	tpl, ok := s.Cfg.Game.Anchors[screen]
	if !ok {
		return false, fmt.Errorf("game.yaml 的 anchors 里没有界面 %q", screen)
	}
	_, hit, err := s.Find(ctx, tpl)
	return hit, err
}

// ShouldRecover 报告连续找不到目标的次数是否已达到恢复阈值。
func (s *Session) ShouldRecover() bool {
	return s.missStreak >= s.Cfg.Game.Recovery.MaxMissBeforeRecover
}

// Recover 执行 game.yaml 里配置的恢复步骤，直到回到主界面。
//
// 游戏会在任意时刻弹活动、公告、礼包，长时间无人值守时这是最主要的卡死来源。
// 恢复是逐级升级的：先试最轻的关闭弹窗，不行才逐步升级到重启应用。
func (s *Session) Recover(ctx context.Context) error {
	s.Log.Warn("连续未命中，开始恢复", "次数", s.missStreak)
	defer func() { s.missStreak = 0 }()

	for _, step := range s.Cfg.Game.Recovery.Steps {
		s.Log.Info("恢复步骤", "步骤", step)
		switch step {
		case "close_popup":
			if ok, _ := s.Click(ctx, TplClose); ok {
				if back, err := s.backAtMain(ctx); err == nil && back {
					return nil
				}
			}
		case "tap_blank":
			// 点画面顶部靠边的空白处：多数弹窗点外部即关闭，
			// 这个位置又基本不会压到实际按钮。
			bw, _ := s.Dev.BaseSize()
			if err := s.Dev.Tap(ctx, bw-40, 40); err != nil {
				return err
			}
			if back, err := s.backAtMain(ctx); err == nil && back {
				return nil
			}
		case "press_back_repeat":
			n := s.Cfg.Game.Recovery.PressBackTimes
			for i := 0; i < n; i++ {
				if err := s.Dev.Back(ctx); err != nil {
					return err
				}
				if back, err := s.backAtMain(ctx); err == nil && back {
					return nil
				}
			}
		case "restart_app":
			s.Log.Warn("前面的恢复手段都无效，重启游戏")
			if err := s.Dev.LaunchApp(ctx, s.Cfg.Game.App.Package, s.Cfg.Game.App.MainActivity); err != nil {
				return err
			}
			if _, ok, err := s.WaitFor(ctx, s.Cfg.Game.Anchors[config.AnchorMain], s.Cfg.Game.LaunchTimeout()); err != nil {
				return err
			} else if ok {
				return nil
			}
		default:
			s.Log.Warn("未知的恢复步骤，已跳过", "步骤", step)
		}
	}
	return fmt.Errorf("恢复失败：已尝试全部步骤仍未回到主界面")
}

func (s *Session) backAtMain(ctx context.Context) (bool, error) {
	anchor, ok := s.Cfg.Game.Anchors[config.AnchorMain]
	if !ok || anchor == "" {
		return false, fmt.Errorf("anchors.%s 未配置", config.AnchorMain)
	}
	_, hit, err := s.Find(ctx, anchor)
	return hit, err
}

// SaveShot 把当前画面存到 logs/ 下，用于事后排查卡在了哪个界面。
func (s *Session) SaveShot(ctx context.Context, tag string) (string, error) {
	f, err := s.Capture(ctx)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s_%s.png", time.Now().Format("20060102_150405"), tag)
	path := filepath.Join(s.Cfg.LogsDir(), name)
	if err := f.SavePNG(path); err != nil {
		return "", err
	}
	return path, nil
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
