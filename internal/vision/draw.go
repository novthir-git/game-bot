package vision

import "math"

// 这里的绘制能力只服务于两件事：把匹配结果标出来看，
// 以及给截图叠一层坐标网格好让人读出裁剪区域。
// 因此只实现直线、矩形和数字，不引入任何字体库。

// digits5x7 是 0-9 的 5x7 点阵字形，每个数字 7 行，每行低 5 位有效。
//
// 用 5x7 而不是更省地方的 3x5：3x5 总共只有 15 个点，8 和 9、5 和 6
// 这类数字在任何 3x5 字体里都只差一笔，小尺寸下极易读错。
// 而这些数字是用来读取裁剪坐标的，读错一位就会裁到完全不同的位置。
//
// 自带点阵而不用 golang.org/x/image/font：只需要显示十个数字，
// 七十个字节就够了，为此拉一个字体依赖并不划算。
var digits5x7 = [10][7]uint8{
	{0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110}, // 0
	{0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110}, // 1
	{0b01110, 0b10001, 0b00001, 0b00010, 0b00100, 0b01000, 0b11111}, // 2
	{0b11111, 0b00010, 0b00100, 0b00010, 0b00001, 0b10001, 0b01110}, // 3
	{0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010}, // 4
	{0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110}, // 5
	{0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110}, // 6
	{0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000}, // 7
	{0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110}, // 8
	{0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100}, // 9
}

const (
	glyphW   = 5
	glyphH   = 7
	glyphGap = 1
)

// DigitWidth 返回按给定缩放绘制 n 位数字所需的宽度（含字间距）。
func DigitWidth(n, scale int) int { return n * (glyphW + glyphGap) * scale }

// DigitHeight 返回按给定缩放绘制一行数字所需的高度。
func DigitHeight(scale int) int { return glyphH * scale }

func (f *Frame) setPx(x, y int, c RGB) {
	if !f.inBounds(x, y) {
		return
	}
	i := (y*f.W + x) * 4
	f.Pix[i], f.Pix[i+1], f.Pix[i+2], f.Pix[i+3] = c.R, c.G, c.B, 255
}

// DrawHLine 画一条水平线。
func (f *Frame) DrawHLine(x0, x1, y int, c RGB) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		f.setPx(x, y, c)
	}
}

// DrawVLine 画一条垂直线。
func (f *Frame) DrawVLine(x, y0, y1 int, c RGB) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		f.setPx(x, y, c)
	}
}

// FillRect 用纯色填充一块矩形，用于给数字垫底以保证可读。
func (f *Frame) FillRect(r Rect, c RGB) {
	r = r.Clip(f.W, f.H)
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			f.setPx(x, y, c)
		}
	}
}

// DrawNumber 在 (x,y) 处绘制一个非负整数，scale 为放大倍数。
func (f *Frame) DrawNumber(x, y, v, scale int, c RGB) {
	if scale < 1 {
		scale = 1
	}
	if v < 0 {
		v = 0
	}
	var ds []int
	if v == 0 {
		ds = []int{0}
	}
	for v > 0 {
		ds = append([]int{v % 10}, ds...)
		v /= 10
	}
	for i, d := range ds {
		g := digits5x7[d]
		ox := x + i*(glyphW+glyphGap)*scale
		for row := 0; row < glyphH; row++ {
			for col := 0; col < glyphW; col++ {
				if g[row]&(1<<(glyphW-1-col)) == 0 {
					continue
				}
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						f.setPx(ox+col*scale+sx, y+row*scale+sy, c)
					}
				}
			}
		}
	}
}

// DrawGrid 叠加一层坐标网格，每 step 像素一条细线，每 major 条细线加粗并标注坐标。
//
// 用途是在没有图像编辑器的情况下，靠肉眼读出要裁剪的区域坐标。
func (f *Frame) DrawGrid(step, major int) {
	if step < 4 {
		step = 100
	}
	if major < 1 {
		major = 5
	}
	// 坐标标签要能被一眼读准——读错一位就会裁到完全不同的位置。
	// 5x7 字形放大 2 倍是 10x14 像素，足够清晰。
	const labelScale = 2

	minor := RGB{R: 255, G: 255, B: 255}
	strong := RGB{R: 255, G: 40, B: 40}
	label := RGB{R: 255, G: 255, B: 255}
	labelBg := RGB{R: 0, G: 0, B: 0}

	for x := 0; x < f.W; x += step {
		c, isMajor := minor, (x/step)%major == 0
		if isMajor {
			c = strong
		}
		f.DrawVLine(x, 0, f.H-1, c)
		if isMajor && x > 0 {
			f.drawLabel(x, 0, x, labelScale, label, labelBg)
		}
	}
	for y := 0; y < f.H; y += step {
		c, isMajor := minor, (y/step)%major == 0
		if isMajor {
			c = strong
		}
		f.DrawHLine(0, f.W-1, y, c)
		if isMajor && y > 0 {
			f.drawLabel(0, y, y, labelScale, label, labelBg)
		}
	}
}

// drawLabel 在网格线旁写一个坐标数字。
// 贴近右边缘或下边缘时改画到线的另一侧，否则最外侧那个标签会被裁掉一半，
// 而它恰恰是最容易需要读的那个。
func (f *Frame) drawLabel(gx, gy, value, scale int, fg, bg RGB) {
	w := DigitWidth(len(itoaLen(value)), scale)
	h := DigitHeight(scale)

	x, y := gx+2, gy+2
	if x+w+2 > f.W {
		x = gx - w - 4 // 溢出右边缘，改画到线左侧
	}
	if y+h+2 > f.H {
		y = gy - h - 4 // 溢出下边缘，改画到线上方
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	f.FillRect(Rect{X: x, Y: y, W: w + 2, H: h + 2}, bg)
	f.DrawNumber(x+1, y+1, value, scale, fg)
}

func itoaLen(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// StdDev 返回灰度图的像素标准差，用来判断一块区域是不是几乎纯色。
//
// 纯色模板在 ZNCC 下相关性无定义（分母为零），匹配时会被判为 0 分，
// 也就是永远匹配不上。裁剪时提前测一下，比等到运行时才发现要好。
func (g *Gray) StdDev() float64 {
	if len(g.Pix) == 0 {
		return 0
	}
	var sum, sum2 float64
	for _, v := range g.Pix {
		f := float64(v)
		sum += f
		sum2 += f * f
	}
	n := float64(len(g.Pix))
	variance := sum2/n - (sum/n)*(sum/n)
	if variance <= 0 {
		return 0
	}
	return math.Sqrt(variance)
}
