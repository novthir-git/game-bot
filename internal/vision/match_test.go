package vision

import (
	"math"
	"testing"
)

// lcg 是一个确定性的伪随机源，保证测试可复现。
type lcg struct{ s uint32 }

func (l *lcg) next() uint8 {
	l.s = l.s*1664525 + 1013904223
	return uint8(l.s >> 24)
}

// buildScene 造一张带纹理的底图，并在 (px,py) 处嵌入一块可辨识的图案。
// bias 会整体抬高嵌入块的亮度，用来验证 ZNCC 对亮度平移不敏感。
func buildScene(w, h, px, py, pw, ph int, bias int) (*Gray, *Gray) {
	r := &lcg{s: 42}
	hay := NewGray(w, h)
	for i := range hay.Pix {
		hay.Pix[i] = r.next()/4 + 60 // 低对比度底噪，模拟水彩背景
	}
	ndl := NewGray(pw, ph)
	pr := &lcg{s: 7}
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			v := int(pr.next())
			ndl.Pix[y*pw+x] = uint8(v)
			hv := v + bias
			if hv > 255 {
				hv = 255
			}
			if hv < 0 {
				hv = 0
			}
			hay.Pix[(py+y)*w+px+x] = uint8(hv)
		}
	}
	return hay, ndl
}

func TestFindExact(t *testing.T) {
	hay, ndl := buildScene(400, 300, 137, 88, 40, 40, 0)
	m, ok := Find(hay, ndl, Options{Threshold: 0.8})
	if !ok {
		t.Fatalf("未命中，得分 %.4f", m.Score)
	}
	if m.Rect.X != 137 || m.Rect.Y != 88 {
		t.Errorf("位置错误：得到 (%d,%d)，期望 (137,88)", m.Rect.X, m.Rect.Y)
	}
	if m.Score < 0.99 {
		t.Errorf("完全一致时得分应接近 1.0，实际 %.4f", m.Score)
	}
}

// 同一个按钮在不同光效下绝对亮度会变，ZNCC 必须扛得住。
func TestFindBrightnessShifted(t *testing.T) {
	hay, ndl := buildScene(400, 300, 200, 150, 48, 48, 40)
	m, ok := Find(hay, ndl, Options{Threshold: 0.8})
	if !ok {
		t.Fatalf("亮度整体 +40 后应仍能命中，实际得分 %.4f", m.Score)
	}
	if m.Rect.X != 200 || m.Rect.Y != 150 {
		t.Errorf("位置错误：得到 (%d,%d)，期望 (200,150)", m.Rect.X, m.Rect.Y)
	}
}

func TestFindAbsent(t *testing.T) {
	hay, _ := buildScene(400, 300, 137, 88, 40, 40, 0)
	other := NewGray(40, 40)
	r := &lcg{s: 999}
	for i := range other.Pix {
		other.Pix[i] = r.next()
	}
	if _, ok := Find(hay, other, Options{Threshold: 0.8}); ok {
		t.Error("不存在的模板不应命中")
	}
}

func TestFindWithROI(t *testing.T) {
	hay, ndl := buildScene(400, 300, 137, 88, 40, 40, 0)
	// ROI 罩住目标
	roi := Rect{X: 120, Y: 70, W: 100, H: 100}
	if m, ok := Find(hay, ndl, Options{Threshold: 0.8, ROI: &roi}); !ok || m.Rect.X != 137 {
		t.Errorf("ROI 内应命中，得到 ok=%v (%d,%d)", ok, m.Rect.X, m.Rect.Y)
	}
	// ROI 避开目标
	off := Rect{X: 0, Y: 0, W: 100, H: 60}
	if _, ok := Find(hay, ndl, Options{Threshold: 0.8, ROI: &off}); ok {
		t.Error("ROI 外的目标不应被找到")
	}
}

func TestFindAllDedup(t *testing.T) {
	hay, ndl := buildScene(500, 400, 50, 50, 36, 36, 0)
	// 再贴两份到别处
	for _, p := range [][2]int{{200, 120}, {350, 260}} {
		for y := 0; y < ndl.H; y++ {
			copy(hay.Pix[(p[1]+y)*hay.W+p[0]:(p[1]+y)*hay.W+p[0]+ndl.W], ndl.Pix[y*ndl.W:(y+1)*ndl.W])
		}
	}
	got := FindAll(hay, ndl, Options{Threshold: 0.9}, 0)
	if len(got) != 3 {
		t.Fatalf("应找到 3 处，实际 %d 处：%+v", len(got), got)
	}
}

// 纯色模板属于选图失误，必须匹配不上，而不是给出虚高的分数。
func TestUniformTemplateRejected(t *testing.T) {
	hay, _ := buildScene(300, 200, 100, 80, 30, 30, 0)
	flat := NewGray(30, 30)
	for i := range flat.Pix {
		flat.Pix[i] = 128
	}
	if m, ok := Find(hay, flat, Options{Threshold: 0.5}); ok {
		t.Errorf("纯色模板不应命中，得分 %.4f", m.Score)
	}
}

func BenchmarkFind720p(b *testing.B) {
	hay, ndl := buildScene(1280, 720, 900, 500, 60, 60, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Find(hay, ndl, Options{Threshold: 0.8})
	}
}

