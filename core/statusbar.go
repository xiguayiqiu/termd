package core

import (
	"fmt"
	"strings"
	"termd"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// =====================================================================
// 状态栏 / 命令栏（底部栏）
//
// 设计目标（仿 vim）：屏幕从顶到底被严格切成三块互不重叠的固定区域——
//   内容区  →  状态栏（常驻 1 行）  →  命令栏（仅命令/搜索模式出现，状态栏下方 1 行）。
// 内容区高度已在 model.contentHeight() 阶段为这两块底栏预留行数，永不重叠。
//
// 状态栏采用两段式布局（占满整行）：
//   [ 模式块 文件名 ± ]        [ 行号,列号 百分比 总行数 行号模式 | 命令反馈 ]
// 命令栏独占整行：前缀高亮 + 灰底正文 + 末尾实心光标块。
// =====================================================================

// statusColors 描述状态栏一段（左模式块 / 右标尺块）的配色。
type statusColors struct {
	bg, fg lipgloss.Color
}

// cmdPrefixStyle 根据命令前缀返回命令行前缀的高亮（仿 vim 的 : / ? 提示）。
func cmdPrefixStyle(prefix rune) (bg, fg lipgloss.Color) {
	switch prefix {
	case '/':
		return lipgloss.Color("30"), lipgloss.Color("255") // 青底（搜索）
	case '?':
		return lipgloss.Color("54"), lipgloss.Color("255") // 紫底（反向搜索）
	case ':':
		return lipgloss.Color("24"), lipgloss.Color("255") // 深蓝底（ex 命令）
	default:
		return lipgloss.Color("236"), lipgloss.Color("255")
	}
}

// modeBlockColors 返回当前模式下状态栏左段（模式块）的配色，贴近 vim StatusLine。
func (m *EditorModel) modeBlockColors() statusColors {
	switch m.sm.Mode() {
	case termd.ModeEdit:
		if m.sm.EditSubName() == "INSERT" {
			return statusColors{lipgloss.Color("34"), lipgloss.Color("255")} // 绿底（vim INSERT）
		}
		return statusColors{lipgloss.Color("61"), lipgloss.Color("255")} // 紫底（NORMAL）
	case termd.ModePreview:
		return statusColors{lipgloss.Color("25"), lipgloss.Color("255")} // 深蓝底
	default:
		return statusColors{lipgloss.Color("63"), lipgloss.Color("255")} // 蓝底（命令等）
	}
}

// statusBar 渲染底部状态栏（固定 1 行，永远在最底部，仿 vim）。
func (m *EditorModel) statusBar() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	// ---- 左段：模式块 + 文件名 + 修改标记 ----
	mode := termd.ModeNames[m.sm.Mode()]
	if sub := m.sm.EditSubName(); sub != "" {
		mode = sub // EDIT 模式直接显示 INSERT / NORMAL，更贴近 vim
	}
	mc := m.modeBlockColors()
	// 文件名 + 编码识别 + 脏标记
	fname := m.Buf.FilePath()
	if fname == "" {
		fname = "[未命名]"
	}
	encTag := ""
	if name := m.Buf.Encoding.Name; name != "" {
		encTag = "[" + name + "]" // 仿 vim 状态栏的 fenc= 显示（如 [utf-8] / [gbk] / [utf-16le]）
	}
	flags := ""
	if m.Buf.IsDirty {
		flags += "[+]"
	}
	leftText := fmt.Sprintf(" %s  %s %s %s ", mode, fname, encTag, flags)
	// 防止左段超宽撑破整行（超宽会使 bubbletea 截断 View 末行，导致滚动序列的
	// 滚动区恢复部分丢失）；截断到 width-1，至少留 1 列给右侧。
	leftText = runewidth.Truncate(leftText, width-1, "…")
	left := lipgloss.NewStyle().Background(mc.bg).Foreground(mc.fg).Render(leftText)

	// ---- 右段：优先显示状态消息（命令反馈），否则显示标尺 ----
	var rightText string
	if m.status != "" {
		// 命令反馈消息（如“已保存”），仿 vim 临时覆盖标尺区
		rightText = " " + m.status + " "
	} else {
		// 标尺：行,列 / 进度分析 / 总行数 / 行号模式
		rowLine := m.cursorRow
		if m.sm.Mode() == termd.ModePreview {
			rowLine = m.previewCursor
		}
		total := m.Buf.LineCount()
		col := m.cursorCol + 1
		// 进度（仿 vim）：
		//   - 基础：光标所在行百分比（ruler %p 的 calc_percentage，溢出安全）
		//   - 视口边界状态（状态栏 %P 的 get_rel_pos）：整个文件可见显示 All、
		//     视口顶贴文件首行显示 Top、视口底贴文件末行显示 Bot。
		pct := calcPercentage(rowLine+1, total)
		progress := fmt.Sprintf("%d%%%%", pct)
		if vp := m.viewportProgress(); vp == "Top" || vp == "Bot" || vp == "All" {
			progress = vp
		}
		lnMode := ""
		if lm := m.sm.LineNumMode(); lm != termd.LNNone {
			lnMode = " " + lm.Name()
		}
		rightText = fmt.Sprintf("%d,%d  %s  %dL%s ", rowLine+1, col, progress, total, lnMode)
	}
	rulerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("238")).Foreground(lipgloss.Color("252"))
	right := rulerStyle.Render(rightText)

	// ---- 计算左右段纯宽度，中间补空格占满整行 ----
	leftW := runewidth.StringWidth(stripANSIForWidth(leftText))
	rightW := runewidth.StringWidth(stripANSIForWidth(rightText))
	pad := width - leftW - rightW
	if pad < 0 {
		// 宽度不足：优先保证左段，截断右段
		pad = 0
		right = ""
	}
	mid := rulerStyle.Render(strings.Repeat(" ", pad))
	return left + mid + right
}

