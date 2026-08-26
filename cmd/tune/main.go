// Command tune 是模板调参工具：在一张截图上试跑某个模板，报告匹配分数和位置。
//
// 它是 Go 版本里替代 Python REPL 的那一环。CV 调参本质是「改一个数、看一次结果」
// 的高频循环，把这件事独立成一个秒级启动的小程序，就不必为了试一个阈值
// 去重新编译整个 bot。
//
//	go run ./cmd/tune -screen logs/shot.png -template main/btn_flower_rack.png
//	go run ./cmd/tune -screen logs/shot.png -template x.png -roi 700,380,320,200 -out marked.png
//
// 输出里最该看的不是最高分，而是最高分与次高分的差距：
// 差距大说明模板足够有辨识度，阈值可以放在两者之间；
// 差距小说明这张模板在画面里不唯一，应该重新截一块更有特征的区域。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/novthir-git/game-bot/internal/config"
	"github.com/novthir-git/game-bot/internal/vision"
)

func main() {
	screen := flag.String("screen", "", "截图路径（gardenbot capture 生成）")
	tpl := flag.String("template", "", "模板路径，相对 templates/ 目录，也可以是绝对路径")
	gameDir := flag.String("game", "games/my_garden_world", "游戏目录")
	roiStr := flag.String("roi", "", "限定搜索区域，格式 x,y,w,h")
	threshold := flag.Float64("threshold", 0, "判定阈值，默认取 game.yaml 里的值")
	topN := flag.Int("top", 5, "列出前 N 个互不重叠的候选")
	out := flag.String("out", "", "把标注后的图写到这个路径")
	flag.Parse()

	if *screen == "" || *tpl == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*screen, *tpl, *gameDir, *roiStr, *out, *threshold, *topN); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

func run(screenPath, tplPath, gameDir, roiStr, outPath string, threshold float64, topN int) error {
	cfg, err := config.Load(gameDir)
	if err != nil {
		return err
	}
	if threshold <= 0 {
		threshold = cfg.Game.Matching.DefaultThreshold
	}

	if !filepath.IsAbs(tplPath) {
		if _, err := os.Stat(tplPath); err != nil {
			tplPath = filepath.Join(cfg.TemplatesDir(), filepath.FromSlash(tplPath))
		}
	}

	scr, err := vision.LoadPNG(screenPath)
	if err != nil {
		return err
	}
	ndl, err := vision.LoadPNG(tplPath)
	if err != nil {
		return err
	}

	opts := vision.Options{Threshold: threshold}
	roiDesc := "全图"
	if roiStr != "" {
		r, err := parseRect(roiStr)
		if err != nil {
			return err
		}
		opts.ROI = &r
		roiDesc = fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.W, r.H)
	}

	bw, bh := cfg.Device.Display.BaseWidth, cfg.Device.Display.BaseHeight
	fmt.Printf("截图     : %s (%dx%d)\n", screenPath, scr.W, scr.H)
	fmt.Printf("模板     : %s (%dx%d)\n", tplPath, ndl.W, ndl.H)
	fmt.Printf("搜索区域 : %s\n", roiDesc)
	fmt.Printf("阈值     : %.3f\n", threshold)
	if scr.W != bw || scr.H != bh {
		fmt.Printf("\n注意: 截图尺寸与基准分辨率 %dx%d 不一致，坐标和匹配结果都会偏。\n", bw, bh)
	}
	fmt.Println()

	// 阈值设 0 是为了拿到全部候选，好看清最高分与次高分的差距。
	all := vision.FindAll(scr.Gray(), ndl.Gray(), vision.Options{ROI: opts.ROI, Threshold: 0}, topN)
	if len(all) == 0 {
		fmt.Println("没有任何候选：模板比搜索区域还大，或者图片读取有问题。")
		return nil
	}

	fmt.Printf("%-4s %-10s %-14s %s\n", "排名", "分数", "位置", "判定")
	for i, m := range all {
		verdict := "低于阈值"
		if m.Score >= threshold {
			verdict = "命中"
		}
		cx, cy := m.Rect.Center()
		fmt.Printf("%-4d %-10.4f %-14s %s (中心 %d,%d)\n",
			i+1, m.Score, fmt.Sprintf("%d,%d", m.Rect.X, m.Rect.Y), verdict, cx, cy)
	}

	best := all[0]
	fmt.Println()
	if len(all) > 1 {
		gap := best.Score - all[1].Score
		fmt.Printf("最高分与次高分相差 %.4f。", gap)
		switch {
		case gap >= 0.15:
			mid := all[1].Score + gap/2
			fmt.Printf("差距充足，阈值可放在 %.2f 附近。\n", mid)
		case gap >= 0.05:
			fmt.Println("差距偏小，阈值需要卡得比较紧，画面稍有变化就可能误判。")
			fmt.Println("建议把模板缩小到更有辨识度的部分（按钮上的图标或文字，别带半透明面板）。")
		default:
			fmt.Println("差距过小，这张模板在画面里不唯一，几乎一定会误匹配。")
			fmt.Println("请重新截取：换一块画面中独一无二、且不在动画区域的部分。")
		}
	}
	if best.Score < threshold {
		fmt.Printf("\n当前阈值 %.3f 下判定为未命中。若肉眼确认位置是对的，可以：\n", threshold)
		fmt.Println("  1. 缩小模板范围，去掉会变化的部分（数字、进度、光效）")
		fmt.Println("  2. 用 -threshold 单独为这张模板放宽，不要去改全局默认值")
	}

	if outPath != "" {
		marked := scr.Clone()
		for i, m := range all {
			c := vision.RGB{R: 255, G: 60, B: 60} // 最佳命中画红框
			if i > 0 {
				c = vision.RGB{R: 60, G: 160, B: 255} // 其余候选画蓝框
			}
			marked.DrawRect(m.Rect, c, 2)
		}
		if err := marked.SavePNG(outPath); err != nil {
			return err
		}
		fmt.Printf("\n已写出标注图: %s（红框为最佳命中，蓝框为其余候选）\n", outPath)
	}
	return nil
}

func parseRect(s string) (vision.Rect, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return vision.Rect{}, fmt.Errorf("roi 格式应为 x,y,w,h，实得 %q", s)
	}
	var v [4]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return vision.Rect{}, fmt.Errorf("roi 第 %d 项不是整数: %q", i+1, p)
		}
		v[i] = n
	}
	return vision.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}, nil
}
