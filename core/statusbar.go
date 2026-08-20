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
	// 文件名 + 脏标记
	fname := m.Buf.FilePath()
	if fname == "" {
		fname = "[未命名]"
	}
	flags := ""
	if m.Buf.IsDirty {
		flags += "[+]"
	}
	leftText := fmt.Sprintf(" %s  %s %s ", mode, fname, flags)
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
		// 标尺：行,列 / 百分比 / 总行数 / 行号模式
		rowLine := m.cursorRow
		if m.sm.Mode() == termd.ModePreview {
			rowLine = m.previewCursor
		}
		total := m.Buf.LineCount()
		col := m.cursorCol + 1
		pct := 0
		if total > 0 {
			pct = (rowLine * 100) / total
		}
		lnMode := ""
		if lm := m.sm.LineNumMode(); lm != termd.LNNone {
			lnMode = " " + lm.Name()
		}
		rightText = fmt.Sprintf("%d,%d  %d%%%%  %dL%s ", rowLine+1, col, pct, total, lnMode)
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
	field := lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("255"))
	// 补齐空格占满整行：总宽 = width；已用 = 1(前缀) + bodyW + 1(光标块)
	used := 1 + runewidth.StringWidth(body) + 1
	pad := width - used
	if pad < 0 {
		pad = 0
	}
	fieldText := body + strings.Repeat(" ", pad)
	// 光标块：实心块（仿 vim 命令行光标）
	cursorBlock := lipgloss.NewStyle().
		Background(lipgloss.Color("255")).Foreground(lipgloss.Color("236")).Render("█")
	return pre + field.Render(fieldText) + cursorBlock
}