// buildUI 造一张更接近真实游戏画面的图：渐变底 + 一个带边框和内部图案的「按钮」。
// 纯噪声是最坏情况，这个则是实际会遇到的情况。
func buildUI(w, h, px, py int) (*Gray, *Gray) {
	hay := NewGray(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 柔和的对角渐变，模拟水彩背景
			hay.Pix[y*w+x] = uint8(90 + (x*40)/w + (y*30)/h)
		}
	}
	bw, bh := 56, 32
	ndl := NewGray(bw, bh)
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			v := uint8(200)
			if x < 2 || y < 2 || x >= bw-2 || y >= bh-2 {
				v = 60 // 边框
			} else if x >= 12 && x < 44 && y >= 12 && y < 20 {
				v = 30 // 内部图案（模拟按钮上的文字块）
			}
			ndl.Pix[y*bw+x] = v
			hay.Pix[(py+y)*w+px+x] = v
		}
	}
	return hay, ndl
}

func TestFindUIButton(t *testing.T) {
	hay, ndl := buildUI(1280, 720, 813, 447)
	m, ok := Find(hay, ndl, Options{Threshold: 0.78})
	if !ok {
		t.Fatalf("未命中按钮，得分 %.4f", m.Score)
	}
	if m.Rect.X != 813 || m.Rect.Y != 447 {
		t.Errorf("位置错误：得到 (%d,%d)，期望 (813,447)", m.Rect.X, m.Rect.Y)
	}
}

// 阈值语义必须与 step 无关：抽样只用于排序候选，最终分数一律全密度复算。
func TestScoreIndependentOfStep(t *testing.T) {
	hay, ndl := buildUI(640, 480, 301, 202)
	var got []float64
	for _, step := range []int{1, 2, 3} {
		m, ok := Find(hay, ndl, Options{Threshold: 0.78, Step: step})
		if !ok {
			t.Fatalf("step=%d 未命中", step)
		}
		got = append(got, m.Score)
	}
	for i := 1; i < len(got); i++ {
		if math.Abs(got[i]-got[0]) > 1e-9 {
			t.Errorf("不同 step 的最终得分应一致：%v", got)
		}
	}
}

func BenchmarkFindROI(b *testing.B) {
	hay, ndl := buildUI(1280, 720, 813, 447)
	roi := Rect{X: 700, Y: 380, W: 320, H: 200}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Find(hay, ndl, Options{Threshold: 0.78, ROI: &roi})
	}
}

// FindAll 的 maxN 必须是「按最终分数排序后取前 N」，而不是
// 「按抽样分数顺序凑够 N 个」。抽样分数只用于排候选，与全密度分数
// 的排序并不一致，先截断再排序会把真正的最高分整个丢掉，
// 而末尾的排序又让调用方误以为 out[0] 就是全图最佳命中。
func TestFindAllMaxNReturnsTrueTopN(t *testing.T) {
	hay, ndl := buildUI(1280, 720, 100, 80)
	// 再贴若干份，并对其中一些做轻微扰动，制造分数各异的候选
	spots := [][2]int{{400, 80}, {700, 80}, {1000, 80}, {100, 300}, {400, 300}, {700, 300}}
	for i, p := range spots {
		for y := 0; y < ndl.H; y++ {
			for x := 0; x < ndl.W; x++ {
				v := int(ndl.Pix[y*ndl.W+x])
				// 扰动幅度随位置递增，让每一处的最终分数都不同
				if (x+y)%3 == 0 {
					v += (i + 1) * 7
				}
				if v > 255 {
					v = 255
				}
				hay.Pix[(p[1]+y)*hay.W+p[0]+x] = uint8(v)
			}
		}
	}

	full := FindAll(hay, ndl, Options{Threshold: 0}, 0)
	if len(full) < 4 {
		t.Fatalf("场景构造有问题，只找到 %d 处候选", len(full))
	}
	for n := 1; n <= 4; n++ {
		got := FindAll(hay, ndl, Options{Threshold: 0}, n)
		if len(got) != n {
			t.Errorf("maxN=%d 应返回 %d 条，实际 %d 条", n, n, len(got))
			continue
		}
		for i := 0; i < n; i++ {
			if got[i].Rect != full[i].Rect || got[i].Score != full[i].Score {
				t.Errorf("maxN=%d 第 %d 条 = %.4f@%v，期望 %.4f@%v（应与不限个数时的前 N 条一致）",
					n, i+1, got[i].Score, got[i].Rect, full[i].Score, full[i].Rect)
			}
		}
	}
}

// 结果必须按分数降序，调用方靠 out[0]/out[1] 算最高分与次高分的差距。
func TestFindAllResultsAreSortedDescending(t *testing.T) {
	hay, ndl := buildUI(900, 600, 100, 80)
	for _, p := range [][2]int{{400, 80}, {700, 300}} {
		for y := 0; y < ndl.H; y++ {
			copy(hay.Pix[(p[1]+y)*hay.W+p[0]:(p[1]+y)*hay.W+p[0]+ndl.W], ndl.Pix[y*ndl.W:(y+1)*ndl.W])
		}
	}
	got := FindAll(hay, ndl, Options{Threshold: 0}, 6)
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("结果未按分数降序：第 %d 条 %.4f > 第 %d 条 %.4f",
				i+1, got[i].Score, i, got[i-1].Score)
		}
	}
}
