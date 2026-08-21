package termd

import (
	"bytes"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// =====================================================================
// 文件编码识别与转换
//
// 仿 Vim 'fileencodings'（默认 "ucs-bom,utf-8,default,latin1"）的检测链：
//   1. ucs-bom   —— 先看 BOM：UTF-8 / UTF-16 LE-BE / UTF-32 LE-BE；
//   2. utf-8     —— 无 BOM 时校验整个文件是否为合法 UTF-8；
//   3. default   —— 非 UTF-8 时尝试中文编码 GBK（用兼容的 GB18030 解码器检测，
//                   比 Vim 默认链多覆盖一种常见场景，GB18030 与 GBK 字节互解）；
//   4. latin1    —— 兜底 Latin-1（ISO-8859-1，任意字节序列均可解码，永不失手）。
//
// 与 Vim 一致：检测成功后文件内容统一转成内部 UTF-8 存储，原编码名记录在
// Buffer.Encoding；保存时（Buffer.writeTo）再按原编码转回写盘，
// 从而保证「打开→保存」不破坏原始字节（等价于 Vim 的 &fileencoding 回写）。
// =====================================================================

// 常用编码显示名（状态栏展示用，仿 Vim 的 fenc 缩写）。
const (
	EncUTF8    = "utf-8"
	EncUTF16LE = "utf-16le"
	EncUTF16BE = "utf-16be"
	EncUTF32LE = "utf-32le"
	EncUTF32BE = "utf-32be"
	EncGBK     = "gbk"
	EncLatin1  = "latin1"
)

// Encoding 描述一次文件编码检测的结果。
type Encoding struct {
	// Name 是状态栏展示的编码名（utf-8 / gbk / latin1 / utf-16le ...）。
	Name string
	// UTF8 是否为 UTF-8 编码（GBK/Latin-1 等需要转换时为 false）。
	UTF8 bool
	// BOM 原始文件是否带 BOM（保存时需原样保留，避免破坏 BOM 语义）。
	BOM bool
}

// utf8BOM / utf16BOM / utf32BOM 是各类 BOM 的字节序列。
var (
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEBOM = []byte{0xFF, 0xFE}
	utf16BEBOM = []byte{0xFE, 0xFF}
	utf32LEBOM = []byte{0xFF, 0xFE, 0x00, 0x00}
	utf32BEBOM = []byte{0x00, 0x00, 0xFE, 0xFF}
)

// sniffBOM 检查 data 开头是否带 BOM。返回 BOM 长度（0 表示无）。
// 顺序必须长 BOM 在前：utf-32le 的 BOM 以 utf-16le BOM 为前缀。
func sniffBOM(data []byte) int {
	switch {
	case bytes.HasPrefix(data, utf8BOM):
		return len(utf8BOM)
	case bytes.HasPrefix(data, utf32LEBOM):
		return len(utf32LEBOM)
	case bytes.HasPrefix(data, utf32BEBOM):
		return len(utf32BEBOM)
	case bytes.HasPrefix(data, utf16LEBOM):
		return len(utf16LEBOM)
	case bytes.HasPrefix(data, utf16BEBOM):
		return len(utf16BEBOM)
	}
	return 0
}

// DetectEncoding 仿 Vim 'fileencodings' 检测 data 的编码。
// 返回检测结果与去除 BOM 后的原始字节（不改变编码，转换由 DecodeToUTF8 负责）。
func DetectEncoding(data []byte) (Encoding, []byte) {
	if n := sniffBOM(data); n > 0 {
		body := data[n:]
		switch {
		case bytes.Equal(data[:n], utf8BOM):
			return Encoding{Name: EncUTF8, UTF8: true, BOM: true}, body
		case bytes.Equal(data[:n], utf16LEBOM):
			return Encoding{Name: EncUTF16LE, UTF8: false, BOM: true}, body
		case bytes.Equal(data[:n], utf16BEBOM):
			return Encoding{Name: EncUTF16BE, UTF8: false, BOM: true}, body
		case bytes.Equal(data[:n], utf32LEBOM):
			return Encoding{Name: EncUTF32LE, UTF8: false, BOM: true}, body
		default:
			return Encoding{Name: EncUTF32BE, UTF8: false, BOM: true}, body
		}
	}
	// 无 BOM：先试 UTF-8（含 ASCII），失败再试 GBK，最后 latin1 兜底。
	if utf8.Valid(data) {
		return Encoding{Name: EncUTF8, UTF8: true}, data
	}
	if validGBK(data) {
		return Encoding{Name: EncGBK, UTF8: false}, data
	}
	return Encoding{Name: EncLatin1, UTF8: false}, data
}

// validGBK 判断 data 是否为 GBK 编码的中文文本。
// 注意：x/text 的 GB18030/GBK 解码器非常宽容，对截断/非法序列只输出 U+FFFD
// 替换符而不报错，因此不能只依赖「解码无错」。采用启发式：
//   - 解码结果中不得出现 U+FFFD 替换符（非法字节的直接信号）；
//   - 解码结果须包含 CJK 汉字 / 全角符号 / 日文假名（U+3000..U+9FFF 或 U+FF00..U+FFEF），
//     把 café（latin1）这类「高字节但非中文」的文本排除在 GBK 之外。
func validGBK(data []byte) bool {
	out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), data)
	if err != nil {
		return false
	}
	hasHigh, hasCJK := false, false
	for len(out) > 0 {
		r, size := DecodeRune(out)
		out = out[size:]
		if r == 0xFFFD {
			return false
		}
		if r < 0x80 {
			continue
		}
		hasHigh = true
		if (r >= 0x3000 && r <= 0x30FF) || (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0xFF00 && r <= 0xFFEF) {
			hasCJK = true
		}
	}
	return hasHigh && hasCJK
}

