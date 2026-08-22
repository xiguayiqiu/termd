package termd

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// glamourStyle 指定 Preview 模式所用的 glamour 主题。
// 与 mdcat 默认效果对齐：dark 主题（支持 GFM 表格、任务列表、代码块等）。
const glamourStyle = "dark"

// ============================================================
// Renderer —— 混合渲染策略（本项目的核心难点）
// ============================================================
//
// 设计目标：在终端里既享受 Markdown 富文本预览，又保持极低输入延迟。
//
// 核心思想：智能识别 + 双态渲染。
//   1. 对于“纯文本态”行（不含任何 Markdown 语法）——直接原样输出，
//      不做任何解析，输入零开销。
//   2. 对于“渲染态”行（含 Markdown 语法，如标题/列表/代码块/引用等）——
//      使用 goldmark 解析并用 termenv/lipgloss 渲染为富文本样式。
//
// 注意：Edit Mode 下为保证光标定位与编辑体验，整体走“纯文本态”渲染；
//       只有 Preview Mode 才会对单行/段落启用 goldmark 富文本渲染。
//       这样既能预览排版，又不会在打字时引入解析卡顿。

// mdRE 用于快速判断一行是否“看起来”包含 Markdown 语法。
// 仅做粗筛（正则命中即视为渲染态），避免逐行调用 goldmark 的高昂代价。
// 使用双引号字符串以便直接书写反引号，避免反引号字符串无法转义的麻烦。
var mdRE = regexp.MustCompile("^(#{1,6}\\s|>\\s|[-*+]\\s|[-*+]\\s\\[[ xX]\\]\\s|\\d+\\.\\s|```|~~~| {2,}`| {4,}|-{3,}|\\*{3,}|_{3,})")

// blockStartRE 识别块级语法的起始（代码块、表格分隔行等）。
var blockStartRE = regexp.MustCompile("^(```|~~~)")

// inlineMDRE 识别行内 Markdown 语法（粗体/斜体/删除线/行内代码/链接/图片/emoji）。
// 用于让普通段落行也能渲染其中的行内语法。
var inlineMDRE = regexp.MustCompile("(\\*\\*|~~|--|==|%%|\\*|`|\\[[^\\]]+\\]|!\\[|(:[a-z0-9_+-]+:))")

// Renderer 负责把 Buffer 的某一行/段落渲染成带样式的最终字符串。
type Renderer struct {
	// goldmark 实例（启用 GFM 扩展：表格、任务列表、删除线等）
	md goldmark.Markdown
	// termenv 配置文件（用于检测终端颜色能力）
	profile termenv.Profile
	// 样式集合（用 lipgloss 描述各类元素的视觉）
	Styles Styles
	// 复用的 glamour TermRenderer（Preview 模式单行渲染使用）
	gRenderer *glamour.TermRenderer
	// glamour 渲染宽度（终端宽度 - 行号前缀宽度）
	gWidth int
	// 软换行可用宽度（终端宽度 - 行号前缀宽度）。供 HR 分隔线、标题下划线等
	// 占满整行宽度使用；<=0 表示未设置，退化为固定长度。
	wrap int
}

// SetWrap 设置当前渲染的可用宽度（终端宽度 - 行号前缀宽度），
// 供分隔线、标题下划线等“占满整行”的元素使用。
func (r *Renderer) SetWrap(w int) {
	r.wrap = w
}

// Profile 返回渲染器使用的 termenv.Profile（用于图片渲染的色彩能力检测）。
func (r *Renderer) Profile() termenv.Profile {
	return r.profile
}

// Styles 集中管理所有视觉样式，避免样式散落。
type Styles struct {
	H1     lipgloss.Style
	H2     lipgloss.Style
	H3     lipgloss.Style
	H4     lipgloss.Style
	H5     lipgloss.Style
	H6     lipgloss.Style
	Quote  lipgloss.Style
	Code   lipgloss.Style
	CodeBg lipgloss.Style
	List   lipgloss.Style
	Task   lipgloss.Style
	Dim    lipgloss.Style
	Img    lipgloss.Style
}

// NewRenderer 构造渲染器，初始化 goldmark 与样式。
func NewRenderer() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,      // 表格、任务列表、删除线、自动链接等
			extension.Footnote, // 脚注支持
		),
	)

	r := &Renderer{
		md:      md,
		profile: termenv.EnvColorProfile(), // 默认按环境推断，运行时可覆盖
		gWidth:  80,
	}
	// 初始化 glamour TermRenderer（单行预览渲染复用）
	if gr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(80),
	); err == nil {
		r.gRenderer = gr
	}
	r.initStyles()
	return r
}

// SetLineWidth 更新 Preview 单行渲染时使用的可用宽度（终端宽度减行号前缀占用的列数）。
// 同时重新构造 glamour TermRenderer 以应用新宽度（glamour 宽度在构造时固定）。
func (r *Renderer) SetLineWidth(totalWidth, lineNumCol int) {
	w := totalWidth - lineNumCol
	if w < 20 {
		w = 20 // 最小可用宽度
	}
	if w == r.gWidth && r.gRenderer != nil {
		return
	}
	r.gWidth = w
	if gr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(w),
	); err == nil {
		r.gRenderer = gr
	}
}

