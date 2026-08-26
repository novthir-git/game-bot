package tasks

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/state"
	"github.com/novthir-git/game-bot/internal/task"
)

func newSession(t *testing.T) *task.Session {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &task.Session{
		State: st,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func stubToday(t *testing.T, day string) {
	t.Helper()
	orig := today
	today = func() string { return day }
	t.Cleanup(func() { today = orig })
}

// 一轮 Run 含实机操作，可能跑十几秒。若某轮横跨零点，落盘必须用
// 这一轮开始时的日期，而不是结束时的日期——否则昨天累计的次数会被
// 盖上今天的日期，下一轮看到日期已是今天便不再重置，跨天归零永远不触发。
func TestSaveUsesRunStartDateNotEndDate(t *testing.T) {
	s := newSession(t)
	tk := &FlowerRackCycle{cfg: config.Task{ResetDaily: true, TargetCount: 135}}
	tk.st.Done = 135
	tk.st.LastList = time.Now()

	stubToday(t, "2026-08-26") // Run 结束时已经跨天
	tk.save(s, "2026-08-25")   // 但这一轮是 25 号开始的

	var got rackState
	ok, err := s.State.Get(stateKeyRack, &got)
	if err != nil || !ok {
		t.Fatalf("读取状态失败: ok=%v err=%v", ok, err)
	}
	if got.Date != "2026-08-25" {
		t.Errorf("落盘日期 = %q，应为本轮开始时的 2026-08-25", got.Date)
	}

	// 关键后果：下一轮加载时必须识别出已跨天并归零
	next := &FlowerRackCycle{cfg: config.Task{ResetDaily: true, TargetCount: 135}}
	next.load(s)
	if next.st.Done != 0 {
		t.Errorf("跨天后进度应归零，实际 %d", next.st.Done)
	}
}

func TestLoadRestoresSameDayProgress(t *testing.T) {
	s := newSession(t)
	stubToday(t, "2026-08-26")

	tk := &FlowerRackCycle{cfg: config.Task{ResetDaily: true}}
	tk.st.Done = 87
	tk.st.LastList = time.Now().Add(-time.Minute)
	tk.save(s, "2026-08-26")

	restored := &FlowerRackCycle{cfg: config.Task{ResetDaily: true}}
	restored.load(s)
	if restored.st.Done != 87 {
		t.Errorf("同一天内应恢复进度 87，实际 %d", restored.st.Done)
	}
	if restored.st.LastList.IsZero() {
		t.Error("上次上架时刻也应被恢复，否则重启后会立刻重上一次")
	}
}

// reset_daily 为 false 时是一次性累计目标，跨天不该归零。
func TestLoadKeepsProgressWhenResetDailyOff(t *testing.T) {
	s := newSession(t)
	stubToday(t, "2026-08-25")
	tk := &FlowerRackCycle{cfg: config.Task{ResetDaily: false}}
	tk.st.Done = 120
	tk.save(s, "2026-08-25")

	stubToday(t, "2026-08-27")
	restored := &FlowerRackCycle{cfg: config.Task{ResetDaily: false}}
	restored.load(s)
	if restored.st.Done != 120 {
		t.Errorf("reset_daily=false 时跨天不应归零，实际 %d", restored.st.Done)
	}
}

// 没有状态存储时任务仍要能跑，只是重启后从零开始。
func TestLoadWithoutStateStoreIsSafe(t *testing.T) {
	s := &task.Session{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tk := &FlowerRackCycle{cfg: config.Task{ResetDaily: true}}
	tk.load(s)
	tk.save(s, "2026-08-26") // 不应 panic
	if tk.st.Done != 0 {
		t.Errorf("无状态存储时应为零值，实际 %d", tk.st.Done)
	}
}

func TestProgressReportsAgainstTarget(t *testing.T) {
	tk := &FlowerRackCycle{cfg: config.Task{TargetCount: 135}}
	tk.st.Done = 42
	if done, target := tk.Progress(); done != 42 || target != 135 {
		t.Errorf("Progress() = %d/%d，期望 42/135", done, target)
	}
}
