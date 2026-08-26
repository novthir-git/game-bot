// Package tasks 实现《我的花园世界》的具体任务流程。
//
// 这里引用的模板图目前尚未采集，文件名即约定：
// 按 templates/README.md 的规范截图并放到对应目录后，任务即可运行。
// 启动前 `gardenbot doctor` 会列出还缺哪些图。
package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/task"
)

// 模板路径常量。集中在这里，便于 doctor 汇总检查，也避免字符串散落各处。
const (
	tplMainAnchor = "main/anchor_main.png"

	tplEntryRack       = "main/btn_flower_rack.png"
	tplEntryWaterwheel = "main/btn_waterwheel.png"
	tplEntryPearl      = "main/btn_pearl.png"

	tplRackAnchor    = "flower_rack/anchor_rack.png"
	tplRackDelist    = "flower_rack/btn_delist.png"
	tplRackList      = "flower_rack/btn_list.png"
	tplRackConfirm   = "flower_rack/btn_confirm.png"
	tplRackSlotEmpty = "flower_rack/state_slot_empty.png"

	tplWaterCollect = "resources/btn_collect_water.png"
	tplPearlReady   = "resources/icon_pearl_ready.png"
	tplPearlCollect = "resources/btn_collect_pearl.png"
)

// 各步骤的等待上限。游戏有过场动画，给得太紧会误判为失败。
const (
	waitPanel  = 8 * time.Second
	waitAction = 5 * time.Second
)

// returnToMain 连按返回直到回到主界面。
//
// 每个任务结束时都要调用：任务之间必须从同一个已知状态开始，
// 否则上一个任务残留的界面会让下一个任务从一开始就找不到目标。
func returnToMain(ctx context.Context, s *task.Session) error {
	for i := 0; i < 5; i++ {
		if _, ok, err := s.Find(ctx, tplMainAnchor); err != nil {
			return err
		} else if ok {
			return nil
		}
		// 顺手关掉可能挡路的弹窗
		if ok, _ := s.Click(ctx, task.TplClose); ok {
			continue
		}
		if err := s.Dev.Back(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("连按返回 5 次仍未回到主界面")
}

// enterPanel 从主界面进入某个子界面。
func enterPanel(ctx context.Context, s *task.Session, entry, anchor string) error {
	if err := returnToMain(ctx, s); err != nil {
		return err
	}
	ok, err := s.Click(ctx, entry)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("主界面上找不到入口 %s", entry)
	}
	if _, ok, err := s.WaitFor(ctx, anchor, waitPanel); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("点击 %s 后未进入目标界面（等待 %s 超时）", entry, anchor)
	}
	return nil
}

// Register 按 tasks.yaml 把已启用的任务注册进调度器。
//
// 只注册 enabled 为 true 的任务；关掉的任务连对象都不会创建，
// 因此它们依赖的模板图也不会被 doctor 要求。
func Register(sched *task.Scheduler, cfg *config.Bundle) error {
	for name, tc := range cfg.Tasks.Tasks {
		if !tc.Enabled {
			continue
		}
		var (
			t        task.Task
			interval time.Duration
		)
		switch name {
		case "waterwheel_collect":
			t, interval = &WaterwheelCollect{cfg: tc}, tc.Interval()
		case "pearl_harvest":
			t, interval = &PearlHarvest{cfg: tc}, tc.Interval()
		case "flower_rack_cycle":
			t, interval = &FlowerRackCycle{cfg: tc}, tc.RelistInterval()
		default:
			// tasks.yaml 里已经写好但尚未实现的任务（P1/P2），
			// 开着也只是提醒，不应让整个程序起不来。
			return fmt.Errorf("任务 %q 在 tasks.yaml 中被启用，但尚未实现；"+
				"请先把它的 enabled 改回 false", name)
		}
		if interval <= 0 {
			return fmt.Errorf("任务 %q 未配置有效的执行间隔", name)
		}
		sched.Add(t, interval, tc.Priority)
	}
	return nil
}

// errNotFound 统一「界面上找不到某个元素」的报错文案，
// 直接指向排查方向：要么阈值需要调，要么模板图该重截。
func errNotFound(tpl string) error {
	return fmt.Errorf("界面上找不到 %s：先用 `tune` 在失败截图上核对该模板的匹配分数", tpl)
}
