package termd

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/lipgloss"
)

// ============================================================
// markdown_render.go —— 富文本渲染增强模块
// ============================================================
//
// 目标：在 Preview 模式支持完整的 Markdown 语法渲染：
//   基本语法：标题(1~6)、段落/换行、粗体/斜体/粗斜体、有序/无序/嵌套列表、
//            引用(含嵌套)、行内代码与代码块(语言高亮)、链接、图片、分隔线。
//   扩展语法：表格(对齐)、删除线、任务列表、脚注、标题自定义ID、定义列表、
//            emoji 简码(:smile:)。
//   其他：转义字符、内嵌 HTML、数学公式(行内 $...$ 与块级 $$...$$)。
//
// 设计：
//   - 行内语法（粗体/斜体/删除线/链接/图片/行内代码/emoji/数学/转义）走
//     轻量正则渲染（RenderInline），逐行可用、不依赖整段解析、零额外延迟。
//   - 块级语法（代码块 ```、表格、定义列表、脚注块、数学块）用 glamour 整块
//     渲染（RenderBlock），保证跨行语法被正确解析（如表格对齐线、代码高亮）。
//
// glamour 默认已支持：标题、段落、列表、引用、代码块(chroma 高亮+语言标注)、
// 链接、图片(替代文本)、分隔线、表格(对齐)、删除线、任务列表、脚注、嵌套
// 结构；配合 WithEmoji() 支持 emoji 简码；GFM 扩展覆盖定义列表。

// sharedGlamour 构造一个支持 GFM + Emoji 的 glamour 渲染器。
// glamour 默认已启用 GFM 扩展（表格、删除线、任务列表、脚注、定义列表等），
// 配合 WithEmoji() 支持 emoji 简码(:smile:)。
func sharedGlamour(width int) *glamour.TermRenderer {
	if width < 20 {
		width = 20
	}
	gr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(), // emoji 简码 :smile:
	)
	if err != nil {
		// 退化为纯 dark 风格（仍覆盖绝大多数语法）
		gr, _ = glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(width),
		)
	}
	return gr
}

// RenderBlock 用 glamour 把一段 Markdown 块（代码块/表格/定义列表/脚注/数学块等）
// 渲染成带 ANSI 的富文本字符串。返回可能包含多行。
func (r *Renderer) RenderBlock(blockText string, width int) string {
	gr := sharedGlamour(width)
	out, err := gr.Render(blockText)
	if err != nil {
		return blockText
	}
	// glamour 段尾常带一个空行，裁剪掉便于按行拼接
	out = strings.TrimRight(out, "\n")
	return out
}

// ---- 行内语法渲染（轻量正则） ----

var (
	reInlineCode = regexp.MustCompile("`([^`]+)`")                              // `code`
	reImage      = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)               // ![alt](url)
	ReLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)                // [text](url)
	reBoldItalic = regexp.MustCompile(`\*\*\*([^*]+)\*\*\*`)                    // ***粗斜体***
	reBold       = regexp.MustCompile(`\*\*([^*]+)\*\*`)                        // **粗体**
	reItalic     = regexp.MustCompile(`\*([^*]+)\*`)                            // *斜体*
	reStrike     = regexp.MustCompile(`~~([^~]+)~~`)                            // ~~删除线~~
	reEmoji      = regexp.MustCompile(`:([a-z0-9_+-]+):`)                       // :smile:
	reMathInline = regexp.MustCompile(`\$([^$\n]+?)\$`)                         // $行内公式$
	reEscape     = regexp.MustCompile(`\\([\\` + "`" + `*_{}\[\]()#+\-.!>~|])`) // 转义 \*
)

// 行内样式（与 renderer.go 的 Styles 调性一致）
var (
	stBold   = lipgloss.NewStyle().Bold(true)
	stItalic = lipgloss.NewStyle().Italic(true)
	stBI     = lipgloss.NewStyle().Bold(true).Italic(true)
	stStrike = lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("245"))
	stInline = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("86"))
	stLink   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Underline(true)
	stImg    = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	stEmoji  = lipgloss.NewStyle().Foreground(lipgloss.Color("227"))
	stMath   = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Italic(true)
)