// SetProfile 根据终端能力设置颜色配置。
func (r *Renderer) SetProfile(p termenv.Profile) {
	r.profile = p
	// 同步 lipgloss 的全局颜色配置
	lipgloss.SetColorProfile(p)
}

// initStyles 定义各类 Markdown 元素的视觉表现。
func (r *Renderer) initStyles() {
	r.Styles = Styles{
		H1:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Underline(true),
		H2:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("200")),
		H3:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")),
		H4:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75")),
		H5:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("72")),
		H6:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")),
		Quote:  lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true),
		Code:   lipgloss.NewStyle().Foreground(lipgloss.Color("86")),
		CodeBg: lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")),
		List:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		Task:   lipgloss.NewStyle().Foreground(lipgloss.Color("82")),
		Dim:    lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Img:    lipgloss.NewStyle().Foreground(lipgloss.Color("213")),
	}
}

// IsMarkdownLine 判断给定行是否包含 Markdown 语法（粗筛）。
// 注意：缩进的列表项 / 引用 / 代码块等需要先去掉前导空格再匹配，
// 否则 "  - xx" 这类嵌套列表会匹配不到 ^[-*+]\s 等块级模式。
func (r *Renderer) IsMarkdownLine(line []byte) bool {
	s := strings.TrimRight(string(line), " \t")
	if s == "" {
		return false
	}
	trimmed := strings.TrimLeft(s, " \t")
	return mdRE.MatchString(trimmed) || blockStartRE.MatchString(trimmed) || inlineMDRE.MatchString(trimmed) || IsDefListLine(trimmed)
}

// CellWidth 返回单个 rune 在终端中的显示列宽（仿 vim 的 char2cells）。
// 东亚全角/宽字符占 2 列；制表符按 8 列对齐近似处理；其余占 1 列。
// ANSI 转义序列本身零宽，由 skipANSI 单独跳过。
func CellWidth(r rune) int {
	switch {
	case r == '\t':
		return 8
	case r < 0x20 || r == 0x7f:
		return 0
	case r >= 0x1100 &&
		(r <= 0x115f || r == 0x2329 || r == 0x232a ||
			(r >= 0x2e80 && r <= 0x303e) ||
			(r >= 0x3041 && r <= 0x33ff) ||
			(r >= 0x3400 && r <= 0x4dbf) ||
			(r >= 0x4e00 && r <= 0x9fff) ||
			(r >= 0xa000 && r <= 0xa4cf) ||
			(r >= 0xac00 && r <= 0xd7a3) ||
			(r >= 0xf900 && r <= 0xfaff) ||
			(r >= 0xfe30 && r <= 0xfe4f) ||
			(r >= 0xff00 && r <= 0xff60) ||
			(r >= 0xffe0 && r <= 0xffe6) ||
			(r >= 0x20000 && r <= 0x3fffd) ||
			(r >= 0x1f300 && r <= 0x1faff)):
		return 2
	default:
		return 1
	}
}

// skipANSI 判断从 runes[i] 起是否为一段 ANSI CSI 转义序列（ESC[...X，零宽），
// 返回跳过该序列后的下一 rune 下标。若不是转义则原样返回 i。
// 支持所有标准 CSI 终止字符（@ A-Z [ \ ] ^ _ ` a-z { | } ~），而不只是 'm'，
// 避免光标控制序列（如 ESC[?25h/l）、清屏序列等被当作可见字符处理。
// 注意：与调用方统一以 rune 下标工作，避免 CJK（多字节）下 byte/rune 混用错位。
func skipANSI(runes []rune, i int) int {
	if i < 0 {
		return 0
	}
	if i+1 < len(runes) && runes[i] == 0x1b && runes[i+1] == '[' {
		j := i + 2
		for j < len(runes) {
			c := runes[j]
			// CSI 参数字节：0x30-0x3F（0-9:;<=>?）
			if c >= 0x30 && c <= 0x3F {
				j++
				continue
			}
			// CSI 中间字节：0x20-0x2F（空格 !"#$%&'()*+,-./）
			if c >= 0x20 && c <= 0x2F {
				j++
				continue
			}
			// CSI 终止字节：0x40-0x7E（@ A-Z [ \ ] ^ _ ` a-z { | } ~）
			if c >= 0x40 && c <= 0x7E {
				return j + 1
			}
			// 遇到非法字节：不是 CSI，保守回退
			break
		}
	}
	return i
}

// isResetSeq 判断一段 ANSI 转义是否为 SGR 复位（ESC[0m / ESC[m）。
func isResetSeq(seq string) bool {
	return seq == "\x1b[0m" || seq == "\x1b[m"
}