// DecodeToUTF8 把外部编码的 data 转换为内部 UTF-8。
// UTF-8 编码直接原样返回（零拷贝）；其余编码按各自的解码器转换。
func (e Encoding) DecodeToUTF8(data []byte) []byte {
	switch e.Name {
	case EncUTF16LE:
		return convert(unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), data)
	case EncUTF16BE:
		return convert(unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), data)
	case EncUTF32LE:
		return utf32Little.decode(data)
	case EncUTF32BE:
		return utf32Big.decode(data)
	case EncGBK:
		return convert(simplifiedchinese.GBK, data)
	case EncLatin1:
		return latin1Decode(data)
	default: // utf-8
		return data
	}
}

// EncodeFromUTF8 把内部 UTF-8 内容转回原编码（保存用），自动补回 BOM。
func (e Encoding) EncodeFromUTF8(data []byte) []byte {
	var body []byte
	switch e.Name {
	case EncUTF16LE:
		body = convertTo(unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), data)
	case EncUTF16BE:
		body = convertTo(unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), data)
	case EncUTF32LE:
		body = utf32Little.encode(data)
	case EncUTF32BE:
		body = utf32Big.encode(data)
	case EncGBK:
		body = convertTo(simplifiedchinese.GBK, data)
	case EncLatin1:
		body = latin1Encode(data)
	default: // utf-8
		body = data
	}
	if e.BOM {
		switch e.Name {
		case EncUTF8:
			body = append(append([]byte(nil), utf8BOM...), body...)
		case EncUTF16LE:
			body = append(append([]byte(nil), utf16LEBOM...), body...)
		case EncUTF16BE:
			body = append(append([]byte(nil), utf16BEBOM...), body...)
		case EncUTF32LE:
			body = append(append([]byte(nil), utf32LEBOM...), body...)
		case EncUTF32BE:
			body = append(append([]byte(nil), utf32BEBOM...), body...)
		}
	}
	return body
}

// convert 用 enc 的解码器把外部编码字节转为 UTF-8；失败时原样返回（不破坏数据）。
func convert(enc encoding.Encoding, data []byte) []byte {
	out, _, err := transform.Bytes(enc.NewDecoder(), data)
	if err != nil {
		return data
	}
	return out
}

// convertTo 用 enc 的编码器把 UTF-8 字节转为目标编码；目标编码无法表示的字符
// （如 GBK 遇生僻字）导致失败时退回 UTF-8 字节，保证保存不丢内容。
func convertTo(enc encoding.Encoding, data []byte) []byte {
	out, _, err := transform.Bytes(enc.NewEncoder(), data)
	if err != nil {
		return data
	}
	return out
}

// latin1Decode 把 Latin-1 单字节序列直通为 UTF-8（每个字节映射到 U+00xx）。
func latin1Decode(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data))
	var tmp [3]byte
	for _, c := range data {
		n := encodeRune(rune(c), tmp[:])
		out.Write(tmp[:n])
	}
	return out.Bytes()
}

// latin1Encode 把 UTF-8 内容折叠回 Latin-1 单字节（仅对 U+0000..U+00FF 有效，
// 越界字符无法表示时以 '?' 占位，符合 Latin-1 语义）。
func latin1Encode(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := DecodeRune(data)
		data = data[size:]
		if r < 0x100 {
			out = append(out, byte(r))
		} else {
			out = append(out, '?')
		}
	}
	return out
}

// utf32Endian 标记 UTF-32 大小端（x/text 未提供 UTF-32，自行实现，保证往返一致）。
type utf32Endian int

const (
	utf32Little utf32Endian = iota
	utf32Big
)

// decode 将 UTF-32 字节序列解码为 UTF-8，跳过非法码点（代理区/超界）。
func (e utf32Endian) decode(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data) / 4 * 3)
	for i := 0; i+4 <= len(data); i += 4 {
		var u uint32
		if e == utf32Little {
			u = uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		} else {
			u = uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3])
		}
		r := rune(u)
		if r == 0xFFFE || r == 0xFFFF || r > utf8.MaxRune || (r >= 0xD800 && r <= 0xDFFF) {
			continue
		}
		out.WriteRune(r)
	}
	return out.Bytes()
}

// encode 将 UTF-8 字节序列编码为 UTF-32（与 decode 互为逆运算）。
func (e utf32Endian) encode(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data) / 3 * 4)
	for len(data) > 0 {
		r, size := DecodeRune(data)
		data = data[size:]
		var b [4]byte
		if e == utf32Little {
			b[0] = byte(r)
			b[1] = byte(r >> 8)
			b[2] = byte(r >> 16)
			b[3] = byte(r >> 24)
		} else {
			b[0] = byte(r >> 24)
			b[1] = byte(r >> 16)
			b[2] = byte(r >> 8)
			b[3] = byte(r)
		}
		out.Write(b[:])
	}
	return out.Bytes()
}

// stripBOM 去除 data 开头可能存在的 BOM（原始用途：未做编码检测时的预处理）。
func stripBOM(data []byte) []byte {
	if n := sniffBOM(data); n > 0 {
		return data[n:]
	}
	return data
}
