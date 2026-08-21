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
//   其他：转义字符、内嵌 HTML。
//
// 设计：
//   - 行内语法（粗体/斜体/删除线/链接/图片/行内代码/脚注引用/行内脚注/emoji/转义）
//     走轻量正则渲染（RenderInline），逐行可用、不依赖整段解析、零额外延迟。
//   - 块级语法（代码块 ```、表格、定义列表）用 glamour 整块渲染（RenderBlock），
//     保证跨行语法被正确解析（如表格对齐线、代码高亮）。
//   - 脚注定义（[^id]: 及缩进续行）不交给 glamour：实测 glamour 对单独脚注定义
//     渲染为空、行内脚注 ^[...] 完全不识别。因此脚注整块由 model_rebuild.go
//     收集后逐行经 RenderLine 渲染（定义行走 IsFootnoteDefLine 分支显示 [id] 编号，
//     续行按普通行保留缩进），正文引用与行内脚注由 RenderInline 处理。
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

// RenderBlock 用 glamour 把一段 Markdown 块（代码块/表格/定义列表/脚注等）
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
	reInlineCode   = regexp.MustCompile("`([^`]+)`")                              // `code`
	reInlineNote   = regexp.MustCompile(`\^\[([^\]]+)\]`)                         // ^[行内脚注]
	reImage        = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)               // ![alt](url)
	ReLink         = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)                // [text](url)
	reBoldItalic   = regexp.MustCompile(`\*\*\*([^*]+)\*\*\*`)                    // ***粗斜体***
	reBold         = regexp.MustCompile(`\*\*([^*]+)\*\*`)                        // **粗体**
	reItalic       = regexp.MustCompile(`\*([^*]+)\*`)                            // *斜体*
	reStrike       = regexp.MustCompile(`~~([^~]+)~~`)                            // ~~删除线~~
	reStrikeDash   = regexp.MustCompile(`--(.+?)--`)                              // --删除线--（非空内容，避免误伤 ----）
	reHighlight    = regexp.MustCompile(`==([^=]+)==`)                            // ==高亮文字==（非空内容，避免误伤 ====）
	reComment      = regexp.MustCompile(`%%([^%]+)%%`)                            // %%单行注释%%（非空内容，避免误伤 %%%%）
	reFootnoteRef  = regexp.MustCompile(`\[\^([^\]]+)\]`)                         // 正文脚注引用 [^1]
	reEmoji        = regexp.MustCompile(`:([a-z0-9_+-]+):`)                       // :smile:
	reEscape       = regexp.MustCompile(`\\([\\` + "`" + `*_{}\[\]()#+\-.!>~|])`) // 转义 \*
)

// 行内样式（与 renderer.go 的 Styles 调性一致）
// 说明：许多终端对 ANSI bold(1)/italic(3) 的字形支持不佳（无对应字体变体，
// 或中文字形根本没有斜体），单靠转义序列几乎看不出差异。因此给粗体/斜体
// 额外叠加对比色：粗体→亮黄、斜体→淡紫、粗斜体→橙红，保证即使终端不
// 支持字形也能与正文一眼区分；支持字形的终端则字形+颜色双重效果。
var (
	stBold   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	stItalic = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("141"))
	stBI     = lipgloss.NewStyle().Bold(true).Italic(true).Foreground(lipgloss.Color("209"))
	stStrike = lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("245"))
	stInline   = lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("86"))
	stLink     = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Underline(true)
	stImg      = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	stEmoji    = lipgloss.NewStyle().Foreground(lipgloss.Color("227"))
	stFnRef    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("35"))          // 正文脚注引用 ^1
	stInlineNt = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Underline(true)     // 行内脚注 ※内容
	stFnNum    = lipgloss.NewStyle().Foreground(lipgloss.Color("66"))                      // 脚注定义编号 [1]
	stHilight  = lipgloss.NewStyle().Background(lipgloss.Color("220")).Foreground(lipgloss.Color("0")) // ==高亮文字==（荧光笔：亮黄底黑字）
	stComment  = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("242"))        // %%注释%%（暗灰斜体，低调区别于正文）
)