// WrapText 把一段（可能含 ANSI 转义的）文本软换行到 maxWidth 列以内，
// 返回按 "\n" 连接的多个物理行。策略：
//   - 仅在空格处断行（词软换行），实现类 vim 'wrap' 的可读折行；
//   - 单个“词”超过 maxWidth 时硬断行（避免超宽溢出终端）；
//   - 不切断任何 ANSI 转义序列；断行若落在某个样式 run 中间，会在上一行
//     末尾补复位符、并在续行开头重开该样式，保证每行 ANSI 完全自洽（无色偏/污点）；
//   - 列宽按终端单元格计算（东亚宽字符占 2 列），与 lipgloss.Width 一致。
//
// 调用方负责在续行上拼接 gutter（行号占位），WrapText 只产出“纯内容行”。
func WrapText(s string, maxWidth int) []string {
	if maxWidth < 1 {
		maxWidth = 1
	}
	runes := []rune(s)
	n := len(runes)
	var lines []string
	line := strings.Builder{}
	lineW := 0
	// open：当前“未闭合”的 SGR 开序列（最近一次非复位 SGR）。断行落在某
	// 样式 run 中间时，用它在续行开头重开，并在上一行末尾复位。
	open := ""

	// newLine 结束当前物理行（必要时补复位），并开启新行（必要时重开样式）。
	newLine := func() {
		if open != "" {
			line.WriteString("\x1b[0m")
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
		line.Reset()
		lineW = 0
		if open != "" {
			line.WriteString(open)
		}
	}

	i := 0
	for i < n {
		// 跳过 ANSI 转义：零宽，附加到当前行，并维护 open 状态
		if next := skipANSI(runes, i); next != i {
			if next < i {
				next = i + 1 // 防御：绝不回退
			}
			if next > n {
				next = n
			}
			seq := string(runes[i:next])
			line.WriteString(seq)
			if isResetSeq(seq) {
				open = ""
			} else {
				open = seq
			}
			i = next
			continue
		}
		r := runes[i]
		w := CellWidth(r)
		if r == '\n' {
			// 原文自带换行（如标题下划线）：强制断行并开启新物理行，
			// 由调用方的续行 gutter 逻辑负责对齐；不写入该换行符本身。
			newLine()
			i++
			continue
		}
		if r == ' ' {
			// 空格是词边界；若当前行已有内容且再加此空格会超宽，
			// 则在此空格处断行（空格被丢弃，不进入续行首）
			if lineW > 0 && lineW+w > maxWidth {
				newLine()
			} else if lineW+w <= maxWidth {
				line.WriteRune(r)
				lineW += w
			}
			i++
			continue
		}
		if lineW+w > maxWidth {
			// 当前“词”放不下：先断行（newLine 自动补复位并在续行重开样式），再写本 rune
			newLine()
			line.WriteRune(r)
			lineW = w
		} else {
			line.WriteRune(r)
			lineW += w
		}
		i++
	}
	// 收尾：闭合未完成的样式
	if open != "" {
		line.WriteString("\x1b[0m")
	}
	lines = append(lines, strings.TrimRight(line.String(), " "))
	return lines
}

// RenderLine 渲染单行。
//   - preview=true：Preview 模式下启用富文本渲染（混合策略：纯文本行原样，语法行解析）。
//   - preview=false：编辑态，一律纯文本输出（用于非光标行的富文本预览，保留渲染风格）。
//   - highlight：若提供搜索关键字，则高亮匹配。
func (r *Renderer) RenderLine(line []byte, preview bool, highlight string) string {
	if !preview {
		// Edit 模式下的“非光标行”：仍可走纯文本，避免光标行以外的样式带来额外渲染开销。
		// 但为了让用户看到 Markdown 语法着色，这里改为直接走富文本渲染（不走 insertCursor）。
		// 真正的光标行使用 RenderEditLineWithCursor。
		return r.renderStyledLine(string(line), highlight)
	}

	s := string(line)
	if r.IsMarkdownLine(line) {
		// 渲染态：按语法类型做轻量级单行富文本渲染（无需完整 goldmark 解析整段）
		return r.renderStyledLine(s, highlight)
	}
	// 纯文本态：直接输出，可选搜索高亮
	return r.applyHighlight(s, highlight)
}

// RenderEditLineWithCursor 渲染 Edit 模式下的当前光标所在行（WYSIWYG + 列精确对齐）。
//
// 实现策略（避免 ANSI 转义破坏光标列对齐）：
//  1. 仅对语法前缀（如 "#"、"##"、"###"、">"、"-"、"- [x]"、"- [ ]"、"1."、```` ``` ````）应用颜色。
//  2. 主体内容**保持原文**（不调用 lipgloss 渲染），这样 `body` 仍是普通字符串，
//     在其中插入反显空格光标不会破坏 rune 计数 = 显示列宽的对等关系。
//  3. 光标用一个反显空格标记，宽度为 1 字符。
//
// 视觉效果：用户能看到 Markdown 语法高亮前缀，但行内字符不被任何会改变显示宽度的样式干扰，
// 光标列与实际输入位置完全一致。
// 当 showCursor=false 时，不注入蓝底光标块（用于硬件光标模式）。
func (r *Renderer) RenderEditLineWithCursor(line []byte, cursorCol int, showCursor ...bool) string {
	show := true
	if len(showCursor) > 0 {
		show = showCursor[0]
	}
	s := string(line)
	trimmed := strings.TrimLeft(s, " \t")
	indent := s[:len(s)-len(trimmed)]
	// 把整行光标列换算为相对 trimmed 的局部 rune 列
	insertAt := cursorAt(s, cursorCol)
	// dimStyle 用于语法前缀：暗色前景（不改变显示宽度）
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	hl := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Bold(true)
	quote := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	list := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	task := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	codeBg := lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))

	cursorFunc := insertCursor
	if !show {
		cursorFunc = func(s string, col int) string { return s }
	}

	switch {
	case strings.HasPrefix(trimmed, "# "):
		return indent + hl.Render(trimmed[:2]) + cursorFunc(trimmed[2:], insertAt-2)
	case strings.HasPrefix(trimmed, "## "):
		return indent + hl.Render(trimmed[:3]) + cursorFunc(trimmed[3:], insertAt-3)
	case strings.HasPrefix(trimmed, "### "):
		return indent + hl.Render(trimmed[:4]) + cursorFunc(trimmed[4:], insertAt-4)
	case strings.HasPrefix(trimmed, "> "):
		return indent + quote.Render(trimmed[:2]) + cursorFunc(trimmed[2:], insertAt-2)
	case strings.HasPrefix(trimmed, "- [ ] "), strings.HasPrefix(trimmed, "* [ ] "):
		return indent + task.Render(trimmed[:6]) + cursorFunc(trimmed[6:], insertAt-6)
	case strings.HasPrefix(trimmed, "- [x] "), strings.HasPrefix(trimmed, "* [x] "):
		return indent + task.Render(trimmed[:6]) + cursorFunc(trimmed[6:], insertAt-6)
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
		return indent + list.Render(trimmed[:2]) + cursorFunc(trimmed[2:], insertAt-2)
	case isOrderedList(trimmed):
		idx := strings.Index(trimmed, ". ")
		return indent + list.Render(trimmed[:idx+2]) + cursorFunc(trimmed[idx+2:], insertAt-(idx+2))
	case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
		return indent + codeBg.Render(cursorFunc(trimmed, insertAt))
	case strings.HasPrefix(trimmed, ": "):
		return indent + list.Render(trimmed[:2]) + cursorFunc(trimmed[2:], insertAt-2)
	default:
		_ = dim
		return indent + insertCursor(trimmed, insertAt)
	}
}

