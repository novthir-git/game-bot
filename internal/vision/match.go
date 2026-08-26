package vision

import (
	"math"
	"runtime"
	"sort"
	"sync"
)

// Match 是一次模板匹配的结果。
type Match struct {
	Rect  Rect    // 命中区域（在被搜索图的坐标系下）
	Score float64 // 0..1，越大越像
}

// Options 控制一次匹配。
type Options struct {
	// ROI 限定搜索区域，nil 表示全图。
	// 强烈建议填写：已知目标大概位置时，搜索区从 1280x720 缩到 300x200 可省掉十几倍计算。
	ROI *Rect

	// Threshold 判定为命中的最低分数。本游戏为水彩低对比度风格，建议 0.75~0.80 起调。
	Threshold float64

	// Step 是粗搜阶段在模板窗口内的像素抽样步长。0 表示按模板尺寸自动选择，1 表示逐像素。
	// 注意这里抽样的是「窗口内的像素」，不是「搜索位置」——所有位置都会被评估，
	// 因此不存在漏掉峰值的风险，详见下方说明。
	Step int
}

// 评分采用零均值归一化互相关（ZNCC），与 OpenCV 的 TM_CCOEFF_NORMED 同一量纲，
// 因此社区常见的阈值经验（0.7/0.8/0.9）可以直接套用。
//
// ZNCC 对整体亮度平移和对比度缩放不敏感，这正是本游戏需要的——
// 同一个按钮在白天/夜晚场景、不同光效下的绝对灰度值会变，但明暗关系不变。
//
// 加速方式：粗搜阶段在模板窗口内每隔 step 个像素取一个样，
// 计算量降为 1/step²，随后只对得分最高的少数几个位置做全密度复算。
//
// 这里刻意没有采用图像金字塔。金字塔需要把底图和模板各自独立降采样，
// 当目标位置的坐标不是缩放因子的整数倍时，两者的采样相位会错开，
// 对高频内容（细密纹理、小字号文字）足以让相关性归零而漏检。
// 窗口内抽样则始终从底图和模板的同一相对位置取样，不存在这个问题。
type tplStats struct {
	n   float64
	sum float64
	den float64 // ΣT² - (ΣT)²/n
}

func tplStatsStep(ndl *Gray, step int) tplStats {
	var sum, sum2, n float64
	for y := 0; y < ndl.H; y += step {
		row := y * ndl.W
		for x := 0; x < ndl.W; x += step {
			f := float64(ndl.Pix[row+x])
			sum += f
			sum2 += f * f
			n++
		}
	}
	return tplStats{n: n, sum: sum, den: sum2 - sum*sum/n}
}

// scoreAtStep 计算模板左上角落在 (px,py) 时的 ZNCC 分数，窗口内每 step 像素取一样本。
//
// 累加全部用整数：像素值 0..255，即使 256x256 的模板，ΣIT 上界也只有 4.3e9，
// int64 绰绰有余。整数乘加比 float64 快数倍，而这里是全局最热的循环。
func scoreAtStep(hay, ndl *Gray, px, py int, ts tplStats, step int) float64 {
	var sumI, sumI2, sumIT int64
	for y := 0; y < ndl.H; y += step {
		base := (py+y)*hay.W + px
		hRow := hay.Pix[base : base+ndl.W]
		nRow := ndl.Pix[y*ndl.W : y*ndl.W+ndl.W]
		for x := 0; x < ndl.W; x += step {
			i := int64(hRow[x])
			t := int64(nRow[x])
			sumI += i
			sumI2 += i * i
			sumIT += i * t
		}
	}
	fI, fI2, fIT := float64(sumI), float64(sumI2), float64(sumIT)
	denI := fI2 - fI*fI/ts.n
	// 模板或窗口任一为纯色时相关性无定义。模板纯色属于选图失误，
	// 这里返回 0 让它匹配不上，比返回一个虚高的分数安全。
	if denI <= 1e-9 || ts.den <= 1e-9 {
		return 0
	}
	num := fIT - fI*ts.sum/ts.n
	sc := num / math.Sqrt(denI*ts.den)
	if sc < 0 {
		return 0 // 负相关对我们没有意义，统一压到 0
	}
	return sc
}

// scoreMapStep 在 roi 范围内评估每一个合法位置。
// 返回的 scores 按行优先排列，(i%sw + ox, i/sw + oy) 是对应的模板左上角坐标。
func scoreMapStep(hay, ndl *Gray, roi Rect, step int) (scores []float64, sw, sh, ox, oy int) {
	sw = roi.W - ndl.W + 1
	sh = roi.H - ndl.H + 1
	if sw <= 0 || sh <= 0 {
		return nil, 0, 0, 0, 0
	}
	ox, oy = roi.X, roi.Y
	scores = make([]float64, sw*sh)
	ts := tplStatsStep(ndl, step)

	workers := min(runtime.NumCPU(), sh)
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	rows := (sh + workers - 1) / workers
	for w := 0; w < workers; w++ {
		y0 := w * rows
		y1 := min(y0+rows, sh)
		if y0 >= y1 {
			continue
		}
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			for sy := y0; sy < y1; sy++ {
				for sx := 0; sx < sw; sx++ {
					scores[sy*sw+sx] = scoreAtStep(hay, ndl, ox+sx, oy+sy, ts, step)
				}
			}
		}(y0, y1)
	}
	wg.Wait()
	return scores, sw, sh, ox, oy
}

