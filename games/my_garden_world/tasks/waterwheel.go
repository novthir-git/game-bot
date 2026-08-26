package tasks

import (
	"context"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/task"
)

// WaterwheelCollect 领取水车的免费水滴。
//
// 水滴是体力，水车有容量上限，满了之后继续产出的部分直接浪费。
// 人会忘，所以这是自动化收益最直接的一类任务——它不需要任何判断，
// 只要按时去点一下就行。
type WaterwheelCollect struct {
	cfg config.Task
}

func (t *WaterwheelCollect) Name() string { return "水车领取" }

func (t *WaterwheelCollect) RequiredTemplates() []string {
	return []string{tplMainAnchor, tplEntryWaterwheel, tplWaterCollect}
}

func (t *WaterwheelCollect) Run(ctx context.Context, s *task.Session) error {
	if err := returnToMain(ctx, s); err != nil {
		return err
	}
	ok, err := s.Click(ctx, tplEntryWaterwheel)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound(tplEntryWaterwheel)
	}

	// 领取按钮不在就说明这一轮还没攒出可领的量，属于正常情况，不算失败。
	collected, err := s.Click(ctx, tplWaterCollect)
	if err != nil {
		return err
	}
	if collected {
		s.Log.Info("已领取水车水滴")
		// 领取后通常会有飘字动画，等它结束再走，避免下个任务截到动画中的画面
		if _, err := s.WaitVanish(ctx, tplWaterCollect, waitAction); err != nil {
			return err
		}
	} else {
		s.Log.Info("水车暂无可领取的水滴")
	}
	return returnToMain(ctx, s)
}