// RenderCommentLineWithCursor 渲染编辑模式下"多行注释块内"的光标行：
// 整行以注释样式（暗灰斜体）渲染，并按显示列注入蓝底光标块（保持列对齐）。
// 与 RenderEditLineWithCursor 的差异：主体整体使用注释样式（不解析行内语法），
// 注释内容中的 `*`/`**`/`_` 等不会被误认为 markdown 标记。
func (r *Renderer) RenderCommentLineWithCursor(line []byte, cursorCol int) string {
	s := string(line)
	trimmed := strings.TrimLeft(s, " \t")
	indent := s[:len(s)-len(trimmed)]
	insertAt := cursorAt(s, cursorCol)
	disp := runeColToDisp(trimmed, insertAt)
	// injectCursorBlock 是 ANSI-aware 的（跳过 CSI 序列），可安全用于已含样式的串
	return indent + injectCursorBlock(CommentStyle().Render(trimmed), disp)
}

// RenderEditLineWithCursorStyled 是 RenderEditLineWithCursor 的增强版：
// 主体做行内标记着色（**粗体**/*斜体*/`代码`/~~删除线~~/[链接](url) 的标记字符着色，
// 不改变文本与显示宽度），再按显示列注入蓝底光标块，使光标列与输入位置严格对齐。
// 与 RenderEditLineWithCursor 的区别仅在于主体会带行内语法着色，适合普通行/表格行；
// 代码块内请使用原版 RenderEditLineWithCursor（避免代码中的 * 被误认为斜体标记）。
func (r *Renderer) RenderEditLineWithCursorStyled(line []byte, cursorCol int, showCursor ...bool) string {
	show := true
	if len(showCursor) > 0 {
		show = showCursor[0]
	}
	s := string(line)
	trimmed := strings.TrimLeft(s, " \t")
	indent := s[:len(s)-len(trimmed)]
	// 把整行光标列换算为相对 trimmed 的局部 rune 列
	insertAt := cursorAt(s, cursorCol)
	hl := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Bold(true)
	quote := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	list := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	task := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	switch {
	case strings.HasPrefix(trimmed, "# "):
		return indent + hl.Render(trimmed[:2]) + r.cursorBodyOrPlain(trimmed[2:], insertAt-2, show)
	case strings.HasPrefix(trimmed, "## "):
		return indent + hl.Render(trimmed[:3]) + r.cursorBodyOrPlain(trimmed[3:], insertAt-3, show)
	case strings.HasPrefix(trimmed, "### "):
		return indent + hl.Render(trimmed[:4]) + r.cursorBodyOrPlain(trimmed[4:], insertAt-4, show)
	case strings.HasPrefix(trimmed, "> "):
		return indent + quote.Render(trimmed[:2]) + r.cursorBodyOrPlain(trimmed[2:], insertAt-2, show)
	case strings.HasPrefix(trimmed, "- [ ] "), strings.HasPrefix(trimmed, "* [ ] "):
		return indent + task.Render(trimmed[:6]) + r.cursorBodyOrPlain(trimmed[6:], insertAt-6, show)
	case strings.HasPrefix(trimmed, "- [x] "), strings.HasPrefix(trimmed, "* [x] "):
		return indent + task.Render(trimmed[:6]) + r.cursorBodyOrPlain(trimmed[6:], insertAt-6, show)
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
		return indent + list.Render(trimmed[:2]) + r.cursorBodyOrPlain(trimmed[2:], insertAt-2, show)
	case isOrderedList(trimmed):
		idx := strings.Index(trimmed, ". ")
		return indent + list.Render(trimmed[:idx+2]) + r.cursorBodyOrPlain(trimmed[idx+2:], insertAt-(idx+2), show)
	case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
		// 代码围栏行：保持原样（不应用行内标记着色）
		codeBg := lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))
		return indent + codeBg.Render(r.insertCursorOrPlain(trimmed, insertAt, show))
	case strings.HasPrefix(trimmed, ": "):
		return indent + list.Render(trimmed[:2]) + r.cursorBodyOrPlain(trimmed[2:], insertAt-2, show)
	default:
		return indent + r.cursorBodyOrPlain(trimmed, insertAt, show)
	}
}

