// Command gardenbot 运行游戏自动化任务。
//
//	gardenbot doctor    检查环境：adb、设备连接、分辨率、前台包名、缺失的模板图
//	gardenbot capture   截一张图存到本地，用于制作模板
//	gardenbot run       启动调度器
//
// 首次使用的顺序是 doctor -> capture -> 截模板 -> tune 调阈值 -> run。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	gametasks "github.com/novthir-git/game-bot/games/my_garden_world/tasks"
	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/device"
	"github.com/novthir-git/game-bot/internal/state"
	"github.com/novthir-git/game-bot/internal/task"
	"github.com/novthir-git/game-bot/internal/vision"
)

const defaultGameDir = "games/my_garden_world"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]

	var err error
	switch sub {
	case "doctor":
		err = cmdDoctor(args)
	case "capture":
		err = cmdCapture(args)
	case "run":
		err = cmdRun(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", sub)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法: gardenbot <子命令> [参数]

子命令:
  doctor    检查 adb、设备连接、分辨率、前台包名，并列出缺失的模板图
  capture   截一张图保存到本地，用于制作模板
  run       启动调度器，按 tasks.yaml 执行已启用的任务

公共参数:
  -game <目录>   游戏目录，默认 `+defaultGameDir+`

首次使用顺序: doctor -> capture -> 截模板 -> tune 调阈值 -> run
`)
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

// setup 加载配置并连接设备，是三个子命令共用的开头。
func setup(gameDir string) (*config.Bundle, *device.Device, *slog.Logger, error) {
	cfg, err := config.Load(gameDir)
	if err != nil {
		return nil, nil, nil, err
	}
	log := newLogger(cfg.Tasks.Logging.Level)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dev, err := device.Open(ctx, &cfg.Device, log)
	if err != nil {
		return cfg, nil, log, err
	}
	return cfg, dev, log, nil
}

// cmdDoctor 逐项检查环境。
//
// 各项之间互不阻断：设备连不上时，模板清单照样要打印出来。
// 「准备模板图」本来就是连设备之前该做的事，一失败就整个退出等于把顺序搞反了。
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	gameDir := fs.String("game", defaultGameDir, "游戏目录")
	fs.Parse(args)

	// 配置读不出来才是真的没法继续——后面每一项检查都要用它。
	cfg, err := config.Load(*gameDir)
	if err != nil {
		return err
	}
	log := newLogger("error") // doctor 自己打印结果，不需要库里的日志噪音

	var blockers []string
	note := func(format string, a ...any) {
		blockers = append(blockers, fmt.Sprintf(format, a...))
	}

	section("配置")
	fmt.Printf("  游戏          : %s（%s）\n", *gameDir, cfg.Game.App.Name)
	fmt.Printf("  基准分辨率    : %dx%d\n", cfg.Device.Display.BaseWidth, cfg.Device.Display.BaseHeight)
	fmt.Printf("  匹配阈值      : %.2f\n", cfg.Game.Matching.DefaultThreshold)
	if cfg.Game.App.Package == "" {
		fmt.Println("  游戏包名      : 未填写")
		note("game.yaml 的 app.package 为空——把游戏切到前台后重跑本命令即可拿到")
	} else {
		fmt.Printf("  游戏包名      : %s\n", cfg.Game.App.Package)
	}

	// 任务与模板这一段完全不需要设备，放在设备检查之前。
	section("任务与模板")
	store := vision.NewStore(cfg.TemplatesDir())
	sched := task.NewScheduler(task.NewSession(nil, store, cfg, nil, log))
	if err := gametasks.Register(sched, cfg); err != nil {
		fmt.Printf("  任务注册      : 失败 - %v\n", err)
		note("任务注册失败，先修 tasks.yaml")
	} else {
		fmt.Printf("  已启用任务    : %d 个\n", sched.Len())
		required := sched.RequiredTemplates()
		missing := store.Missing(required)
		fmt.Printf("  模板图        : %d/%d 就绪\n", len(required)-len(missing), len(required))
		if len(missing) > 0 {
			fmt.Printf("\n  还缺以下模板（相对 %s）：\n", cfg.TemplatesDir())
			for _, m := range missing {
				fmt.Println("    " + m)
			}
			note("还缺 %d 张模板图", len(missing))
		}
	}

	section("任务进度")
	if st, err := state.Open(cfg.StatePath()); err != nil {
		fmt.Printf("  状态文件      : %v\n", err)
	} else {
		printProgress(st)
	}

	section("设备")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dev, err := device.Open(ctx, &cfg.Device, log)
	if err != nil {
		fmt.Printf("  连接          : 失败 - %v\n", err)
		fmt.Println()
		fmt.Println("  排查方向:")
		fmt.Println("    1. MuMu 12 是否已启动？")
		fmt.Println("    2. adb 端口是否正确？多开实例端口各不相同，可在多开管理器里确认")
		fmt.Println("    3. adb.binary 是否指向 MuMu 自带的 adb？系统里的其他 adb 会互相 kill-server")
		note("设备未连通")
	} else {
		rw, rh := dev.RealSize()
		fmt.Printf("  adb           : %s\n", dev.ADB().Binary())
		fmt.Printf("  设备          : %s\n", dev.ADB().Serial())
		fmt.Printf("  实际分辨率    : %dx%d\n", rw, rh)
		if rw != cfg.Device.Display.BaseWidth || rh != cfg.Device.Display.BaseHeight {
			fmt.Printf("                  与基准 %dx%d 不一致，截图会被缩放后再匹配\n",
				cfg.Device.Display.BaseWidth, cfg.Device.Display.BaseHeight)
			note("模拟器分辨率与基准不一致，建议在 MuMu「设置-显示」里改成 %dx%d",
				cfg.Device.Display.BaseWidth, cfg.Device.Display.BaseHeight)
		}

		// 前台包名：这正是 game.yaml 里两个 TODO 字段的采集方式
		if pkg, act, err := dev.CurrentFocus(ctx); err != nil {
			fmt.Printf("  前台应用      : 读取失败 - %v\n", err)
		} else {
			fmt.Printf("  前台应用      : %s / %s\n", pkg, act)
			if cfg.Game.App.Package == "" {
				fmt.Println()
				fmt.Println("  把下面这段填进 game.yaml 即可：")
				fmt.Printf("    app:\n      package: \"%s\"\n      main_activity: \"%s\"\n", pkg, act)
			} else if cfg.Game.App.Package != pkg {
				fmt.Printf("                  注意：与配置里的 %s 不一致（游戏可能不在前台）\n",
					cfg.Game.App.Package)
			}
		}
	}

	section("结论")
	if len(blockers) == 0 {
		fmt.Println("  环境就绪，可以 gardenbot run 了。")
		return nil
	}
	fmt.Printf("  还有 %d 项待处理：\n", len(blockers))
	for i, b := range blockers {
		fmt.Printf("    %d. %s\n", i+1, b)
	}
	return nil
}

func section(title string) {
	fmt.Printf("\n[%s]\n", title)
}

// printProgress 打印已落盘的任务进度。
// 它读的是 run 写下的状态，用于确认重启后进度确实被保留了下来。
func printProgress(st *state.Store) {
	var rack struct {
		Date     string    `json:"date"`
		Done     int       `json:"done"`
		LastList time.Time `json:"last_list"`
	}
	ok, err := st.Get("flower_rack_cycle", &rack)
	switch {
	case err != nil:
		fmt.Printf("  花架进度      : 读取失败 - %v\n", err)
	case !ok:
		fmt.Println("  花架进度      : 尚无记录")
	default:
		fmt.Printf("  花架进度      : %d 次（%s，上次上架 %s）\n",
			rack.Done, rack.Date, rack.LastList.Format("15:04:05"))
	}
}

func cmdCapture(args []string) error {
	fs := flag.NewFlagSet("capture", flag.ExitOnError)
	gameDir := fs.String("game", defaultGameDir, "游戏目录")
	out := fs.String("o", "", "输出文件路径，默认存到游戏目录的 logs/ 下")
	raw := fs.Bool("raw", false, "保存设备原始分辨率的画面，不缩放到基准分辨率")
	fs.Parse(args)

	cfg, dev, log, err := setup(*gameDir)
	if err != nil {
		return err
	}
	_ = log

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	f, err := dev.Screencap(ctx)
	if err != nil {
		return err
	}
	if *raw {
		rw, rh := dev.RealSize()
		f = f.ResizeTo(rw, rh)
	}

	path := *out
	if path == "" {
		path = filepath.Join(cfg.LogsDir(), fmt.Sprintf("shot_%s.png", time.Now().Format("20060102_150405")))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := f.SavePNG(path); err != nil {
		return err
	}
	fmt.Printf("已保存 %dx%d 截图: %s\n", f.W, f.H, path)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	gameDir := fs.String("game", defaultGameDir, "游戏目录")
	fs.Parse(args)

	cfg, dev, log, err := setup(*gameDir)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.LogsDir(), 0o755); err != nil {
		return err
	}
	st, err := state.Open(cfg.StatePath())
	if err != nil {
		return err
	}

	store := vision.NewStore(cfg.TemplatesDir())
	sess := task.NewSession(dev, store, cfg, st, log)
	sched := task.NewScheduler(sess)
	if err := gametasks.Register(sched, cfg); err != nil {
		return err
	}

	// 缺模板就别启动了。让它跑起来再一个个失败，只会刷满日志还看不出重点。
	if missing := store.Missing(sched.RequiredTemplates()); len(missing) > 0 {
		return fmt.Errorf("缺少 %d 张模板图，先跑 `gardenbot doctor` 看清单:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	// Ctrl-C 取消 context，正在执行的任务会在下一个可中断点停下。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("开始运行", "游戏", cfg.Game.App.Name, "任务数", sched.Len())
	return sched.Run(ctx)
}
