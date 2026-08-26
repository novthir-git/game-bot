package vision

import "testing"

// 坐标网格的全部价值在于让人一眼读准数字，读错一位就会裁到完全不同的位置。
// 任意两个字形至少要差 3 个点——早先用 3x5 时多对数字只差 1 个点，
// 正是为了满足这条才换成 5x7。
func TestDigitGlyphsAreMutuallyDistinct(t *testing.T) {
	const minDiff = 3
	for a := 0; a < 10; a++ {
		for b := a + 1; b < 10; b++ {
			diff := 0
			for row := 0; row < glyphH; row++ {
				x := digits5x7[a][row] ^ digits5x7[b][row]
				for bit := 0; bit < glyphW; bit++ {
					if x&(1<<bit) != 0 {
						diff++
					}
				}
			}
			if diff < minDiff {
				t.Errorf("字形 %d 与 %d 只差 %d 个点（要求至少 %d），小尺寸下容易读错",
					a, b, diff, minDiff)
			}
		}
	}
}

func TestDrawNumberRendersEachDigit(t *testing.T) {
	for d := 0; d <= 9; d++ {
		f := NewFrame(16, 16)
		f.DrawNumber(1, 1, d, 1, RGB{R: 255, G: 255, B: 255})
		lit := 0
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				if r, _, _, _ := f.At(x, y); r == 255 {
					lit++
				}
			}
		}
		want := 0
		for row := 0; row < glyphH; row++ {
			for bit := 0; bit < glyphW; bit++ {
				if digits5x7[d][row]&(1<<bit) != 0 {
					want++
				}
			}
		}
		if lit != want {
			t.Errorf("数字 %d 应点亮 %d 个像素，实际 %d", d, want, lit)
		}
	}
}

func TestDrawNumberMultiDigitDoesNotOverlap(t *testing.T) {
	f := NewFrame(64, 16)
	f.DrawNumber(0, 0, 111, 1, RGB{R: 255, G: 255, B: 255})
	one := 0
	for row := 0; row < glyphH; row++ {
		for bit := 0; bit < glyphW; bit++ {
			if digits5x7[1][row]&(1<<bit) != 0 {
				one++
			}
		}
	}
	lit := 0
	for i := 0; i < len(f.Pix); i += 4 {
		if f.Pix[i] == 255 {
			lit++
		}
	}
	if lit != one*3 {
		t.Errorf("111 应点亮 %d 个像素（三位互不重叠），实际 %d", one*3, lit)
	}
}

// 纯色区域做模板会因 ZNCC 分母为零而永远匹配不上，StdDev 必须能识别出来。
func TestStdDevDetectsFlatRegion(t *testing.T) {
	flat := NewGray(20, 20)
	for i := range flat.Pix {
		flat.Pix[i] = 128
	}
	if sd := flat.StdDev(); sd != 0 {
		t.Errorf("纯色区域标准差应为 0，实际 %.4f", sd)
	}

	varied := NewGray(20, 20)
	for i := range varied.Pix {
		if i%2 == 0 {
			varied.Pix[i] = 0
		} else {
			varied.Pix[i] = 255
		}
	}
	if sd := varied.StdDev(); sd < 100 {
		t.Errorf("黑白相间区域标准差应接近 127.5，实际 %.4f", sd)
	}
}