// cursorBodyOrPlain 根据 show 参数决定是否注入光标
func (r *Renderer) cursorBodyOrPlain(body string, col int, show bool) string {
	if show {
		return cursorBody(body, col)
	}
	return highlightInlineMarks(body)
}

// insertCursorOrPlain 根据 show 参数决定是否注入光标
func (r *Renderer) insertCursorOrPlain(s string, col int, show bool) string {
	if show {
		return insertCursor(s, col)
	}
	return s
}

// cursorBody 对无 ANSI 的主体串做行内标记着色（宽度不变），并按 rune 列 col 注入蓝底光标块。
func cursorBody(body string, col int) string {
	disp := runeColToDisp(body, col)
	return injectCursorBlock(highlightInlineMarks(body), disp)
}

// runeColToDisp 计算串 s 前 col 个 rune 的显示列宽（含宽字符 w=2）。
func runeColToDisp(s string, col int) int {
	d := 0
	i := 0
	for _, r := range s {
		if i >= col {
			break
		}
		d += CellWidth(r)
		i++
	}
	return d
}

// inlineMarkRE 匹配行内语法标记（粗体/删除线/行内代码/链接/斜体），
// 用于编辑模式光标行的“标记着色”——仅替换标记字符为样式，不改变文本与宽度。
var inlineMarkRE = regexp.MustCompile(
	`\*\*.+?\*\*|~~.+?~~|--.+?--|==.+?==|%%(.+?)%%|` + "`[^`]+?`" + `|\[[^\]]+\]\([^)]*\)|\[\^[^\]]+\]|\^\[[^\]]+\]|\*[^*\s][^*]*?\*`)

// highlightInlineMarks 对行内语法标记字符着色（** * ` ~~ -- == %% [ ] ( ) ^），不改变文本内容与显示宽度。
// 仅用于编辑模式光标行：保留原文（可编辑）同时让 markdown 语法“可见地渲染”出来。
func highlightInlineMarks(s string) string {
	if !strings.ContainsAny(s, "*`~[-=%") {
		return s
	}
	markStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("252"))
	linkStyle := lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("39"))
	return inlineMarkRE.ReplaceAllStringFunc(s, func(m string) string {
		switch {
		case strings.HasPrefix(m, "**") && strings.HasSuffix(m, "**"):
			return markStyle.Render("**") + m[2:len(m)-2] + markStyle.Render("**")
		case strings.HasPrefix(m, "~~") && strings.HasSuffix(m, "~~"):
			return markStyle.Render("~~") + m[2:len(m)-2] + markStyle.Render("~~")
		case strings.HasPrefix(m, "--") && strings.HasSuffix(m, "--"):
			return markStyle.Render("--") + m[2:len(m)-2] + markStyle.Render("--")
		case strings.HasPrefix(m, "==") && strings.HasSuffix(m, "=="):
			return markStyle.Render("==") + m[2:len(m)-2] + markStyle.Render("==")
		case strings.HasPrefix(m, "%%") && strings.HasSuffix(m, "%%"):
			return markStyle.Render("%%") + m[2:len(m)-2] + markStyle.Render("%%")
		case strings.HasPrefix(m, "`") && strings.HasSuffix(m, "`"):
			return bgStyle.Render(m)
		case strings.HasPrefix(m, "[^"):
			return markStyle.Render(m)
		case strings.HasPrefix(m, "^["):
			return markStyle.Render(m[:2]) + m[2:len(m)-1] + markStyle.Render("]")
		case strings.HasPrefix(m, "[") && strings.HasSuffix(m, ")"):
			return linkStyle.Render(m)
		default: // 斜体 *x*
			return markStyle.Render("*") + m[1:len(m)-1] + markStyle.Render("*")
		}
	})
}

