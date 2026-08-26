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
	"strconv"
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
	case "crop":
		err = cmdCrop(args)
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
  crop      从截图里裁出一张模板，并立刻验证它在原图中是否唯一
  run       启动调度器，按 tasks.yaml 执行已启用的任务

公共参数:
  -game <目录>   游戏目录，默认 `+defaultGameDir+`

首次使用顺序: doctor -> capture -> crop -> run

crop 用法:
  # 先生成带坐标网格的图，照着读出要裁的区域
  gardenbot crop -screen logs/shot.png -grid logs/grid.png

  # 裁出模板；保存后会自动回到原图验证一遍
  gardenbot crop -screen logs/shot.png -rect 880,520,96,44 -o main/btn_pearl.png
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

// cmdCrop 从截图里裁出一张模板。
//
// 裁完立刻拿它回到原截图里搜一遍，这是本命令存在的主要理由：
// 一张在画面中不唯一的模板，运行时会稳定地点错地方，而这种错误
// 在日志里表现为「点了但没反应」，极难倒推。裁剪当场就能发现。
func cmdCrop(args []string) error {
	fs := flag.NewFlagSet("crop", flag.ExitOnError)
	gameDir := fs.String("game", defaultGameDir, "游戏目录")
	screen := fs.String("screen", "", "源截图路径")
	rectStr := fs.String("rect", "", "裁剪区域，格式 x,y,w,h")
	out := fs.String("o", "", "输出模板路径，相对 templates/ 目录，如 main/btn_pearl.png")
	grid := fs.String("grid", "", "不裁剪，改为把带坐标网格的截图写到该路径")
	gridStep := fs.Int("grid-step", 50, "网格线间隔像素，每 5 条加粗并标注坐标")
	force := fs.Bool("force", false, "允许覆盖已存在的模板文件")
	fs.Parse(args)

	if *screen == "" {
		return fmt.Errorf("必须指定 -screen（用 gardenbot capture 生成）")
	}
	cfg, err := config.Load(*gameDir)
	if err != nil {
		return err
	}
	src, err := vision.LoadPNG(*screen)
	if err != nil {
		return err
	}

	bw, bh := cfg.Device.Display.BaseWidth, cfg.Device.Display.BaseHeight
	if src.W != bw || src.H != bh {
		fmt.Printf("警告: 截图为 %dx%d，与基准分辨率 %dx%d 不一致。\n", src.W, src.H, bw, bh)
		fmt.Println("      从它裁出的模板在运行时匹配不上。请先把模拟器分辨率固定成基准值再重新截图。")
		fmt.Println()
	}

	// 网格模式：只是帮人读坐标，不产出模板
	if *grid != "" {
		g := src.Clone()
		g.DrawGrid(*gridStep, 5)
		if err := os.MkdirAll(filepath.Dir(*grid), 0o755); err != nil {
			return err
		}
		if err := g.SavePNG(*grid); err != nil {
			return err
		}
		fmt.Printf("已写出坐标网格图: %s\n", *grid)
		fmt.Printf("细线每 %d 像素，粗线（红色）每 %d 像素并标注坐标。\n", *gridStep, *gridStep*5)
		fmt.Println("照着读出区域后：gardenbot crop -screen <截图> -rect x,y,w,h -o <路径>")
		return nil
	}

	if *rectStr == "" || *out == "" {
		return fmt.Errorf("裁剪需要同时指定 -rect 和 -o（先用 -grid 生成网格图找坐标）")
	}
	r, err := parseRect(*rectStr)
	if err != nil {
		return err
	}
	clipped := r.Clip(src.W, src.H)
	if clipped.W != r.W || clipped.H != r.H {
		return fmt.Errorf("裁剪区域 %d,%d %dx%d 超出截图范围 %dx%d",
			r.X, r.Y, r.W, r.H, src.W, src.H)
	}
	if r.W <= 0 || r.H <= 0 {
		return fmt.Errorf("裁剪区域的宽高必须为正数")
	}

	tpl := src.Crop(r)
	gray := tpl.Gray()
	sd := gray.StdDev()

	fmt.Printf("裁剪区域 : %d,%d %dx%d\n", r.X, r.Y, r.W, r.H)
	fmt.Printf("像素标准差: %.1f\n", sd)

	// 纯色模板在 ZNCC 下分母为零，会被判 0 分，也就是永远匹配不上。
	// 与其让它写进去、等运行时才发现，不如现在就拒绝。
	if sd < 3 {
		return fmt.Errorf("这块区域几乎是纯色（标准差 %.1f），做模板永远匹配不上。"+
			"请改选带边框、图标或文字的区域", sd)
	}
	if sd < 10 {
		fmt.Printf("警告: 对比度偏低（标准差 %.1f），匹配可能不稳。建议选明暗反差更明显的部分。\n", sd)
	}
	if r.W < 12 || r.H < 12 {
		fmt.Printf("警告: 模板尺寸 %dx%d 偏小，容易在画面里撞车。建议至少 20x20。\n", r.W, r.H)
	}
	if r.W > src.W/3 || r.H > src.H/3 {
		fmt.Printf("警告: 模板占了画面的很大一块，里面很可能混进了会变化的内容" +
			"（数字、进度、光效）。建议收窄到按钮上的图标或文字。\n")
	}
	checkNaming(*out)

	dst := *out
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(cfg.TemplatesDir(), filepath.FromSlash(*out))
	}
	if _, err := os.Stat(dst); err == nil && !*force {
		return fmt.Errorf("%s 已存在。确认要覆盖请加 -force", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := tpl.SavePNG(dst); err != nil {
		return err
	}
	fmt.Printf("已保存   : %s\n\n", dst)

	verifyTemplate(src, gray, r, cfg.Game.Matching.DefaultThreshold)
	return nil
}

// checkNaming 对照 templates/README.md 的命名规范给出提示。
func checkNaming(name string) {
	base := strings.ToLower(filepath.Base(name))
	for _, p := range []string{"anchor_", "btn_", "state_", "icon_"} {
		if strings.HasPrefix(base, p) {
			return
		}
	}
	fmt.Printf("提示: 文件名 %q 不符合命名规范，建议用 anchor_ / btn_ / state_ / icon_ 前缀。\n", base)
}

// verifyTemplate 把刚裁好的模板放回原截图里搜一遍，报告它是否唯一。
func verifyTemplate(src *vision.Frame, tpl *vision.Gray, want vision.Rect, threshold float64) {
	hits := vision.FindAll(src.Gray(), tpl, vision.Options{Threshold: 0}, 4)
	if len(hits) == 0 {
		fmt.Println("验证: 异常——在原图里反而找不到自己，请检查截图是否损坏。")
		return
	}

	best := hits[0]
	fmt.Println("验证（把模板放回原截图里搜）:")
	for i, h := range hits {
		mark := "  "
		if h.Rect.X == want.X && h.Rect.Y == want.Y {
			mark = "<-" // 标出裁剪时选的那处
		}
		fmt.Printf("  %d. %.4f  @ %d,%d %s\n", i+1, h.Score, h.Rect.X, h.Rect.Y, mark)
		if i == 0 && (h.Rect.X != want.X || h.Rect.Y != want.Y) {
			fmt.Println("     注意: 最高分不在你裁剪的位置上，说明画面里有更像它的地方。")
		}
	}
	fmt.Println()

	if len(hits) == 1 {
		fmt.Println("结论: 画面里只有这一处，模板唯一。")
		return
	}
	gap := best.Score - hits[1].Score
	fmt.Printf("最高分与次高分相差 %.4f。", gap)
	switch {
	case gap >= 0.15:
		fmt.Printf("差距充足，当前阈值 %.2f 可用。\n", threshold)
	case gap >= 0.05:
		fmt.Println("差距偏小，画面稍有变化就可能误判。")
		fmt.Println("建议: 把裁剪范围收窄到更有辨识度的部分，或在任务里给这次查找加 ROI 限定搜索区域。")
	default:
		fmt.Println("差距过小，这张模板在画面里不唯一，运行时几乎一定会点错地方。")
		fmt.Println("建议: 重新选一块画面中独一无二、且不在动画区域的区域。")
	}

	// ROI 是最省事的补救手段：即使模板本身不够独特，
	// 限定搜索区域后其他相似处根本不会进入搜索范围。
	pad := 40
	roi := vision.Rect{X: want.X - pad, Y: want.Y - pad, W: want.W + 2*pad, H: want.H + 2*pad}.
		Clip(src.W, src.H)
	fmt.Printf("\n可用的 ROI（任务代码里传给 task.WithROI）:\n")
	fmt.Printf("  vision.Rect{X: %d, Y: %d, W: %d, H: %d}\n", roi.X, roi.Y, roi.W, roi.H)
}

// parseRect 解析 "x,y,w,h"。
func parseRect(s string) (vision.Rect, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return vision.Rect{}, fmt.Errorf("rect 格式应为 x,y,w,h，实得 %q", s)
	}
	var v [4]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return vision.Rect{}, fmt.Errorf("rect 第 %d 项不是整数: %q", i+1, p)
		}
		v[i] = n
	}
	return vision.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}, nil
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
