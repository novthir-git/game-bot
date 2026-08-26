package task

import (
	"context"
	"testing"
	"time"
)

type fakeTask struct {
	name string
	tpls []string
}

func (f fakeTask) Name() string                        { return f.name }
func (f fakeTask) Run(context.Context, *Session) error { return nil }
func (f fakeTask) RequiredTemplates() []string         { return f.tpls }

func TestPickHonoursPriorityAndDueTime(t *testing.T) {
	now := time.Now()
	s := &Scheduler{}
	// 两个都已到期，优先级数字小的应当先跑
	low := &entry{task: fakeTask{name: "低优先"}, priority: 1, next: now.Add(-time.Minute)}
	high := &entry{task: fakeTask{name: "高优先"}, priority: 0, next: now.Add(-time.Second)}
	future := &entry{task: fakeTask{name: "未到期"}, priority: 0, next: now.Add(time.Hour)}
	s.entries = []*entry{low, high, future}

	if got := s.pick(now); got != high {
		t.Fatalf("应选中高优先任务，实际 %v", got.task.Name())
	}

	// 同优先级时，先到期的优先
	a := &entry{task: fakeTask{name: "早"}, priority: 0, next: now.Add(-10 * time.Minute)}
	b := &entry{task: fakeTask{name: "晚"}, priority: 0, next: now.Add(-time.Second)}
	s.entries = []*entry{b, a}
	if got := s.pick(now); got != a {
		t.Errorf("同优先级应选先到期的，实际 %v", got.task.Name())
	}

	// 全部未到期时不选任何任务
	s.entries = []*entry{future}
	if got := s.pick(now); got != nil {
		t.Errorf("无到期任务时应返回 nil，实际 %v", got.task.Name())
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	s := &Scheduler{tick: 5 * time.Second}

	// 短周期任务的退避不得超过它自己的间隔，否则会连着错过好几轮
	short := &entry{interval: 30 * time.Second}
	var prev time.Duration
	for i := 1; i <= 8; i++ {
		short.fails = i
		d := s.backoff(short)
		if d > 30*time.Second {
			t.Fatalf("第 %d 次失败退避 %v，超过了任务间隔 30s", i, d)
		}
		if d < prev {
			t.Errorf("退避不应变短：第 %d 次 %v < 上一次 %v", i, d, prev)
		}
		prev = d
	}

	// 长周期任务的退避封顶 5 分钟
	long := &entry{interval: 2 * time.Hour}
	long.fails = 20
	if d := s.backoff(long); d != 5*time.Minute {
		t.Errorf("长周期任务退避应封顶 5 分钟，实际 %v", d)
	}
}

func TestNextRunOneShotDoesNotRepeat(t *testing.T) {
	s := &Scheduler{}
	oneShot := &entry{interval: 0}
	if got := s.nextRun(oneShot); time.Until(got) < 24*time.Hour {
		t.Errorf("interval<=0 的任务不应被再次调度，下次运行时间 %v", got)
	}
	repeating := &entry{interval: time.Minute}
	if got := s.nextRun(repeating); time.Until(got) > 2*time.Minute {
		t.Errorf("周期任务的下次运行时间不合理: %v", got)
	}
}

func TestRequiredTemplatesDedupes(t *testing.T) {
	s := &Scheduler{sess: &Session{}}
	s.sess.Cfg = nil
	// 直接构造，避开需要真实设备的 NewScheduler
	s.entries = []*entry{
		{task: fakeTask{name: "a", tpls: []string{"x/one.png", "x/two.png"}}},
		{task: fakeTask{name: "b", tpls: []string{"x/two.png", "x/three.png"}}},
	}
	got := s.requiredFrom(nil)
	want := []string{TplClose, "x/one.png", "x/three.png", "x/two.png"}
	if len(got) != len(want) {
		t.Fatalf("模板去重结果 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项 = %q，期望 %q", i, got[i], want[i])
		}
	}
}
