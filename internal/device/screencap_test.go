package device

import (
	"encoding/binary"
	"testing"
)

func header(w, h, format uint32, extra bool) []byte {
	n := 12
	if extra {
		n = 16
	}
	b := make([]byte, n)
	binary.LittleEndian.PutUint32(b[0:], w)
	binary.LittleEndian.PutUint32(b[4:], h)
	binary.LittleEndian.PutUint32(b[8:], format)
	return b
}

// 12 字节头（Android 8 及更早）与 16 字节头（Android 9+）都要能解。
func TestDecodeBothHeaderLengths(t *testing.T) {
	for _, extra := range []bool{false, true} {
		px := make([]byte, 4*3*4)
		for i := range px {
			px[i] = uint8(i)
		}
		raw := append(header(4, 3, fmtRGBA8888, extra), px...)
		f, err := DecodeScreencap(raw)
		if err != nil {
			t.Fatalf("extra=%v 解析失败: %v", extra, err)
		}
		if f.W != 4 || f.H != 3 {
			t.Errorf("extra=%v 尺寸 = %dx%d", extra, f.W, f.H)
		}
		if r, g, b, a := f.At(0, 0); r != 0 || g != 1 || b != 2 || a != 3 {
			t.Errorf("extra=%v 首像素 = %d,%d,%d,%d", extra, r, g, b, a)
		}
	}
}

func TestDecodeBGRASwapsChannels(t *testing.T) {
	px := []byte{10, 20, 30, 255} // B,G,R,A
	raw := append(header(1, 1, fmtBGRA8888, true), px...)
	f, err := DecodeScreencap(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := f.At(0, 0); r != 30 || g != 20 || b != 10 {
		t.Errorf("BGRA 未正确换序，得到 %d,%d,%d", r, g, b)
	}
}

func TestDecodeRGB565FullRange(t *testing.T) {
	px := make([]byte, 2)
	binary.LittleEndian.PutUint16(px, 0xFFFF) // 全白
	raw := append(header(1, 1, fmtRGB565, true), px...)
	f, err := DecodeScreencap(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 5/6 位满值必须展开成 255，否则「白色」会变成 248 这种值，破坏比色判定
	if r, g, b, _ := f.At(0, 0); r != 255 || g != 255 || b != 255 {
		t.Errorf("RGB565 满值应展开为 255,255,255，得到 %d,%d,%d", r, g, b)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	cases := map[string][]byte{
		"过短":    {1, 2, 3},
		"尺寸离谱":  append(header(999999, 1, fmtRGBA8888, true), 0, 0, 0, 0),
		"未知格式":  append(header(2, 2, 99, true), make([]byte, 16)...),
		"长度对不上": append(header(4, 4, fmtRGBA8888, true), make([]byte, 8)...),
	}
	for name, raw := range cases {
		if _, err := DecodeScreencap(raw); err == nil {
			t.Errorf("%s 应当报错", name)
		}
	}
}