// RenderInline 把一行内的行内 Markdown 语法渲染为带 ANSI 的字符串。
// 处理顺序：行内代码（最优先保护，占位提取）→ 行内脚注 ^[...]（占位提取）→
// 单行注释 %%x%%（占位提取）→ 图片 → 链接 → 粗斜体 → 粗体 → 斜体 → 删除线
// （~~x~~ 与 --x--）→ 高亮文字（==x==）→ 正文脚注引用 [^id] → emoji 简码 → 转义字符。
//
// 实现要点：
//   - 行内代码/行内脚注先**用占位符提取**（纯文本阶段），其内部内容不被后续
//     语法解析，且其渲染出的 ANSI 序列不会干扰后续正则（回归：链接正则曾把
//     ANSI 序列里的 '[' 误当链接起点，把样式参数逐字符包裹成乱码）。
//   - 链接/粗体/斜体等正则都作用于**纯文本**（已抽走占位内容），不会被 ANSI 干扰。
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
	// 0.5 行内脚注 ^[文本]：同样用占位符提取（\x01{index}\x01），渲染为 ※文本。
	//     提取在链接/粗体之前，避免内容中的 ANSI/括号干扰后续正则。
	var inlineNotes []string
	s = reInlineNote.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		inlineNotes = append(inlineNotes, stInlineNt.Render("※"+string(rs[2:len(rs)-1])))
		return "\x01" + strconv.Itoa(len(inlineNotes)-1) + "\x01"
	})
	// 0.7 单行/行内注释 %%x%%：占位符提取（\x02{index}\x02），内容原样渲染为注释样式
	//     （暗灰斜体），不参与后续任何行内语法解析。
	var comments []string
	s = reComment.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		comments = append(comments, stComment.Render(string(rs[2:len(rs)-2])))
		return "\x02" + strconv.Itoa(len(comments)-1) + "\x02"
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
	// 6. 删除线 ~~x~~ 与 --x--（两种写法等价；--x-- 要求非空内容，避免与 ---- 分隔线冲突）
	s = reStrike.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		return stStrike.Render(string(rs[2 : len(rs)-2]))
	})
	s = reStrikeDash.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		return stStrike.Render(string(rs[2 : len(rs)-2]))
	})
	// 6.5 高亮文字 ==x== → 荧光笔效果（亮黄底黑字）。
	//     要求内容非空（[^=]+），避免 ==== 等连续等号被误判。
	s = reHighlight.ReplaceAllStringFunc(s, func(m string) string {
		rs := []rune(m)
		if len(rs) < 4 {
			return m
		}
		return stHilight.Render(string(rs[2 : len(rs)-2]))
	})
	// 7. 正文脚注引用 [^id] → 渲染为上标式 "id"（绿色粗体，带 ^ 前缀）。
	//    注意脚注定义行 [^id]: 不会经过本函数（见 renderStyledLine 的
	//    IsFootnoteDefLine 分支），因此这里匹配到的都是正文中的引用。
	s = reFootnoteRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := reFootnoteRef.FindStringSubmatch(m)
		return stFnRef.Render("^" + sub[1])
	})
	// 8. emoji 简码 :smile:
	s = reEmoji.ReplaceAllStringFunc(s, func(m string) string {
		return stEmoji.Render(m)
	})
	// 9. 转义字符：移除反斜杠，保留其后被转义的特殊字符
	s = reEscape.ReplaceAllString(s, "$1")
	// 10. 还原占位符（行内代码 → 行内脚注 → 单行注释，均为渲染后的 ANSI 样式）
	for i, sp := range codeSpans {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", sp, 1)
	}
	for i, sp := range inlineNotes {
		s = strings.Replace(s, "\x01"+strconv.Itoa(i)+"\x01", sp, 1)
	}
	for i, sp := range comments {
		s = strings.Replace(s, "\x02"+strconv.Itoa(i)+"\x02", sp, 1)
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

// IsFootnoteDefLine 判断是否为脚注定义行（[^id]: 开头）。
func IsFootnoteDefLine(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "[^") {
		return false
	}
	idx := strings.Index(t, "]:")
	return idx > 2
}

// IsCommentStart 判断是否为多行注释块起始行：以 %% 开头且行内未闭合
// （行内已闭合的 %%x%% 视为单行注释，由 RenderInline 处理，不进入多行块）。
func IsCommentStart(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "%%") {
		return false
	}
	if strings.Contains(t[2:], "%%") {
		return false
	}
	return true
}

// IsCommentEnd 判断是否为多行注释块结束行（单独的 %%）。
func IsCommentEnd(s string) bool {
	return strings.TrimSpace(s) == "%%"
}

// IsWholeLineComment 判断整行是否为闭合的单行注释（TrimSpace 后形如 %%xxx%%，
// 整行除首尾 %% 外均为注释内容、内部不含第二个 %%）：
// 用于编辑模式光标行的"编辑时显示纯文本、离开后再渲染"判定。
func IsWholeLineComment(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "%%") || !strings.HasSuffix(t, "%%") {
		return false
	}
	inner := t[2 : len(t)-2]
	if inner == "" || strings.Contains(inner, "%%") {
		return false
	}
	return true
}

// CommentStyle 返回注释样式（暗灰斜体），供 core 包渲染多行注释块使用。
func CommentStyle() lipgloss.Style {
	return stComment
}

// StripCommentMarkers 剥离多行注释块的 %% 标记符号（标记不参与显示）：
//   - 起始行（isStart=true）：去掉行首 %%（前导缩进保留），行内剩余内容继续显示；
//   - 结束行（isEnd=true）：整行为 %%，剥掉后内容为空；
//   - 中间行：原样返回，colShift 为 0。
//
// 返回剥离后的内容与剥掉的标记列数（用于光标列对齐）。
func StripCommentMarkers(line string, isStart, isEnd bool) (string, int) {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]
	if isStart && strings.HasPrefix(trimmed, "%%") {
		return indent + trimmed[2:], 2
	}
	if isEnd {
		return indent, 2
	}
	return line, 0
}

// ByteLinesToStrings 把 [][]byte 转为 []string。
func ByteLinesToStrings(lines [][]byte) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}