// injectCursorBlock 在字符串 s 的显示列 dispCol 处覆盖注入蓝底光标块（宽度不变，ANSI-aware）。
// 与 insertCursor 的区别：按显示列定位并跳过 CSI 序列，可安全用于已含行内着色的串。
func injectCursorBlock(s string, dispCol int) string {
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("33")). // 蓝色背景
		Foreground(lipgloss.Color("231")) // 近白色前景
	if dispCol < 0 {
		dispCol = 0
	}
	if s == "" {
		return cursorStyle.Render(" ")
	}
	runes := []rune(s)
	col := 0
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && !(runes[j] >= '@' && runes[j] <= '~') {
				j++
			}
			if j < len(runes) {
				i = j // 消费整段 CSI（零宽），下一轮循环自增
			} else {
				i = len(runes) - 1 // 残缺序列直接消费到末尾
			}
			continue
		}
		r := runes[i]
		w := 1
		if FBDisplayWidth(string(r)) >= 2 {
			w = 2
		}
		if dispCol < col+w {
			// 光标落在此字符上：字符前内容 + 反色块（覆盖字符）+ reset + 后续内容
			return string(runes[:i]) + cursorStyle.Render(string(r)) + "\x1b[0m" + string(runes[i+1:])
		}
		col += w
	}
	// 循环未命中：光标位于行尾之后（dispCol >= 行总宽），如 "asdf|"。
	// 追加一个蓝底空格表示光标在最后一个字符之后（vim INSERT 模式行尾语义）；
	// 修复：原先“覆盖最后一个可见字符”导致光标始终停在末字符上（始终 -1 列），
	// 例如 asdf 无法定位到 f 之后。insertCursor 早已是“追加空格”的正确行为。
	return s + cursorStyle.Render(" ")
}

// cursorAt 将相对整行的 rune 列转换为相对 trimmed 串内的 rune 列偏移。
// 返回的是 trimmed 的 rune 列下标。约定：s = indent + trimmed。
func cursorAt(s string, cursorCol int) int {
	indent := s[:len(s)-len(strings.TrimLeft(s, " \t"))]
	indentRunes := utf8RuneCount(indent)
	local := cursorCol - indentRunes
	if local < 0 {
		local = 0
	}
	return local
}

// utf8RuneCount 返回 UTF-8 串的 rune 数量。
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// insertCursor 在字符串 s 的第 col 个 rune 处插入一个高亮光标块。
// 保持显示列宽（覆盖式，宽度 1），使用明确的蓝底白字确保任意终端下光标醒目可见。
// col 为 rune 列。
func insertCursor(s string, col int) string {
	// 明确的背景/前景色，比 Reverse 更可靠（不依赖终端默认色）
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("33")). // 蓝色背景
		Foreground(lipgloss.Color("231")) // 近白色前景
	if s == "" {
		// 空行也要显示光标
		return cursorStyle.Render(" ")
	}
	runes := []rune(s)
	if col < 0 {
		col = 0
	}
	if col >= len(runes) {
		// 行末光标：追加一个高亮空格，保持显示宽度
		return s + cursorStyle.Render(" ")
	}
	cursor := cursorStyle.Render(string(runes[col]))
	out := make([]rune, 0, len(runes)+4)
	out = append(out, runes[:col]...)
	out = append(out, []rune(cursor)...)
	out = append(out, runes[col+1:]...)
	return string(out)
}

