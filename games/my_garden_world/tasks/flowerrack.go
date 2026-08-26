package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/task"
)

// stateKeyRack 是花架进度在状态文件中的键名。
const stateKeyRack = "flower_rack_cycle"

// rackState 是需要跨进程重启保留的花架进度。
type rackState struct {
	// Date 是这份进度所属的日期（本地时区，YYYY-MM-DD）。
	// 目标次数若按天重置，跨天后就要把 Done 清零。
	Date string `json:"date"`
	// Done 是已完成的上架次数，由 bot 自己数，不从界面读。
	Done int `json:"done"`
	// LastList 是上次上架的时刻，重启后据此判断距离可下架还差多久。
	LastList time.Time `json:"last_list"`
}

// FlowerRackCycle 刷花架上架次数。
//
// 这是本游戏自动化价值最高的一项：需要上架 135 次，可以靠「上架 4 分钟后
// 下架再重新上架」来刷，折合约 9 小时的纯点击。这种设计本身就是在惩罚人肉玩家。
//
// 每次 Run 只做一轮「下架 -> 重新上架」，而不是在内部循环几个小时。
// 交给调度器按 relist_interval_sec 反复调用有两个好处：
// 珍珠采集这类任务能在间隙里插进来，以及 Ctrl-C 能立刻停下。
//
// 进度落盘：九小时的任务几乎必然会遇到重启（断线、手动停、机器休眠），
// 计数只放内存的话每次都从零开始，这个任务就永远刷不满。
type FlowerRackCycle struct {
	cfg config.Task

	st     rackState
	loaded bool
}

func (t *FlowerRackCycle) Name() string { return "花架上架循环" }

func (t *FlowerRackCycle) RequiredTemplates() []string {
	return []string{
		tplMainAnchor, tplEntryRack, tplRackAnchor,
		tplRackDelist, tplRackList, tplRackConfirm, tplRackSlotEmpty,
	}
}

// Progress 返回已完成次数与目标次数。
func (t *FlowerRackCycle) Progress() (int, int) { return t.st.Done, t.cfg.TargetCount }

// today 是包级变量而非普通函数，测试要靠替换它来模拟跨零点。
var today = func() string { return time.Now().Format("2006-01-02") }

// load 首次运行时从状态文件恢复进度。
func (t *FlowerRackCycle) load(s *task.Session) {
	if t.loaded {
		return
	}
	t.loaded = true
	if s.State == nil {
		return
	}
	var st rackState
	ok, err := s.State.Get(stateKeyRack, &st)
	if err != nil {
		// 状态读不出来不该让任务停摆，从零开始重刷即可。
		s.Log.Warn("花架进度读取失败，按从零开始处理", "错误", err)
		return
	}
	if !ok {
		return
	}
	if t.cfg.ResetDaily && st.Date != today() {
		s.Log.Info("已跨天，花架进度归零", "上次日期", st.Date, "上次次数", st.Done)
		return
	}
	t.st = st
	s.Log.Info("已恢复花架进度", "次数", st.Done, "上次上架", st.LastList.Format("15:04:05"))
}

// save 把进度落盘。day 必须是本轮 Run 开始时取的日期，不能在这里重新取：
// 一轮 Run 含 enterPanel/WaitFor 等实机操作，可能跑十几秒。若某轮横跨零点，
// 用结束时刻的日期落盘，就会把昨天累计的次数盖上今天的日期；
// 下一轮看到日期已是今天，便认为「今天已处理过」，跨天重置这一天永远不会触发。
func (t *FlowerRackCycle) save(s *task.Session, day string) {
	if s.State == nil {
		return
	}
	t.st.Date = day
	if err := s.State.Set(stateKeyRack, t.st); err != nil {
		// 落盘失败只影响重启后的恢复，不影响本次运行，记一条警告继续跑。
		s.Log.Warn("花架进度落盘失败", "错误", err)
	}
}

func (t *FlowerRackCycle) Run(ctx context.Context, s *task.Session) error {
	t.load(s)

	// 本轮的日期只取一次，重置判定和最后落盘都用它。
	day := today()

	// 跨天检查放在每次运行时做，而不是只在启动时做：
	// 这个任务本来就要连着跑九个多小时，中途跨零点是常态。
	if t.cfg.ResetDaily && t.st.Date != "" && t.st.Date != day {
		s.Log.Info("已跨天，花架进度归零", "上次日期", t.st.Date, "上次次数", t.st.Done)
		t.st = rackState{}
	}

	if t.cfg.TargetCount > 0 && t.st.Done >= t.cfg.TargetCount {
		s.Log.Info("花架次数已刷满", "次数", t.st.Done)
		return nil
	}

	// 计数和计时都由 bot 自己维护，不去读界面上的 "87/135" 和倒计时。
	// 读屏只会为本来不会出错的环节引入一个出错源：OCR 把 87 认成 37，整个循环就废了。
	if !t.st.LastList.IsZero() {
		if elapsed := time.Since(t.st.LastList); elapsed < t.cfg.RelistInterval() {
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

	t.st.Done++
	t.st.LastList = time.Now()
	t.save(s, day)
	s.Log.Info("花架上架完成", "进度", fmt.Sprintf("%d/%d", t.st.Done, t.cfg.TargetCount))

	return returnToMain(ctx, s)
}
