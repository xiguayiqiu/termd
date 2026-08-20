package main

import "unicode/utf8"

// 以下是对标准库 unicode/utf8 的薄封装，方便在 buffer 中处理 rune 级编解码，
// 避免直接散落 magic 细节，提高可读性。

// encodeRune 将单个 rune 编码为 UTF-8 写入 dst，返回写入字节数。
func encodeRune(r rune, dst []byte) int {
	return utf8.EncodeRune(dst, r)
}

// decodeRune 从 data[0] 解码一个 rune，返回 rune 与字节宽度。
func decodeRune(data []byte) (rune, int) {
	return utf8.DecodeRune(data)
}

// utf8RuneStart 判断某字节是否为一个 UTF-8 字符的起始字节。
func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