// renderStyledLine 对单行长语法做“就地”富文本渲染（标题/引用/列表/任务/代码）。
// 采用轻量正则/前缀匹配，而非整段 goldmark 解析，避免逐行解析开销。
func (r *Renderer) renderStyledLine(s string, highlight string) string {
	trimmed := strings.TrimLeft(s, " ")
	indent := s[:len(s)-len(trimmed)]

	switch {
	case strings.HasPrefix(trimmed, "#"):
		// 标题：以 "H1 "/"H2 " ... 前缀标记层级，并对内容着色显示。
		// 内容也做行内渲染（行内代码/粗体/链接等），保证 `` `:ex` `` 这类
		// 标题内行内标记不残留原文（回归：曾直接 h.Render(content) 导致标题内
		// markdown 语法全部原样显示）。
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level < len(trimmed) && trimmed[level] == ' ' {
			content := trimmed[level+1:]
			var h lipgloss.Style
			switch level {
			case 1:
				h = r.Styles.H1
			case 2:
				h = r.Styles.H2
			case 3:
				h = r.Styles.H3
			case 4:
				h = r.Styles.H4
			case 5:
				h = r.Styles.H5
			default:
				h = r.Styles.H6
			}
			prefix := h.Render(fmt.Sprintf("H%d ", level))
			// 顶两级的标题在其下方补一条强调线，使“呈现”更明显；
			// 下划线长度占满可用宽度（仿 vim 的占满整行），未设置时退化为按内容长度
			if level <= 2 {
				ruleLen := r.wrap
				if ruleLen <= 0 {
					ruleLen = 4 + utf8RuneCount(content)
				}
				content = prefix + h.Render(RenderInline(content)) + "\n" + indent + r.Styles.Dim.Render(strings.Repeat("─", ruleLen))
			} else {
				content = prefix + h.Render(RenderInline(content))
			}
			return indent + content
		}
		return indent + r.Styles.H3.Render(trimmed)
	case strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "> "):
		// 嵌套引用：统计连续的 '>' 层级，逐层渲染为 '| ' 标记（ASCII，保证任意
		// 终端/中文 locale 下恒为 1 列，避免 U+2502 变宽撑破行宽），
		// 并对最内层文本内容做行内语法渲染（粗体/斜体/代码/链接等）。
		level := 0
		rest := trimmed
		for {
			if rest == ">" {
				level++
				rest = ""
				break
			}
			if strings.HasPrefix(rest, "> ") {
				level++
				rest = rest[2:]
				continue
			}
			if strings.HasPrefix(rest, ">") && len(rest) > 1 && rest[1] != ' ' {
				// 形如 ">text"（缺空格）：仍当作一层
				level++
				rest = rest[1:]
				continue
			}
			break
		}
		rest = strings.TrimSpace(rest)
		markers := ""
		if level > 0 {
			markers = strings.Repeat("| ", level) // ASCII '|'：U+2502 在 CJK locale 下可能变 2 列
		}
		if rest == "" {
			return indent + markers
		}
		return indent + markers + r.Styles.Quote.Render(RenderInline(rest))
	case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
		return indent + r.Styles.CodeBg.Render(trimmed)
	case strings.HasPrefix(trimmed, "- [ ] ") || strings.HasPrefix(trimmed, "* [ ] "):
		return indent + r.Styles.Task.Render("[ ] "+RenderInline(trimmed[6:]))
	case strings.HasPrefix(trimmed, "- [x] ") || strings.HasPrefix(trimmed, "* [x] "):
		return indent + r.Styles.Task.Render("[x] "+RenderInline(trimmed[6:]))
	case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
		lvl := indentLevel(indent)
		return indent + r.Styles.List.Render(listBullet(lvl)+" "+RenderInline(trimmed[2:]))
	case strings.HasPrefix(trimmed, "+ "):
		lvl := indentLevel(indent)
		return indent + r.Styles.List.Render(listBullet(lvl)+" "+RenderInline(trimmed[2:]))
	case isOrderedList(trimmed):
		// 有序列表：保留 "1. " 序号，内容渲染行内语法
		idx := strings.Index(trimmed, ". ")
		return indent + r.Styles.List.Render(trimmed[:idx+2]+RenderInline(trimmed[idx+2:]))
	case isHR(trimmed):
		// 分隔线：用一条横线呈现，长度占满可用宽度（未设置时退化为定长）
		ruleLen := r.wrap
		if ruleLen <= 0 {
			ruleLen = 20
		}
		return indent + r.Styles.Dim.Render(strings.Repeat("─", ruleLen))
	case IsFootnoteDefLine(trimmed):
		// 脚注定义 [^id]: 内容 —— 显示 [id] 编号 + 行内渲染的内容
		t := trimmed
		ci := strings.Index(t, "]:")
		id := t[2:ci]
		rest := strings.TrimSpace(t[ci+2:])
		content := rest
		if rest != "" {
			content = RenderInline(rest)
		}
		return indent + stFnNum.Render("["+id+"] ") + content
	case IsDefListLine(trimmed):
		// 定义列表定义行 : term —— 以 ▸ 标记呈现，并对内容渲染行内语法
		return indent + r.Styles.List.Render("▸ "+RenderInline(trimmed[2:]))
	default:
		// 普通段落行：渲染行内语法（粗体/斜体/链接/emoji 等）
		return r.applyHighlight(RenderInline(s), highlight)
	}
}

// applyHighlight 在文本中高亮所有搜索关键字匹配。
func (r *Renderer) applyHighlight(s, keyword string) string {
	if keyword == "" {
		return s
	}
	idx := strings.Index(strings.ToLower(s), strings.ToLower(keyword))
	if idx < 0 {
		return s
	}
	// 简单实现：对第一次匹配加反显样式（可扩展为全部匹配）
	before := s[:idx]
	match := s[idx : idx+len(keyword)]
	after := s[idx+len(keyword):]
	hl := lipgloss.NewStyle().Reverse(true).Render(match)
	return before + hl + after
}