// renderCmdLine 渲染命令/搜索输入框（仿 vim 的命令行，显示在最底部，独占整行）。
func (m *EditorModel) renderCmdLine() string {
	input := m.sm.CmdInput
	if input == "" {
		input = ":"
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	prefix := rune(input[0])
	bg, fg := cmdPrefixStyle(prefix)

	// 输入框正文（去掉前导前缀符，前缀单独高亮成一块），光标块放在末尾。
	body := input
	if r := []rune(input); len(r) > 1 {
		body = string(r[1:])
	} else {
		body = "" // 仅前缀符时正文为空
	}

	// 前缀块
	pre := lipgloss.NewStyle().Background(bg).Foreground(fg).Render(string(prefix))
	// 正文（在整行底色上）。防止超宽撑破整行（超宽会使 bubbletea 截断 View 末行）；
	// 留 3 列：1 前缀 + 1 光标块 + 1 余量。
	body = runewidth.Truncate(body, width-3, "…")
	bodyW := runewidth.StringWidth(body)
	// 光标块紧贴输入正文末尾（不隔空格、不粘行末），与 cmdlineCursorGoto 定位列完全一致：
	//   - col 1：前缀
	//   - col 2..(1+bodyW)：已输入正文
	//   - col 1+bodyW+1：光标块（即「正文结束后第 1 位」，vim 命令行光标位置）
	//   - 之后：灰底空格占满行末
	// 此前踩过的坑：
	//   1) 用 24-bit 粉底 #ff79c6 + dracula 前景 #282a36，在不支持 TrueColor 的终端被
	//      降级成黑色块，深色终端上不可见。
	//   2) 改成白底(255)黑字(0)后仍显示黑块——因为 █ 是实心字符，字符本身（前景色）
	//      填满整个单元格，背景色被完全覆盖。所以「块要什么颜色」必须设在前景色上。
	//   3) body 后多留了 1 个空格，导致块落在 col 1+bodyW+2、与硬件光标定位的
	//      col 1+bodyW+1 错开 1 位 → 去掉空格，块紧跟正文。
	field := lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("255"))
	// 白色光标块：前景白色(255)+Bold 使实心 █ 整格变白；背景用深色兜底即可。
	cursorBlock := lipgloss.NewStyle().
		Bold(true).
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("255")).
		Render("█")
	// 剩余行末空格（光标块之后用 236 灰底铺到行末）
	trailingW := width - 1 - bodyW - 1
	if trailingW < 0 {
		trailingW = 0
	}
	trailing := lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(strings.Repeat(" ", trailingW))
	return pre + field.Render(body) + cursorBlock + trailing
}

// calcPercentage 计算 part/whole 的百分比（仿 vim 的 calc_percentage）：
// part 很大时先用 part 除以 whole/100，避免 part*100 溢出（>21 亿行的超大文件）。
func calcPercentage(part, whole int) int {
	if whole <= 0 {
		return 0
	}
	if part > 1_000_000 {
		if w := whole / 100; w > 0 {
			return part / w
		}
	}
	return (part * 100) / whole
}

// viewportProgress 返回视口相对位置（仿 vim 状态栏 %P 的 get_rel_pos）：
//   - All：整个文件都在视口内（below <= 0 且 above <= 0）；
//   - Top：视口顶贴文件首行（above <= 0）；
//   - Bot：视口底贴文件末行（below <= 0）；
//   - NN%：否则为「视口顶部之上行数 / (之上+之下)」的百分比。
//
// 与 vim 一致，该进度基于视口（滚动）而非光标，适合在文件中快速定位“看到哪了”。
func (m *EditorModel) viewportProgress() string {
	top := m.scroll
	total := m.Buf.LineCount()
	viewH := m.contentHeight()
	if m.sm.Mode() == termd.ModePreview {
		top = m.previewScroll
		if n := len(m.previewLines); n > 0 {
			total = n // 预览以视觉行为准（block 渲染后 preview 行数 ≠ buffer 行数）
		}
	}
	if total <= 0 || viewH <= 0 {
		return "0%"
	}
	// 仿 vim：above = w_topline - 1（视口顶之上），below = line_count - w_botline + 1（视口底之下）。
	above := top
	below := total - (top + viewH)
	if below <= 0 {
		if above <= 0 {
			return "All"
		}
		return "Bot"
	}
	if above <= 0 {
		return "Top"
	}
	return fmt.Sprintf("%d%%", calcPercentage(above, above+below))
}

// cmdlineCursorGoto 返回把硬件光标定位到命令行输入框的 ANSI 序列（仿 vim 命令行）。
// 命令行固定在屏幕倒数第二行（height 行是底线）；光标块紧跟已输入正文末尾，
// 因此硬件光标定位到光标块所在列（1 前缀 + 正文宽度 + 1 = 块起始列），形成块光标效果。
func (m *EditorModel) cmdlineCursorGoto() string {
	row := m.height - 1
	if row < 1 {
		row = 1
	}
	col := 1 + 1 // 前缀 + 空正文时的光标块
	input := m.sm.CmdInput
	if r := []rune(input); len(r) > 1 {
		col = 1 + runewidth.StringWidth(string(r[1:])) + 1
	}
	return cursorGoto(row, col, m.blinkMode)
}