// autoStep 按模板尺寸挑抽样步长：保证抽样后每轴至少还剩 10 个样本点，
// 样本太少会让候选排序变得不稳定。步长上限 4（即最多 16 倍加速）。
// 抽样只影响候选排序，最终分数总是全密度复算，所以放宽一点是安全的。
func autoStep(ndl *Gray) int {
	s := 4
	for s > 1 && (ndl.W/s < 10 || ndl.H/s < 10) {
		s--
	}
	return s
}

// refineFull 在候选点 ±step 的邻域内做全密度评分，返回其中最好的一处。
//
// 这一步不能省：抽样评分的极大值往往落在真实峰值旁边一两个像素处，
// 非极大值抑制又会把真实峰值当作它的邻居吞掉。若直接在候选点上复算全密度分数，
// 边缘锐利的模板（按钮描边只有 2px）会因为这一两个像素的偏移而掉到阈值以下。
func refineFull(hay, ndl *Gray, p point, step int, roi Rect, full tplStats) Match {
	best := Match{}
	x0, y0 := max(p.X-step, roi.X), max(p.Y-step, roi.Y)
	x1, y1 := min(p.X+step, roi.X+roi.W-ndl.W), min(p.Y+step, roi.Y+roi.H-ndl.H)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if sc := scoreAtStep(hay, ndl, x, y, full, 1); sc > best.Score {
				best = Match{Rect: Rect{X: x, Y: y, W: ndl.W, H: ndl.H}, Score: sc}
			}
		}
	}
	return best
}

func resolve(hay, ndl *Gray, opt Options) (roi Rect, step int, ok bool) {
	roi = Rect{0, 0, hay.W, hay.H}
	if opt.ROI != nil {
		roi = opt.ROI.Clip(hay.W, hay.H)
	}
	if roi.W < ndl.W || roi.H < ndl.H || ndl.W == 0 || ndl.H == 0 {
		return roi, 1, false
	}
	step = opt.Step
	if step <= 0 {
		step = autoStep(ndl)
	}
	return roi, step, true
}

// Find 在 hay 中找 ndl 最像的一处。
func Find(hay, ndl *Gray, opt Options) (Match, bool) {
	roi, step, ok := resolve(hay, ndl, opt)
	if !ok {
		return Match{}, false
	}
	scores, sw, _, ox, oy := scoreMapStep(hay, ndl, roi, step)
	if scores == nil {
		return Match{}, false
	}
	best := Match{}
	if step == 1 {
		// 已是全密度，直接取极大值
		bi := argmax(scores)
		best = Match{Rect: Rect{X: ox + bi%sw, Y: oy + bi/sw, W: ndl.W, H: ndl.H}, Score: scores[bi]}
	} else {
		// 抽样分数只用来排序候选，最终分数一律全密度复算，
		// 否则阈值的含义会随 step 漂移。
		full := tplStatsStep(ndl, 1)
		for _, p := range topPositions(scores, sw, ox, oy, ndl, 6, 0) {
			if m := refineFull(hay, ndl, p, step, roi, full); m.Score > best.Score {
				best = m
			}
		}
	}
	return best, best.Score >= opt.Threshold
}

// FindAll 找出所有达到阈值的位置，做非极大值抑制去重。
// 用于「花架上有几个空槽位」这类需要枚举同类元素的场景。
func FindAll(hay, ndl *Gray, opt Options, maxN int) []Match {
	roi, step, ok := resolve(hay, ndl, opt)
	if !ok {
		return nil
	}
	scores, sw, _, ox, oy := scoreMapStep(hay, ndl, roi, step)
	if scores == nil {
		return nil
	}
	// 抽样阶段把门槛放宽，避免抽样误差把边缘命中提前筛掉
	cut := opt.Threshold
	if step > 1 {
		cut -= 0.1
	}
	full := tplStatsStep(ndl, 1)
	var out []Match
	for _, p := range topPositions(scores, sw, ox, oy, ndl, 0, cut) {
		m := refineFull(hay, ndl, p, step, roi, full)
		if m.Score < opt.Threshold {
			continue
		}
		out = append(out, m)
		if maxN > 0 && len(out) >= maxN {
			break
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	return out
}

type point struct{ X, Y int }

// topPositions 按分数从高到低挑位置，并做非极大值抑制：
// 相距不到半个模板宽高的两处视为同一个峰值，只保留分数更高的那个。
// limit<=0 表示不限个数；cut 是入选的最低分数。
func topPositions(scores []float64, sw, ox, oy int, ndl *Gray, limit int, cut float64) []point {
	type cand struct {
		p point
		s float64
	}
	cands := make([]cand, 0, 64)
	for i, s := range scores {
		if s >= cut {
			cands = append(cands, cand{point{ox + i%sw, oy + i/sw}, s})
		}
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].s > cands[b].s })

	minDX, minDY := max(ndl.W/2, 1), max(ndl.H/2, 1)
	out := make([]point, 0, 8)
	for _, c := range cands {
		dup := false
		for _, o := range out {
			if abs(o.X-c.p.X) < minDX && abs(o.Y-c.p.Y) < minDY {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, c.p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func argmax(s []float64) int {
	bi := 0
	for i, v := range s {
		if v > s[bi] {
			bi = i
		}
	}
	return bi
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