// RenderInline 把一行内的行内 Markdown 语法渲染为带 ANSI 的字符串。
// 处理顺序：行内代码（最优先保护，占位提取）→ 图片 → 链接 → 粗斜体 → 粗体 →
// 斜体 → 删除线 → emoji 简码 → 数学公式 → 转义字符。
//
// 实现要点：
//   - 行内代码先**用占位符提取**（纯文本阶段），其内部内容不被后续语法解析，
//     且行内代码渲染出的 ANSI 序列不会干扰后续正则（回归：链接正则曾把 ANSI
//     序列里的 '[' 误当链接起点，把样式参数逐字符包裹成乱码）。
//   - 链接/粗体/斜体等正则都作用于**纯文本**（行内代码已抽走），不会被 ANSI 干扰。
//   - 为避免按字节切片 (m[1:len(m)-1]) 在多字节字符（中文/emoji）边界处切出
//     非法 UTF-8，全部转换为 []rune 后用 rune 索引操作。
func RenderInline(s string) string {
	// 0. 行内代码：先提取为占位符（\x00{index}\x00），最后再还原渲染结果。
	var codeSpans []string
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 2 {
			return m
		}
		codeSpans = append(codeSpans, stInline.Render(string(rs[1:len(rs)-1])))
		return "\x00" + strconv.Itoa(len(codeSpans)-1) + "\x00"
	})
	// 1. 图片 ![alt](url) → 渲染为 "🖼 alt (url)" 占位符（图形渲染见 model.go 图片分支）
	s = reImage.ReplaceAllStringFunc(s, func(m string) string {
		sub := reImage.FindStringSubmatch(m)
		return stImg.Render("🖼 " + sub[1] + " (" + sub[2] + ")")
	})
	// 2. 链接 [text](url) → 只渲染链接文本（下划线 + 高亮色），不显示 url；
	//    实际链接目标由点击处理（Preview 模式左键点击链接跳转，见 mouse.go / model.go）。
	//    渲染顺序在粗体/斜体之前，链接文本内部的格式不会被二次解析。
	s = ReLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := ReLink.FindStringSubmatch(m)
		return stLink.Render(sub[1])
	})
	// 3. 粗斜体 ***x***
	s = reBoldItalic.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 6 {
			return m
		}
		return stBI.Render(string(rs[3 : len(rs)-3]))
	})
	// 4. 粗体 **x**
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		return stBold.Render(string(rs[2 : len(rs)-2]))
	})
	// 5. 斜体 *x*
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 2 {
			return m
		}
		return stItalic.Render(string(rs[1 : len(rs)-1]))
	})
	// 6. 删除线 ~~x~~
	s = reStrike.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		return stStrike.Render(string(rs[2 : len(rs)-2]))
	})
	// 7. emoji 简码 :smile:
	s = reEmoji.ReplaceAllStringFunc(s, func(m string) string {
		return stEmoji.Render(m)
	})
	// 8. 行内数学公式 $...$
	s = reMathInline.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 2 {
			return m
		}
		return stMath.Render(string(rs[1 : len(rs)-1]))
	})
	// 9. 转义字符：移除反斜杠，保留其后被转义的特殊字符
	s = reEscape.ReplaceAllString(s, "$1")
	// 10. 还原行内代码占位符（渲染后的 ANSI 样式）
	for i, sp := range codeSpans {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", sp, 1)
	}
	return s
}

// ---- block 识别辅助 ----

// IsFenceStart 判断是否为代码块/围栏块起始行（``` 或 ~~~，可带语言标注）。
func IsFenceStart(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
		// 仅当后续为可选语言名或空（排除 `内联` 这类）
		rest := t[3:]
		if len(rest) == 0 {
			return true
		}
		// 允许 ```go / ~~~js 等语言标注；出现空格或字母数字即视为围栏
		return !strings.ContainsAny(rest, "`")
	}
	return false
}

// IsFenceEnd 判断是否为围栏块结束行（``` 或 ~~~）。
func IsFenceEnd(s string) bool {
	t := strings.TrimSpace(s)
	return t == "```" || t == "~~~" || strings.HasPrefix(t, "```") && strings.TrimSpace(t) == "```" || strings.HasPrefix(t, "~~~") && strings.TrimSpace(t) == "~~~"
}

func isTableDivider(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.Contains(t, "|") || !strings.Contains(t, "-") {
		return false
	}
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '|', '-', ':', ' ':
			return -1
		}
		return r
	}, t)
	return cleaned == ""
}

// IsTableStart 判断是否为表格起始行（含 | 的行，且下一行是分隔行 |---|---|）。
func IsTableStart(s, next string) bool {
	t := strings.TrimSpace(s)
	if !strings.Contains(t, "|") {
		return false
	}
	return isTableDivider(next)
}

// IsMathBlockDelim 判断是否为数学块分隔行（独占一行的 $$）。
func IsMathBlockDelim(s string) bool {
	return strings.TrimSpace(s) == "$$"
}

// IsMathBlock 判断一行是否“开启”一个数学块：独占一行的 $$ 或单行显示公式 $$...$$。
// 注意：仅以 $$ 开头但不成对的行（如行内 $$x$$ 误用）不会被当成块首，避免误吞正文。
func IsMathBlock(s string) bool {
	t := strings.TrimSpace(s)
	if t == "$$" {
		return true
	}
	// 单行显示公式：以 $$ 开头且以 $$ 结尾，且中间至少含一个非 $ 字符
	return strings.HasPrefix(t, "$$") && strings.HasSuffix(t, "$$") && len([]rune(t)) > 4
}

// IsSingleLineMath 判断是否为单行显示公式 $$...$$（无需另寻结束分隔行）。
func IsSingleLineMath(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "$$") && strings.HasSuffix(t, "$$") && len([]rune(t)) > 4
}

// IsDefListLine 判断是否为定义列表的“定义”行（以 ": " 开头）。
func IsDefListLine(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), ": ")
}

// IsDefTermLine 判断一行能否作为定义列表的“术语”行（前导行）。
// 排除其它块级语法（标题/引用/列表/代码/表格/分隔线等），只允许普通段落文本。
func IsDefTermLine(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "#"), strings.HasPrefix(t, ">"),
		strings.HasPrefix(t, "-"), strings.HasPrefix(t, "*"),
		strings.HasPrefix(t, "+"), strings.HasPrefix(t, "`"),
		strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"),
		strings.HasPrefix(t, "|"), strings.HasPrefix(t, "$$"),
		strings.HasPrefix(t, "!"):
		return false
	}
	if isHR(t) {
		return false
	}
	return true
}

// isFootnoteDef 判断是否为脚注定义（[^id]:）。
func isFootnoteDef(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "[^") {
		return false
	}
	idx := strings.Index(t, "]:")
	return idx > 2
}

// ByteLinesToStrings 把 [][]byte 转为 []string。
func ByteLinesToStrings(lines [][]byte) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}
