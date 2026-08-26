package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/task"
)

// FlowerRackCycle 刷花架上架次数。
//
// 这是本游戏自动化价值最高的一项：需要上架 135 次，可以靠「上架 4 分钟后
// 下架再重新上架」来刷，折合约 9 小时的纯点击。这种设计本身就是在惩罚人肉玩家。
//
// 每次 Run 只做一轮「下架 -> 重新上架」，而不是在内部循环几个小时。
// 交给调度器按 relist_interval_sec 反复调用有两个好处：
// 珍珠采集这类任务能在间隙里插进来，以及 Ctrl-C 能立刻停下。
type FlowerRackCycle struct {
	cfg config.Task

	done      int       // 已完成的上架次数，由 bot 自己数
	lastList  time.Time // 上次上架的时刻
	firstDone bool
}

func (t *FlowerRackCycle) Name() string { return "花架上架循环" }

func (t *FlowerRackCycle) RequiredTemplates() []string {
	return []string{
		tplMainAnchor, tplEntryRack, tplRackAnchor,
		tplRackDelist, tplRackList, tplRackConfirm, tplRackSlotEmpty,
	}
}

// Progress 返回已完成次数与目标次数。
func (t *FlowerRackCycle) Progress() (int, int) { return t.done, t.cfg.TargetCount }

func (t *FlowerRackCycle) Run(ctx context.Context, s *task.Session) error {
	if t.cfg.TargetCount > 0 && t.done >= t.cfg.TargetCount {
		s.Log.Info("花架次数已刷满", "次数", t.done)
		return nil
	}

	// 计数和计时都由 bot 自己维护，不去读界面上的 "87/135" 和倒计时。
	// 读屏只会为本来不会出错的环节引入一个出错源：OCR 把 87 认成 37，整个循环就废了。
	if t.firstDone {
		if elapsed := time.Since(t.lastList); elapsed < t.cfg.RelistInterval() {
			s.Log.Debug("距离可下架还差一点", "已过", elapsed.Round(time.Second))
			return nil
		}
	}

	if err := enterPanel(ctx, s, tplEntryRack, tplRackAnchor); err != nil {
		return err
	}

	// 已有在架商品就先下架。首轮时架子可能本来就是空的，这一步没找到属于正常。
	if ok, err := s.Click(ctx, tplRackDelist); err != nil {
		return err
	} else if ok {
		if _, err := s.WaitVanish(ctx, tplRackDelist, waitAction); err != nil {
			return err
		}
		s.Log.Debug("已下架")
	}

	// 上架：点空槽位 -> 选商品 -> 确认
	if ok, err := s.Click(ctx, tplRackSlotEmpty); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("花架上找不到空槽位（%s）：可能下架未生效，或槽位模板需要重新采集", tplRackSlotEmpty)
	}

	// prefer_highest_price 为 true 时应当选价格最高的花艺品。
	// 具体选法要等看到实际界面才能定：若列表默认按价格排序，点第一项即可；
	// 否则需要一个「最高价」的标识模板。目前先走默认的上架按钮。
	if ok, err := s.Click(ctx, tplRackList); err != nil {
		return err
	} else if !ok {
		return errNotFound(tplRackList)
	}
	if ok, err := s.Click(ctx, tplRackConfirm); err != nil {
		return err
	} else if !ok {
		return errNotFound(tplRackConfirm)
	}

	t.done++
	t.lastList = time.Now()
	t.firstDone = true
	s.Log.Info("花架上架完成", "进度", fmt.Sprintf("%d/%d", t.done, t.cfg.TargetCount))

	return returnToMain(ctx, s)
}
