// Package vision 提供本项目全部的图像识别能力：模板匹配、比色、进度条测量。
//
// 设计约束：不引入 OpenCV / cgo，全部为纯 Go 实现。
// 本游戏节奏为分钟级，对识别延迟不敏感，纯 Go 的性能余量充足。
package vision

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
)

// Rect 是图像上的一块矩形区域，坐标为左上角原点。
type Rect struct {
	X, Y, W, H int
}

func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Clip 把矩形裁到 w x h 的画布范围内。
func (r Rect) Clip(w, h int) Rect {
	if r.X < 0 {
		r.W += r.X
		r.X = 0
	}
	if r.Y < 0 {
		r.H += r.Y
		r.Y = 0
	}
	if r.X+r.W > w {
		r.W = w - r.X
	}
	if r.Y+r.H > h {
		r.H = h - r.Y
	}
	if r.W < 0 {
		r.W = 0
	}
	if r.H < 0 {
		r.H = 0
	}
	return r
}

// Center 返回矩形中心点。点击时统一点中心，而不是左上角。
func (r Rect) Center() (int, int) { return r.X + r.W/2, r.Y + r.H/2 }

// Frame 是一帧彩色截图，像素按 RGBA 排列，每像素 4 字节。
type Frame struct {
	W, H int
	Pix  []uint8
}

func NewFrame(w, h int) *Frame {
	return &Frame{W: w, H: h, Pix: make([]uint8, w*h*4)}
}

func (f *Frame) inBounds(x, y int) bool { return x >= 0 && y >= 0 && x < f.W && y < f.H }

// At 返回 (x,y) 处的 RGBA。越界返回全 0，调用方需自行保证坐标合法。
func (f *Frame) At(x, y int) (r, g, b, a uint8) {
	if !f.inBounds(x, y) {
		return 0, 0, 0, 0
	}
	i := (y*f.W + x) * 4
	return f.Pix[i], f.Pix[i+1], f.Pix[i+2], f.Pix[i+3]
}

func (f *Frame) Crop(r Rect) *Frame {
	r = r.Clip(f.W, f.H)
	out := NewFrame(r.W, r.H)
	for y := 0; y < r.H; y++ {
		src := ((r.Y+y)*f.W + r.X) * 4
		dst := y * r.W * 4
		copy(out.Pix[dst:dst+r.W*4], f.Pix[src:src+r.W*4])
	}
	return out
}

// Gray 把彩色帧转为灰度。
//
// 本项目所有模板匹配都在灰度空间进行：游戏为水彩渐变风格，
// 色相随光效和时段变化较大，而明度关系相对稳定。
func (f *Frame) Gray() *Gray {
	g := NewGray(f.W, f.H)
	for i, j := 0, 0; i < len(g.Pix); i, j = i+1, j+4 {
		// ITU-R BT.601 luma，整数运算避免浮点开销
		g.Pix[i] = uint8((299*int(f.Pix[j]) + 587*int(f.Pix[j+1]) + 114*int(f.Pix[j+2])) / 1000)
	}
	return g
}

func (f *Frame) ToImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	copy(img.Pix, f.Pix)
	return img
}

func (f *Frame) SavePNG(path string) error {
	fp, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fp.Close()
	return png.Encode(fp, f.ToImage())
}

// LoadPNG 读取一张 PNG 为 Frame，用于加载模板图和离线调参。
func LoadPNG(path string) (*Frame, error) {
	fp, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fp.Close()
	img, err := png.Decode(fp)
	if err != nil {
		return nil, fmt.Errorf("解码 PNG %s: %w", path, err)
	}
	b := img.Bounds()
	out := NewFrame(b.Dx(), b.Dy())
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bb, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*out.W + x) * 4
			out.Pix[i] = uint8(r >> 8)
			out.Pix[i+1] = uint8(g >> 8)
			out.Pix[i+2] = uint8(bb >> 8)
			out.Pix[i+3] = uint8(a >> 8)
		}
	}
	return out, nil
}

// Gray 是灰度图，每像素 1 字节。
type Gray struct {
	W, H int
	Pix  []uint8
}

