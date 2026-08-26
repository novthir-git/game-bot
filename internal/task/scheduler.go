package task

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sort"
	"time"
)

// Task 是一个可被调度的任务。
type Task interface {
	Name() string
	Run(ctx context.Context, s *Session) error
}

// TemplateUser 是可选接口：任务声明自己依赖哪些模板图。
// 调度器在启动前会汇总检查，缺图直接拒绝启动，
// 而不是让任务跑到一半才因为找不到文件而失败。
type TemplateUser interface {
	RequiredTemplates() []string
}

type entry struct {
	task     Task
	interval time.Duration
	priority int
	next     time.Time
	fails    int
}

// Scheduler 串行调度所有任务。
//
// 这里刻意用单循环而不是「每个任务一个 goroutine + Ticker」。
// 原因是所有任务共享同一台模拟器，同一时刻只能有一个在操作，
// 真正的并发度是 1；而单循环额外换来两个东西：
// 任务到期时能按优先级挑，以及全局的连续失败计数可以直接触发恢复流程。
// 用 goroutine 加锁去实现这两点，只会更啰嗦且更容易写出竞态。
type Scheduler struct {
	sess *Session
	log  *slog.Logger

	tick               time.Duration
	jitterLo, jitterHi time.Duration
	saveShotOnFail     bool

	entries []*entry
}

func NewScheduler(sess *Session) *Scheduler {
	lo, hi := sess.Cfg.Tasks.JitterRange()
	return &Scheduler{
		sess:           sess,
		log:            sess.Log,
		tick:           sess.Cfg.Tasks.TickInterval(),
		jitterLo:       lo,
		jitterHi:       hi,
		saveShotOnFail: sess.Cfg.Tasks.Logging.SaveScreenshotOnFailure,
	}
}

// Add 注册一个任务。interval<=0 表示只在启动时跑一次。
func (s *Scheduler) Add(t Task, interval time.Duration, priority int) {
	s.entries = append(s.entries, &entry{
		task:     t,
		interval: interval,
		priority: priority,
		next:     time.Now(),
	})
}

// Len 返回已注册的任务数。
func (s *Scheduler) Len() int { return len(s.entries) }

// RequiredTemplates 汇总所有已注册任务声明的模板依赖，外加通用模板和界面锚点。
func (s *Scheduler) RequiredTemplates() []string {
	return s.requiredFrom(s.sess.Cfg.Game.Anchors)
}

func (s *Scheduler) requiredFrom(anchors map[string]string) []string {
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
		}
	}
	add(TplClose)
	for _, a := range anchors {
		add(a)
	}
	for _, e := range s.entries {
		if tu, ok := e.task.(TemplateUser); ok {
			for _, n := range tu.RequiredTemplates() {
				add(n)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Run 进入调度循环，直到 ctx 被取消。
func (s *Scheduler) Run(ctx context.Context) error {
	if len(s.entries) == 0 {
		return errors.New("没有启用任何任务：请检查 tasks.yaml 里的 enabled 开关")
	}
	s.log.Info("调度器启动", "任务数", len(s.entries), "轮询间隔", s.tick)

	for {
		if err := ctx.Err(); err != nil {
			s.log.Info("调度器退出")
			return nil
		}

		if e := s.pick(time.Now()); e != nil {
			s.runOne(ctx, e)
		}

		if err := sleepCtx(ctx, s.tick+s.jitter()); err != nil {
			s.log.Info("调度器退出")
			return nil
		}
	}
}

// pick 在已到期的任务里挑一个：优先级数字小的优先，同优先级则先到期的优先。
func (s *Scheduler) pick(now time.Time) *entry {
	var best *entry
	for _, e := range s.entries {
		if e.next.After(now) {
			continue
		}
		if best == nil ||
			e.priority < best.priority ||
			(e.priority == best.priority && e.next.Before(best.next)) {
			best = e
		}
	}
	return best
}

func (s *Scheduler) runOne(ctx context.Context, e *entry) {
	name := e.task.Name()
	start := time.Now()
	s.log.Info("任务开始", "任务", name)

	err := e.task.Run(ctx, s.sess)
	switch {
	case err == nil:
		e.fails = 0
		s.log.Info("任务完成", "任务", name, "耗时", time.Since(start).Round(time.Millisecond))
		e.next = s.nextRun(e)

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// 属于正常退出，不算失败，也不要触发恢复
		e.next = s.nextRun(e)

	default:
		e.fails++
		s.log.Error("任务失败", "任务", name, "次数", e.fails, "错误", err)
		if s.saveShotOnFail {
			if p, serr := s.sess.SaveShot(ctx, name); serr == nil {
				s.log.Info("已保存失败截图", "路径", p)
			}
		}
		// 失败后退避，避免在同一个卡死状态上反复空转刷屏
		e.next = time.Now().Add(s.backoff(e))

		if s.sess.ShouldRecover() {
			if rerr := s.sess.Recover(ctx); rerr != nil {
				s.log.Error("恢复失败", "错误", rerr)
			}
		}
	}
}

func (s *Scheduler) nextRun(e *entry) time.Time {
	if e.interval <= 0 {
		// 一次性任务：推到极远的将来，等于不再执行
		return time.Now().Add(100 * 365 * 24 * time.Hour)
	}
	return time.Now().Add(e.interval)
}

// backoff 按连续失败次数指数退避，上限 5 分钟或任务本身的间隔（取较小者）。
func (s *Scheduler) backoff(e *entry) time.Duration {
	d := s.tick << min(e.fails, 6)
	cap := 5 * time.Minute
	if e.interval > 0 && e.interval < cap {
		cap = e.interval
	}
	if d > cap {
		d = cap
	}
	return d
}

// jitter 给每轮之间加一点随机休息，避免完全固定的机械节奏。
func (s *Scheduler) jitter() time.Duration {
	if s.jitterHi <= s.jitterLo {
		return 0
	}
	return s.jitterLo + time.Duration(rand.Int64N(int64(s.jitterHi-s.jitterLo)))
}
