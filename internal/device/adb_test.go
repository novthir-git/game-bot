package device

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// 父 context 被取消（Ctrl-C）时，adb 子进程会被杀掉，
// cmd.Output() 返回的是「signal: killed」这类表象错误。
// runRaw 必须把 context 的 sentinel 传上去：调度器靠
// errors.Is(err, context.Canceled) 区分「正常退出」和「任务失败」，
// 丢了它，一次 Ctrl-C 会被记成任务失败、存失败截图、甚至触发恢复流程。
func TestRunRawPropagatesCancellation(t *testing.T) {
	sleep := findSleep(t)
	a := &ADB{bin: sleep, serial: "test", timeout: 30 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := a.runRaw(ctx, "10")
	if err == nil {
		t.Fatal("被取消时应当返回错误")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("取消后应立即返回，实际耗时 %v", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) 应为 true，实际错误: %v", err)
	}
}

// 取消发生在子进程启动之前时，同样要能识别。
func TestRunRawCancelledBeforeStart(t *testing.T) {
	sleep := findSleep(t)
	a := &ADB{bin: sleep, serial: "test", timeout: 30 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := a.runRaw(ctx, "10"); !errors.Is(err, context.Canceled) {
		t.Errorf("启动前取消也应可识别，实际错误: %v", err)
	}
}

// 本次调用自己的期限到了才算超时，且超时同样要可用 errors.Is 识别。
func TestRunRawOwnTimeout(t *testing.T) {
	sleep := findSleep(t)
	a := &ADB{bin: sleep, serial: "test", timeout: 200 * time.Millisecond}

	_, err := a.runRaw(context.Background(), "10")
	if err == nil {
		t.Fatal("超时应当返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) 应为 true，实际错误: %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("自身超时不应被误判为取消: %v", err)
	}
}

// 父 context 的 deadline 到期属于「被中断」，不该报成本次调用的超时时长。
func TestRunRawParentDeadlineNotReportedAsOwnTimeout(t *testing.T) {
	sleep := findSleep(t)
	a := &ADB{bin: sleep, serial: "test", timeout: 30 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := a.runRaw(ctx, "10")
	if err == nil {
		t.Fatal("父 context 到期时应当返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("应可识别为 DeadlineExceeded，实际: %v", err)
	}
	if got := err.Error(); contains(got, "30s") {
		t.Errorf("不应把父 context 到期报成本次调用的 30s 超时: %v", got)
	}
}

// 普通的子进程失败仍要返回可读错误，且不能被误判为取消。
func TestRunRawOrdinaryFailure(t *testing.T) {
	a := &ADB{bin: findFalse(t), serial: "test", timeout: 5 * time.Second}
	_, err := a.runRaw(context.Background())
	if err == nil {
		t.Fatal("子进程返回非零退出码时应当报错")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("普通失败不应被识别为取消或超时: %v", err)
	}
}

func findSleep(t *testing.T) string { return findBin(t, "/bin/sleep", "/usr/bin/sleep") }
func findFalse(t *testing.T) string { return findBin(t, "/bin/false", "/usr/bin/false") }

func findBin(t *testing.T, paths ...string) string {
	t.Helper()
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	t.Skipf("测试所需的可执行文件不存在: %v", paths)
	return ""
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
