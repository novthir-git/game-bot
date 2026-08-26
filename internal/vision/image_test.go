package vision

import "testing"

// Clip 的契约是「返回值一定落在画布内」，调用方会直接拿 X、Y 去算切片偏移。
// 曾经它只修正负坐标，超出右/下边界的原点被原样留下（宽高压成 0），
// Crop 随后按越界原点计算偏移就会 panic。
func TestClipKeepsOriginInsideCanvas(t *testing.T) {
	cases := []struct{ in, want Rect }{
		{Rect{X: 150, Y: 99, W: 10, H: 1}, Rect{X: 100, Y: 99, W: 0, H: 1}},
		{Rect{X: -20, Y: -30, W: 50, H: 60}, Rect{X: 0, Y: 0, W: 30, H: 30}},
		{Rect{X: 90, Y: 90, W: 40, H: 40}, Rect{X: 90, Y: 90, W: 10, H: 10}},
		{Rect{X: 500, Y: 500, W: 10, H: 10}, Rect{X: 100, Y: 100, W: 0, H: 0}},
		{Rect{X: 0, Y: 0, W: 100, H: 100}, Rect{X: 0, Y: 0, W: 100, H: 100}},
	}
	for _, c := range cases {
		got := c.in.Clip(100, 100)
		if got != c.want {
			t.Errorf("%+v.Clip(100,100) = %+v，期望 %+v", c.in, got, c.want)
		}
		if got.X < 0 || got.Y < 0 || got.X > 100 || got.Y > 100 {
			t.Errorf("%+v.Clip 返回的原点在画布外: %+v", c.in, got)
		}
		if got.W < 0 || got.H < 0 || got.X+got.W > 100 || got.Y+got.H > 100 {
			t.Errorf("%+v.Clip 返回的范围超出画布: %+v", c.in, got)
		}
	}
}

// 越界矩形传给 Crop 必须安全返回空图，而不是 panic。
// 典型触发场景：把按 1920x1080 写死的 ROI 用在 1280x720 的截图上。
func TestCropOutOfBoundsDoesNotPanic(t *testing.T) {
	rects := []Rect{
		{X: 150, Y: 99, W: 10, H: 1},
		{X: 500, Y: 500, W: 20, H: 20},
		{X: -50, Y: -50, W: 10, H: 10},
		{X: 1900, Y: 1000, W: 40, H: 40},
		{X: 0, Y: 0, W: 0, H: 0},
	}
	for _, r := range rects {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("Frame.Crop(%+v) panic: %v", r, p)
				}
			}()
			if got := NewFrame(100, 100).Crop(r); got.W*got.H != len(got.Pix)/4 {
				t.Errorf("Crop(%+v) 返回的图尺寸与像素数不符", r)
			}
		}()
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("Gray.Crop(%+v) panic: %v", r, p)
				}
			}()
			if got := NewGray(100, 100).Crop(r); got.W*got.H != len(got.Pix) {
				t.Errorf("Gray.Crop(%+v) 返回的图尺寸与像素数不符", r)
			}
		}()
	}
}

func TestCropInBoundsStillWorks(t *testing.T) {
	f := NewFrame(10, 10)
	for i := range f.Pix {
		f.Pix[i] = uint8(i % 251)
	}
	got := f.Crop(Rect{X: 2, Y: 3, W: 4, H: 5})
	if got.W != 4 || got.H != 5 {
		t.Fatalf("裁剪结果尺寸 = %dx%d，期望 4x5", got.W, got.H)
	}
	wr, wg, wb, wa := f.At(2, 3)
	gr, gg, gb, ga := got.At(0, 0)
	if wr != gr || wg != gg || wb != gb || wa != ga {
		t.Error("裁剪后左上角像素与原图对应位置不一致")
	}
}
