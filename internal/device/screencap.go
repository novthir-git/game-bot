package device

import (
	"encoding/binary"
	"fmt"

	"github.com/novthir-git/game-bot/internal/vision"
)

// Android screencap 的原始输出格式：
//
//	uint32 width
//	uint32 height
//	uint32 format
//	uint32 colorSpace   // Android 9 (API 28) 起才有
//	[]byte pixels
//
// 我们不用 `screencap -p`（PNG）：PNG 编码在设备侧要花掉一两百毫秒，
// 而原始格式解析下来只是一次内存搬运。
const (
	fmtRGBA8888 = 1
	fmtRGBX8888 = 2
	fmtRGB888   = 3
	fmtRGB565   = 4
	fmtBGRA8888 = 5
)

func bytesPerPixel(format uint32) (int, error) {
	switch format {
	case fmtRGBA8888, fmtRGBX8888, fmtBGRA8888:
		return 4, nil
	case fmtRGB888:
		return 3, nil
	case fmtRGB565:
		return 2, nil
	default:
		return 0, fmt.Errorf("不认识的 screencap 像素格式: %d", format)
	}
}

// DecodeScreencap 解析 `adb exec-out screencap` 的原始输出。
func DecodeScreencap(raw []byte) (*vision.Frame, error) {
	if len(raw) < 12 {
		return nil, fmt.Errorf("screencap 输出过短（%d 字节），"+
			"通常意味着 adb 连接已断开或设备未就绪", len(raw))
	}
	w := int(binary.LittleEndian.Uint32(raw[0:4]))
	h := int(binary.LittleEndian.Uint32(raw[4:8]))
	format := binary.LittleEndian.Uint32(raw[8:12])

	if w <= 0 || h <= 0 || w > 10000 || h > 10000 {
		return nil, fmt.Errorf("screencap 头部不合理: %dx%d，输出可能已被破坏"+
			"（是否误用了 adb shell 而非 exec-out？）", w, h)
	}
	bpp, err := bytesPerPixel(format)
	if err != nil {
		return nil, err
	}

	// 头部是 12 还是 16 字节取决于安卓版本，直接按剩余长度反推，
	// 比去查设备 API level 更省事也更可靠。
	need := w * h * bpp
	var off int
	switch {
	case len(raw)-12 == need:
		off = 12
	case len(raw)-16 == need:
		off = 16
	default:
		return nil, fmt.Errorf("screencap 数据长度不符: %dx%d 格式 %d 需要 %d 字节，实得 %d",
			w, h, format, need, len(raw)-12)
	}

	f := vision.NewFrame(w, h)
	src := raw[off:]
	switch format {
	case fmtRGBA8888:
		copy(f.Pix, src)
	case fmtRGBX8888:
		copy(f.Pix, src)
		for i := 3; i < len(f.Pix); i += 4 {
			f.Pix[i] = 255 // X 通道无意义，补成不透明
		}
	case fmtBGRA8888:
		for i := 0; i+3 < len(src); i += 4 {
			f.Pix[i] = src[i+2]
			f.Pix[i+1] = src[i+1]
			f.Pix[i+2] = src[i]
			f.Pix[i+3] = src[i+3]
		}
	case fmtRGB888:
		for p := 0; p < w*h; p++ {
			f.Pix[p*4] = src[p*3]
			f.Pix[p*4+1] = src[p*3+1]
			f.Pix[p*4+2] = src[p*3+2]
			f.Pix[p*4+3] = 255
		}
	case fmtRGB565:
		for p := 0; p < w*h; p++ {
			v := binary.LittleEndian.Uint16(src[p*2:])
			// 5-6-5 位展开到 8 位，用高位补低位以保证 0->0、满值->255
			r := uint8((v>>11)&0x1F) << 3
			g := uint8((v>>5)&0x3F) << 2
			b := uint8(v&0x1F) << 3
			f.Pix[p*4] = r | r>>5
			f.Pix[p*4+1] = g | g>>6
			f.Pix[p*4+2] = b | b>>5
			f.Pix[p*4+3] = 255
		}
	}
	return f, nil
}