// RenderParagraph 对一段完整的 Markdown 文本使用 goldmark 做整段富文本渲染。
// 主要用于：代码块、表格、多行引用等块级元素，需要跨行解析时调用。
// 返回渲染后的 ANSI 字符串。
func (r *Renderer) RenderParagraph(lines [][]byte) string {
	var buf bytes.Buffer
	src := bytes.Join(lines, []byte("\n"))
	// goldmark 解析并写入 buf（输出为 HTML）。
	// 注意：终端无法直显 HTML。本项目的块级富文本采用“逐行近似渲染”
	// （见 renderStyledLine），无需完整 HTML->ANSI 转换，性能更好。
	// 此函数作为“接入 glamour 做整段精确渲染”的扩展预留点保留。
	if err := goldmark.Convert(src, &buf); err != nil {
		// 出错时退回纯文本，保证健壮性
		return string(src)
	}
	return buf.String()
}

// isOrderedList 判断是否为有序列表项，如 "1. "。
func isOrderedList(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i < len(s) && s[i] == '.' && i+1 < len(s) && s[i+1] == ' '
}

// isHR 判断是否为分隔线：---、***、___（至少 3 个相同字符，可含首尾空格）。
func isHR(s string) bool {
	t := strings.TrimSpace(s)
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 1; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

// indentLevel 根据前导空白估算 Markdown 嵌套层级（每 2 个空格约一级）。
func indentLevel(indent string) int {
	spaces := 0
	for _, r := range indent {
		switch r {
		case ' ':
			spaces++
		case '\t':
			spaces += 8
		}
	}
	return spaces / 2
}

// listBullet 按层级返回不同无序列表标记，循环使用 • / ◦ / ▪。
func listBullet(level int) string {
	markers := []string{"•", "◦", "▪"}
	return markers[level%len(markers)]
}

// RenderDocument 使用 glamour 将整篇 Markdown 渲染为带 ANSI 的富文本，
// 效果与 mdcat 一致（完整支持 GFM：标题、列表、表格、任务列表、代码块、引用等）。
// width 用于软换行（避免超出终端宽度）。返回渲染后的完整字符串（含换行符）。
func (r *Renderer) RenderDocument(src []byte, width int) (string, error) {
	if width <= 0 {
		width = 80
	}
	gr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	out, err := gr.Render(string(src))
	if err != nil {
		return "", err
	}
	return out, nil
}

// RenderPreviewLine 预览模式下单行 Markdown 富文本渲染（与 mdcat 风格一致）。
// 使用内部复用的 glamour TermRenderer，对含语法的行（标题/列表/任务/引用等）生效，
// 纯文本行直接透传，不会被分析整段（保证行号 1:1 对应 buffer 行，避免错位）。
// 返回不带行号前缀的单行渲染结果。
func (r *Renderer) RenderPreviewLine(line []byte) string {
	s := string(line)
	// 空行：直接返回空串（保持原样）
	if strings.TrimSpace(s) == "" {
		return ""
	}
	if r.gRenderer == nil {
		// glamour 未初始化时退回纯文本
		return s
	}
	out, err := r.gRenderer.Render(s)
	if err != nil {
		return s
	}
	// glamour 对单行输入会在段首插入一个空行（\n），去掉前导换行。
	out = strings.TrimLeft(out, "\n")
	// 取首个可视行：glamour 段后还会填充空格到 wrap 宽度，截到第一个 \n 即可。
	if idx := strings.IndexByte(out, '\n'); idx >= 0 {
		out = out[:idx]
	}
	// 去掉 glamour 为对齐 wrap 宽度而在行尾填充的空格（保留语义空格）。
	out = trimTrailingSpaces(out)
	return out
}

// trimTrailingSpaces 去掉字符串尾部由空白字符（含 ANSI 转义包裹的空格）组成的填充，
// 但保留字符串中间的正常空格。实现上从末尾逐个剥去"空格"或"完整的 ANSI CSI 转义序列"
// （如 \x1b[38;5;252m、\x1b[m），直到遇到可见内容为止。
func trimTrailingSpaces(s string) string {
	b := []byte(s)
	end := len(b)
	for end > 0 {
		// 末尾是普通空格/制表符，直接剥去
		if b[end-1] == ' ' || b[end-1] == '\t' {
			end--
			continue
		}
		// 末尾是一个完整的 ANSI CSI 序列（以 \x1b[ 开头、字母或 m 结尾），剥去
		// 先回退找到 \x1b[ 起点
		last := b[end-1]
		if (last >= 'A' && last <= 'Z') || last == 'm' {
			// 从 end-1 向前找到匹配的 \x1b[
			k := end - 1
			for k >= 2 && !(b[k-2] == 0x1b && b[k-1] == '[') {
				k--
			}
			if k >= 2 && b[k-2] == 0x1b && b[k-1] == '[' {
				end = k - 2 // 剥去整个 \x1b[...X 序列
				continue
			}
		}
		break
	}
	return string(b[:end])
}

// SplitRendered 将 glamour 渲染后的整块文本按换行符拆成可视行，
// 供 Preview 模式按行滚动显示。会剥离末尾多余空行。
func SplitRendered(s string) []string {
	lines := strings.Split(s, "\n")
	// 去掉末尾空行噪音（glamour 通常在结尾输出一个空行）
	for len(lines) > 0 && strings.TrimRight(lines[len(lines)-1], " \t") == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
