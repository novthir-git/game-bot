package tasks

import (
	"context"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/task"
)

// PearlHarvest 采集珍珠。
//
// 珍珠每 2 小时可采一次。与水车同理：机制简单、纯定时、人容易漏，
// 是自动化性价比最高的一类。
type PearlHarvest struct {
	cfg config.Task
}

func (t *PearlHarvest) Name() string { return "珍珠采集" }

func (t *PearlHarvest) RequiredTemplates() []string {
	return []string{tplMainAnchor, tplEntryPearl, tplPearlReady, tplPearlCollect}
}

func (t *PearlHarvest) Run(ctx context.Context, s *task.Session) error {
	if err := returnToMain(ctx, s); err != nil {
		return err
	}

	// 先看采集点上有没有「可采集」的标记再进去。
	// 这一步用找图代替读倒计时数字：我们只需要知道能不能采，不需要知道还剩几分几秒。
	if _, ready, err := s.Find(ctx, tplPearlReady); err != nil {
		return err
	} else if !ready {
		s.Log.Info("珍珠尚未到可采集状态")
		return nil
	}

	ok, err := s.Click(ctx, tplEntryPearl)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound(tplEntryPearl)
	}

	if ok, err := s.Click(ctx, tplPearlCollect); err != nil {
		return err
	} else if !ok {
		return errNotFound(tplPearlCollect)
	}
	s.Log.Info("已采集珍珠")

	if _, err := s.WaitVanish(ctx, tplPearlCollect, waitAction); err != nil {
		return err
	}
	return returnToMain(ctx, s)
}
