package core

import (
	"fmt"
	"strings"
	"termd"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// renderLineNum 生成左侧行号前缀。
// termd.LNNone: 空；termd.LNAbs: 绝对行号（i+1）；termd.LNRel: 当前光标行显示绝对行号，其余显示距光标行距离。
func renderLineNum(ln termd.LineNumMode, row, cursorRow int) string {
	var n int
	switch ln {
	case termd.LNAbs:
		n = row + 1
	case termd.LNRel:
		if row == cursorRow {
			n = cursorRow + 1 // 当前行显示绝对行号
		} else {
			n = abs(row - cursorRow)
		}
	default:
		return ""
	}
	// 焦黄色显示行号：普通行 214（琥珀金），当前光标行 228（更亮的焦黄）以突出焦点
	fg := lipgloss.Color("214")
	if row == cursorRow {
		fg = lipgloss.Color("228")
	}
	return lipgloss.NewStyle().Foreground(fg).Render(fmt.Sprintf("%4d ", n))
}

// abs 返回整数的绝对值（Go 1.21+ 有内置 abs，但保留以兼容旧版本）。
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// codeBG 是代码块使用的灰色背景 ANSI（48;5;236，柔和暗灰），比终端背景略亮但
// 不刺眼，接近 glow 的代码块背景风格。
const codeBG = "\x1b[48;5;236m"

// codeBorder 是代码块左侧的边框条：亮青蓝竖线 + 透明背景。
// 注意用 ASCII '|' 而非 '│'（U+2502 为 Unicode East Asian Ambiguous，在中文
// locale + kitty 下渲染为 2 列会撑破行宽导致整屏换行滚动）。
const codeBorder = "\x1b[38;5;67m|\x1b[0m"

// codeLidFg 是代码块上/下盖的水平边框色（比内容灰底略亮，勾勒矩形轮廓）。
const codeLidFg = "\x1b[38;5;239m"

// highlightCode 用 chroma 直接高亮代码（不走 glamour）：返回逐行、带 256/truecolor
// 前景色但**无背景**的彩色字符串。glamour 的 dark 风格代码块用"灰色前景空格铺满整行"
// 来伪装灰底，无法干净地裁成自适应宽度矩形；故代码块改由 chroma 上色 + renderCodeRect
// 自行绘制真正的 48;5;236 灰底矩形，宽度由原始代码内容决定。
func highlightCode(lang, code string) []string {
	lexer := lexers.Get(strings.TrimSpace(lang))
	if lexer == nil {
		// 无语言标注（```）或语言无法识别（```unknown）：不染色，原样返回文本。
		// 之前落到 chroma Fallback 会把内容统一染成白色前景（浅色终端下几乎不可见），
		// 且不属于任何语法高亮——无语言代码块应保持终端默认前景色（默认不高亮）。
		return strings.Split(code, "\n")
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("dracula")
	if style == nil {
		style = styles.Fallback
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		// 高亮失败则退回原始文本（分行、无颜色）
		return strings.Split(code, "\n")
	}
	var b strings.Builder
	f := formatters.TTY16m
	if err := f.Format(&b, style, it); err != nil {
		return strings.Split(code, "\n")
	}
	lines := strings.Split(b.String(), "\n")
	// 去掉因代码末尾换行产生的多余空项（保留代码内部应有的空行）。
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// contentWidth 计算一行去掉 ANSI 后的显示列宽（宽字符按 2 列）。
func contentWidth(s string) int {
	return termd.FBDisplayWidth(stripANSIForWidth(s))
}

// padCodeLine 把单行代码内容补齐到 maxW 列宽，并保持灰底：
// 把内层 "\x1b[0m"/"\x1b[m" 替换为 codeBG（防止背景被重置丢失），再按其显示列宽
// 用带灰底背景的空格补到 maxW，使矩形右边缘对齐成一条竖线。
func padCodeLine(line string, maxW int) string {
	line = strings.ReplaceAll(line, "\x1b[0m", codeBG)
	line = strings.ReplaceAll(line, "\x1b[m", codeBG)
	w := contentWidth(line)
	if w < maxW {
		line += codeBG + strings.Repeat(" ", maxW-w)
	}
	return line
}

// renderCodeRect 把代码块的多行渲染结果包装成一个“自适应宽度的完整矩形”：
// 矩形宽度 = 块内最宽代码行的显示列宽（代码有多宽框就多宽，不会铺满整行），
// 上/下各有一行纯灰底“盖子”勾出矩形轮廓，左侧以青蓝竖线 │ 为边框，
// 每行内容用灰底补齐到统一宽度，形成闭合矩形。
//   - rendered：glamour 高亮后的逐行输出（用于显示内容/前景色）。
//   - widthRef：原始代码内容行（用于测量真实宽度）。glamour 的 chroma 风格会把代码行
//     背景填充到整行终端宽度，若直接用 rendered 测宽会让矩形又变回铺满整行；故宽度必须
//     以原始代码内容为准（围栏 ``` 行较短，不会成为最宽行，可一并传入）。
//   - firstPrefix 仅用于首行（编辑模式下携带行号 gutter），其余行用 lidPrefix 对齐。
//
// renderCodeRect 把代码块的多行渲染结果包装成“自适应宽度的完整矩形”。
//   - rendered：glamour/chroma 高亮后的逐行输出（用于显示内容/前景色）。
//   - widthRef：原始代码内容行（用于测量真实宽度）。
//   - firstPrefix：首行前缀（含 gutter 行号着色），其余行用 lidPrefix 对齐。
//   - availWidth：代码正文可用宽度（已不含前缀与左侧边框，单位：显示列）。
//
// 关键约束：矩形（前缀+边框+正文）必须≤终端宽，否则超宽代码行被终端自动硬折行，
// 使“逻辑视觉行数”≠“真实显示行数”，预览滚动偏移与蓝底光标块随之错位（飘逸）。
// 故矩形宽度钳制到 availWidth，超宽代码行按显示列软换行（termd.WrapText 保留 chroma 着色）。
func renderCodeRect(rendered, widthRef []string, firstPrefix, lidPrefix string, availWidth int) []string {
	return codeRectCore(rendered, widthRef, firstPrefix, lidPrefix, availWidth, true)
}

// renderCommentRect 是 renderCodeRect 的"无上下盖"变体：仅保留每行灰底 +
// 左侧边框竖线 + 右边缘补齐。用于多行注释块等短内容块，避免顶/底盖
// 各占一行纯灰条造成上下空间浪费。
func renderCommentRect(rendered, widthRef []string, firstPrefix, lidPrefix string, availWidth int) []string {
	return codeRectCore(rendered, widthRef, firstPrefix, lidPrefix, availWidth, false)
}

// codeRectCore 是代码框矩形公共实现：计算自适应宽度（以 widthRef 测宽、钳制到
// availWidth），逐行铺灰底 + 左边界线 + 右边缘补齐；withLid=true 时额外绘制
// 上/下盖纯灰条（renderCodeRect），false 时省略（renderCommentRect）。
func codeRectCore(rendered, widthRef []string, firstPrefix, lidPrefix string, availWidth int, withLid bool) []string {
	if availWidth < 4 {
		availWidth = 4
	}
	maxW := 0
	for _, rl := range widthRef {
		if w := contentWidth(rl); w > maxW {
			maxW = w
		}
	}
	if maxW < 1 {
		maxW = 1
	}
	if maxW > availWidth {
		// 钳制到可用宽度：代码块矩形不会超出终端，杜绝硬折行。
		maxW = availWidth
	}
	var out []string
	// 顶盖：gutter + 边框 + 灰底 + 宽 maxW 的纯色条 + 重置
	if withLid {
		out = append(out, lidPrefix+codeBorder+codeBG+codeLidFg+strings.Repeat(" ", maxW)+"\x1b[0m")
	}
	for k, rl := range rendered {
		prefix := firstPrefix
		if k > 0 {
			prefix = lidPrefix
		}
		// 超宽行按显示列软换行（termd.WrapText 正确按显示宽处理 ANSI，保留 chroma 着色），
		// 每段都套灰底矩形，保证每行宽度≤终端宽、逻辑行数==真实显示行数。
		for si, seg := range termd.WrapText(rl, maxW) {
			p := prefix
			if si > 0 {
				p = lidPrefix
			}
			out = append(out, p+codeBorder+codeBG+padCodeLine(seg, maxW)+"\x1b[0m")
		}
	}
	// 底盖
	if withLid {
		out = append(out, lidPrefix+codeBorder+codeBG+codeLidFg+strings.Repeat(" ", maxW)+"\x1b[0m")
	}
	return out
}

// stripANSIForWidth 粗略去除 ANSI 转义以估算显示列宽（用于底部栏布局对齐）。
func stripANSIForWidth(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == 0x1b && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
