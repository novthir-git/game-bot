package vision

// 比色是本项目最廉价也最可靠的判定手段：单点判断耗时在纳秒级且没有误差，
// 凡是「按钮亮还是灰」「红点在不在」「开关开没开」这类问题都应优先用它，
// 而不是模板匹配，更不是 OCR。

// RGB 是一个颜色采样点的值。
type RGB struct{ R, G, B uint8 }

// ColorAt 返回 (x,y) 处的 RGB。
func ColorAt(f *Frame, x, y int) RGB {
	r, g, b, _ := f.At(x, y)
	return RGB{r, g, b}
}

// Dist 返回两色在 RGB 空间的切比雪夫距离（三通道差值的最大者）。
// 比欧氏距离便宜，且对「某一个通道差很多」更敏感，正合判定用途。
func Dist(a, b RGB) int {
	d := 0
	for _, v := range [3]int{abs(int(a.R) - int(b.R)), abs(int(a.G) - int(b.G)), abs(int(a.B) - int(b.B))} {
		if v > d {
			d = v
		}
	}
	return d
}

// Saturation 返回颜色的饱和度差值，即 max(r,g,b) - min(r,g,b)。
func Saturation(c RGB) int {
	hi, lo := int(c.R), int(c.R)
	for _, v := range []int{int(c.G), int(c.B)} {
		if v > hi {
			hi = v
		}
		if v < lo {
			lo = v
		}
	}
	return hi - lo
}

// IsGrayedOut 判断某点是否为置灰状态。
//
// 游戏里按钮不可用时通常做去饱和处理，三通道会趋于接近。
// tol 建议取 20 左右；不同游戏的置灰程度不同，需要用 cmd/tune 实测一次。
func IsGrayedOut(f *Frame, x, y int, tol int) bool {
	return Saturation(ColorAt(f, x, y)) < tol
}

// ColorPoint 是一个「该点应该是什么颜色」的断言。
type ColorPoint struct {
	X, Y  int
	Color RGB
	Tol   int // 允许的颜色偏差，0 表示用调用方给的默认值
}

// MatchColorPoints 做多点比色：所有点都落在容差内才算命中。
//
// 这是按键精灵一类工具里「多点找色」的等价物，也是判定界面状态最快的方式。
// 选点原则：挑颜色稳定、不在动画区域、彼此分散的 3~5 个点。
func MatchColorPoints(f *Frame, pts []ColorPoint, defaultTol int) bool {
	if len(pts) == 0 {
		return false
	}
	for _, p := range pts {
		tol := p.Tol
		if tol <= 0 {
			tol = defaultTol
		}
		if Dist(ColorAt(f, p.X, p.Y), p.Color) > tol {
			return false
		}
	}
	return true
}

// BarRatio 沿水平方向扫描一条进度条，返回已填充部分的比例（0..1）。
//
// 用于体力条、成长进度这类连续量——比 OCR 读数字稳得多，
// 而且我们通常只需要知道「是不是快满了」，并不需要精确数值。
// filled 判定某个像素是否属于已填充部分，由调用方按实际配色提供。
func BarRatio(f *Frame, bar Rect, filled func(RGB) bool) float64 {
	bar = bar.Clip(f.W, f.H)
	if bar.W <= 0 || bar.H <= 0 {
		return 0
	}
	y := bar.Y + bar.H/2 // 取中线，避开上下边框
	last := -1
	for x := 0; x < bar.W; x++ {
		if filled(ColorAt(f, bar.X+x, y)) {
			last = x
		}
	}
	if last < 0 {
		return 0
	}
	return float64(last+1) / float64(bar.W)
}
