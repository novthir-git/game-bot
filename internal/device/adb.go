// Package device 封装对 MuMu 12 模拟器的 ADB 控制：连接、截图、点击。
//
// 这里直接调用 adb 可执行文件而非实现 ADB 协议。本游戏是分钟级节奏，
// 每次操作多付出的几十毫秒进程启动开销完全无关紧要，
// 换来的是不必自己维护一份协议实现。
package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ADB struct {
	bin     string
	serial  string
	timeout time.Duration
}

// NewADB 解析 adb 可执行文件位置并返回控制器。
//
// 优先使用配置里给的路径——必须用 MuMu 自带的那个 adb，
// 系统里若装了其他版本，两者会互相 kill-server 导致连接反复掉线。
// 配置路径不存在时回退到 PATH 中的 adb，并由调用方决定是否告警。
func NewADB(configuredBin, serial string, timeout time.Duration) (*ADB, error) {
	bin, err := resolveBinary(configuredBin)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ADB{bin: bin, serial: serial, timeout: timeout}, nil
}

// Binary 返回实际使用的 adb 路径。
func (a *ADB) Binary() string { return a.bin }

// Serial 返回目标设备标识。
func (a *ADB) Serial() string { return a.serial }

func resolveBinary(configured string) (string, error) {
	if configured != "" {
		if st, err := os.Stat(configured); err == nil && !st.IsDir() {
			return configured, nil
		}
	}
	p, err := exec.LookPath("adb")
	if err != nil {
		return "", fmt.Errorf("找不到 adb：配置的路径 %q 不存在，PATH 中也没有 adb。"+
			"请把 MuMu 自带的 adb 路径写进 config/local.yaml 的 adb.binary", configured)
	}
	return p, nil
}

// UsingConfiguredBinary 报告是否用上了配置里指定的那个 adb。
// 返回 false 说明回退到了 PATH 里的 adb，存在版本冲突风险。
func (a *ADB) UsingConfiguredBinary(configured string) bool {
	return configured != "" && a.bin == configured
}

// Connect 连接到模拟器。MuMu 通过 TCP 暴露 ADB，需要先 connect 才能寻址。
func (a *ADB) Connect(ctx context.Context) error {
	out, err := a.runRaw(ctx, "connect", a.serial)
	if err != nil {
		return err
	}
	s := string(out)
	// adb connect 失败时仍然返回 0，只能靠输出文本判断
	if strings.Contains(s, "unable to connect") || strings.Contains(s, "failed to connect") {
		return fmt.Errorf("连接 %s 失败：%s（模拟器是否已启动？端口是否正确？）",
			a.serial, strings.TrimSpace(s))
	}
	return nil
}

// Devices 返回当前 adb 已识别的设备列表。
func (a *ADB) Devices(ctx context.Context) ([]string, error) {
	out, err := a.runRaw(ctx, "devices")
	if err != nil {
		return nil, err
	}
	var list []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		list = append(list, line)
	}
	return list, nil
}

// Shell 执行 adb -s <serial> shell <args...>，返回标准输出。
func (a *ADB) Shell(ctx context.Context, args ...string) ([]byte, error) {
	return a.run(ctx, append([]string{"shell"}, args...)...)
}

// ExecOut 执行 adb -s <serial> exec-out <args...>。
//
// 截图必须走 exec-out 而不是 shell：shell 会对输出做换行转换，
// 把二进制像素数据破坏掉。
func (a *ADB) ExecOut(ctx context.Context, args ...string) ([]byte, error) {
	return a.run(ctx, append([]string{"exec-out"}, args...)...)
}

func (a *ADB) run(ctx context.Context, args ...string) ([]byte, error) {
	return a.runRaw(ctx, append([]string{"-s", a.serial}, args...)...)
}

func (a *ADB) runRaw(ctx context.Context, args ...string) ([]byte, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		desc := strings.Join(args, " ")

		// 父 context 被取消（Ctrl-C）时，adb 子进程会被杀掉，
		// cmd.Output() 返回的是「signal: killed」这类表象错误。
		// 必须把 context 的 sentinel 用 %w 传上去：调度器靠
		// errors.Is(err, context.Canceled) 区分「正常退出」和「任务失败」，
		// 丢了它，一次 Ctrl-C 会被记成失败、存失败截图、甚至触发恢复流程。
		if perr := parent.Err(); perr != nil {
			return nil, fmt.Errorf("adb %s 被中断: %w", desc, perr)
		}
		// 到这里父 context 仍然正常，超时只可能来自本次调用自己的期限。
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("adb %s 超时（%s）: %w", desc, a.timeout, context.DeadlineExceeded)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("adb %s 失败: %s", desc, msg)
	}
	return out, nil
}