func NewGray(w, h int) *Gray {
	return &Gray{W: w, H: h, Pix: make([]uint8, w*h)}
}

func (g *Gray) At(x, y int) uint8 { return g.Pix[y*g.W+x] }

func (g *Gray) Crop(r Rect) *Gray {
	r = r.Clip(g.W, g.H)
	out := NewGray(r.W, r.H)
	for y := 0; y < r.H; y++ {
		src := (r.Y+y)*g.W + r.X
		copy(out.Pix[y*r.W:(y+1)*r.W], g.Pix[src:src+r.W])
	}
	return out
}

// Downscale 按整数因子做盒式降采样，用于金字塔粗搜。
// factor <= 1 时返回原图。
func (g *Gray) Downscale(factor int) *Gray {
	if factor <= 1 {
		return g
	}
	w, h := g.W/factor, g.H/factor
	if w < 1 || h < 1 {
		return g
	}
	out := NewGray(w, h)
	area := factor * factor
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum := 0
			for dy := 0; dy < factor; dy++ {
				row := (y*factor + dy) * g.W
				for dx := 0; dx < factor; dx++ {
					sum += int(g.Pix[row+x*factor+dx])
				}
			}
			out.Pix[y*w+x] = uint8(sum / area)
		}
	}
	return out
}

// ResizeTo 双线性缩放到指定尺寸。尺寸相同时直接返回原图，不做拷贝。
//
// 用途：模拟器实际分辨率与模板基准分辨率不一致时，把截图归一化到基准分辨率，
// 这样模板图永远只需要一套。按文档把模拟器固定在 1280x720 时这里是空操作。
func (f *Frame) ResizeTo(w, h int) *Frame {
	if w == f.W && h == f.H {
		return f
	}
	if w <= 0 || h <= 0 || f.W <= 0 || f.H <= 0 {
		return f
	}
	out := NewFrame(w, h)
	xr := float64(f.W) / float64(w)
	yr := float64(f.H) / float64(h)
	for y := 0; y < h; y++ {
		sy := (float64(y)+0.5)*yr - 0.5
		y0 := int(math.Floor(sy))
		fy := sy - float64(y0)
		y1 := y0 + 1
		y0, y1 = clampi(y0, 0, f.H-1), clampi(y1, 0, f.H-1)
		for x := 0; x < w; x++ {
			sx := (float64(x)+0.5)*xr - 0.5
			x0 := int(math.Floor(sx))
			fx := sx - float64(x0)
			x1 := x0 + 1
			x0, x1 = clampi(x0, 0, f.W-1), clampi(x1, 0, f.W-1)

			i00, i01 := (y0*f.W+x0)*4, (y0*f.W+x1)*4
			i10, i11 := (y1*f.W+x0)*4, (y1*f.W+x1)*4
			o := (y*w + x) * 4
			for c := 0; c < 4; c++ {
				top := float64(f.Pix[i00+c])*(1-fx) + float64(f.Pix[i01+c])*fx
				bot := float64(f.Pix[i10+c])*(1-fx) + float64(f.Pix[i11+c])*fx
				out.Pix[o+c] = uint8(top*(1-fy) + bot*fy + 0.5)
			}
		}
	}
	return out
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// DrawRect 在图上画一个矩形边框，用于把匹配结果标注出来看。
func (f *Frame) DrawRect(r Rect, c RGB, thickness int) {
	if thickness < 1 {
		thickness = 1
	}
	set := func(x, y int) {
		if !f.inBounds(x, y) {
			return
		}
		i := (y*f.W + x) * 4
		f.Pix[i], f.Pix[i+1], f.Pix[i+2], f.Pix[i+3] = c.R, c.G, c.B, 255
	}
	for t := 0; t < thickness; t++ {
		for x := r.X; x < r.X+r.W; x++ {
			set(x, r.Y+t)
			set(x, r.Y+r.H-1-t)
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			set(r.X+t, y)
			set(r.X+r.W-1-t, y)
		}
	}
}

// Clone 返回一份深拷贝，标注前先克隆可以避免弄脏原图。
func (f *Frame) Clone() *Frame {
	out := NewFrame(f.W, f.H)
	copy(out.Pix, f.Pix)
	return out
}
