package core

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"termd"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

// ----------------------------------------------------------
// Preview 模式按键处理
// ----------------------------------------------------------
func (m *EditorModel) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 大纲侧边栏打开时，导航按键优先交给大纲处理（未消费则继续常规逻辑）
	if m.outlineMode {
		if m2, c, handled := m.handleOutlineKey(msg); handled {
			return m2, c
		}
	}
	switch msg.String() {
	case "ctrl+t": // 切换大纲侧边栏（keymap: preview.outline）
		m.toggleOutline()
		// 大纲开关改变内容区宽度（重排 previewLines），滚动区失效
		return m, m.clearScrollRegionCmd()
	case "ctrl+o": // 打开文件浏览器（keymap: preview.fileBrowser）
		m.openFileBrowser()
		// 从 Preview 全屏切换到另一种全屏视图：必须清除 Preview 遗留的终端物理滚动区
		//（DECSTBM），否则滚动区 diff 缓存会与文件浏览器两栏内容叠加混排。
		m.invalidateScrollRender()
		return m, m.clearScrollRegionCmd()
	case "i", "e", "a": // KeyI/KeyE/KeyA 见 keymap.go（编辑模式进入）
		// 进入编辑模式：光标接续到预览当前高亮行（i/e 行首插入，a 行尾插入）
		if err := m.sm.EnterEdit(); err == nil {
			m.cursorRow = clamp(m.previewCursor, 0, m.Buf.LineCount()-1)
			if msg.String() == "a" {
				m.cursorCol = byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
			} else {
				m.cursorCol = 0
			}
			m.cursWant = m.cursorCol
			// 让 Edit 视口（scroll）跟随光标行，进入编辑立刻可见（仿 vim）
			m.ensureCursorVisible()
			m.status = termd.T("INSERT（Esc 进入 NORMAL 导航态）")
			// 离开预览渲染：清除内容区滚动区，交还 bubbletea 渲染
			return m, m.clearScrollRegionCmd()
		}
	case "o", "O": // 在光标行下方/上方插入空行并进入插入模式（仿 vim o/O）
		if err := m.sm.EnterEdit(); err == nil {
			if msg.String() == "O" {
				m.cursorRow = m.Buf.OpenLineAbove(m.previewCursor)
			} else {
				m.cursorRow = m.Buf.OpenLineBelow(m.previewCursor)
			}
			m.cursorCol = 0
			m.cursWant = m.cursorCol
			// 让 Edit 视口（scroll）跟随光标行，进入编辑立刻可见（仿 vim）
			m.ensureCursorVisible()
			m.status = termd.T("INSERT（Esc 进入 NORMAL 导航态）")
			// 离开预览渲染：清除内容区滚动区，交还 bubbletea 渲染
			return m, m.clearScrollRegionCmd()
		}
	case termd.KeyColon: // : 进入命令模式（keymap: preview.command）
		_ = m.sm.EnterCommand()
		// 命令模式多占一行命令行，内容区高度变化，滚动区失效
		return m, m.clearScrollRegionCmd()
	case termd.KeySlash: // / 进入搜索模式（keymap: preview.search）
		_ = m.sm.EnterSearch()
		// 搜索高亮变化 + 高度变化，滚动区失效
		return m, m.clearScrollRegionCmd()
	case termd.KeyUp, "k": // 上移高亮行（keymap: preview.up）
		m.previewCursor = max(0, m.previewCursor-1)
		m.previewColKeep()
		m.ensurePreviewCursorVisible()
	case termd.KeyDown, "j": // 下移高亮行（keymap: preview.down）
		m.previewCursor = min(m.Buf.LineCount()-1, m.previewCursor+1)
		m.previewColKeep()
		m.ensurePreviewCursorVisible()
	case termd.KeyLeft, "h": // 左移列光标（keymap: preview.left）
		m.previewCol = max(0, m.previewCol-1)
	case termd.KeyRight, "l": // 右移列光标（keymap: preview.right）
		m.previewCol = min(m.previewLineDisplayWidth(m.previewCursor), m.previewCol+1)
	case termd.KeyEnter: // 回车：光标停在超链接上则跳转（URL / 本地 md 文件），否则下移（keymap: preview.down）
		if t := m.linkTargetAtDispCol(m.previewCursor, m.previewCol); t != "" {
			m.openLink(t)
			return m, nil
		}
		m.previewCursor = min(m.Buf.LineCount()-1, m.previewCursor+1)
		m.previewColKeep()
		m.ensurePreviewCursorVisible()
	case termd.KeyPgUp: // 上翻页（keymap: preview.pageUp）：按视觉行翻页，光标移到新窗口顶部（仿 vim C-b）
		ch := visibleLines(m)
		if ch < 1 {
			ch = 1
		}
		if len(m.previewLines) > 0 {
			m.previewScroll = clamp(m.previewScroll-max(1, ch-1), 0, max(0, len(m.previewLines)-ch))
			m.setPreviewCursorToRow(m.previewScroll)
		}
	case termd.KeyPgDown: // 下翻页（keymap: preview.pageDown）：按视觉行翻页，光标移到新窗口底部（仿 vim C-f）
		ch := visibleLines(m)
		if ch < 1 {
			ch = 1
		}
		if len(m.previewLines) > 0 {
			m.previewScroll = clamp(m.previewScroll+max(1, ch-1), 0, max(0, len(m.previewLines)-ch))
			m.setPreviewCursorToRow(m.previewScroll + ch - 1)
		}
	case "d": // "dd" 删除当前行（仿 vim dd）
		if m.handleDoubleKey("d") {
			m.deleteCurrentPreviewLine()
			m.previewDirty = true
			m.status = termd.Tf("已删除第 %d 行", m.previewCursor+1)
		}
	}
	// 光标移动后重建渲染：使“光标是否落在代码块/表格内”的降级逻辑即时生效
	// （进入块→显示原始文本，离开块→立即恢复渲染），避免块级宽内容导致飘逸。
	m.previewDirty = true
	return m, nil
}

// renderCmdLine 渲染命令/搜索输入框（仿 vim 的命令行，显示在最底部，独占整行）。
// renderPreview 渲染 Preview 模式。
// 采用 block-aware 渲染：普通行逐行富文本（1:1 对应 buffer 行，带行号），
// 块级语法（代码块/表格等）整块 glamour 渲染；块内多行共享首个 buffer 行号。
// 返回值第二项为当前行在可视区内的 1-based 绝对行号（用于把硬件光标定位过去），
// 若当前行不在可视区内则返回 -1。
func (m *EditorModel) renderPreview() (string, int) {
	// 内容变更或窗口尺寸变化时重建渲染缓存
	if m.previewDirty {
		m.rebuildPreview()
		m.previewDirty = false
		// 重建后基于“最新”的 previewLines / bufferToPreview 重新对齐视口：
		// 降级块（光标落在代码块/表格内）会改变块级行数，若仍用 handler 中基于旧映射
		// 算出的 previewScroll，光标可能越界或错位（典型表现：向上移动光标时整体飘逸到
		// 第一行）。重建后重算一次 ensure，保证蓝底光标块始终落在当前光标行。
		m.ensurePreviewCursorVisible()
	}
	// 缓冲区没有任何实际内容：显示 vim 风格介绍信息（欢迎屏），替代空白 + 波浪线。
	if m.isBufferEmpty() {
		return m.renderIntro(), -1
	}
	var b strings.Builder
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	// 视口顶部 = previewScroll（视觉行偏移，仿 glow viewport 的 yOffset）。
	// 渲染时始终输出 previewLines[previewScroll : previewScroll+ch] 这一可见切片，
	// 滚动只是改变偏移，交给 bubbletea 逐行 diff 只重绘变化行 —— 平滑且永不越界。
	m.clampPreviewScroll()
	start := m.previewScroll
	end := min(len(m.previewLines), start+ch)
	// 预览模式下高亮“当前行”：previewCursor 对应的首个 preview 行（与 viewport 解耦）。
	cursorIdx := -1
	if m.previewCursor >= 0 && m.previewCursor < len(m.bufferToPreview) {
		cursorIdx = m.bufferToPreview[m.previewCursor]
	}
	for i := start; i < end; i++ {
		lineOut := m.previewLines[i]
		if m.sm.Mode() == termd.ModePreview && m.previewRowToBuffer[i] == m.previewCursor && i >= cursorIdx {
			// 预览模式列光标：按 previewCol（渲染后显示列）在“当前高亮行”的
			// 任意可见软换行段内注入反色块，指示“光标所在的列”，回车即可跳转
			// 光标处的链接。内部会自行判断 previewCol 是否落在该段内（不在则
			// 原样返回），因此对高亮行的每个可见段调用都安全。
			lineOut = m.injectPreviewColCursor(lineOut, i)
		}
		if i == cursorIdx {
			// 当前行高亮（gutter 蓝底，视觉提示）。放在列光标之后注入，
			// 保证蓝底块在行首、列光标在行内，互不覆盖。
			lineOut = injectPreviewCursor(lineOut)
		}
		b.WriteString(lineOut + "\n")
	}
	// 文件末尾之后的空白区域：用 vim 风格的 '~' 标记未编辑行。
	// 仿 vim 放在最左列（第 1 列），不占用行号 gutter，紧贴边缘。
	tildeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	for i := end; i < start+ch; i++ {
		b.WriteString(tildeStyle.Render("~") + "\n")
	}
	// 当前行绝对行号（1-based，内容区顶到第 1 行）。
	absRow := -1
	if cursorIdx >= start && cursorIdx < end {
		absRow = cursorIdx - start + 1
	}
	return b.String(), absRow
}

// isBufferEmpty 判断缓冲区是否“没有实际内容”（所有行均为空或仅空白）。
// 用于预览模式空缓冲区时显示 vim 风格介绍信息。
func (m *EditorModel) isBufferEmpty() bool {
	for i := 0; i < m.Buf.LineCount(); i++ {
		if strings.TrimSpace(string(m.Buf.GetLine(i))) != "" {
			return false
		}
	}
	return true
}

// renderIntro 在缓冲区为空时显示 vim 风格介绍信息（欢迎屏）。
// 仿 vim 启动画面：标题 + 版本 + 常用键位，整块水平居中、顶部对齐，
// 下方其余可视行用 '~' 填充，保持与 renderPreview 相同的行数（viewport 高度）。
func (m *EditorModel) renderIntro() string {
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	width := m.contentWidth()
	if width < 20 {
		width = 20
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	verStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	rows := [][2]string{
		{"i / a", termd.T("进入编辑模式")},
		{"/", termd.T("搜索（Preview 模式）")},
		{":", termd.T("进入命令模式")},
		{":help", termd.T("查看键位帮助")},
		{":q", termd.T("退出")},
	}
	// 计算 key 列固定宽度，使描述列对齐。
	keyW := 0
	for _, r := range rows {
		if w := termd.FBDisplayWidth(r[0]); w > keyW {
			keyW = w
		}
	}
	lines := []string{
		titleStyle.Render(termd.T("termd — 终端 Markdown 编辑器")),
		"",
		// 版本号取自根包唯一常量 termd.Version，发布新版本只需改 version.go 一处。
		verStyle.Render(termd.T("版本") + " " + termd.Version),
		"",
	}
	for _, r := range rows {
		pad := keyW - termd.FBDisplayWidth(r[0]) + 2
		lines = append(lines, keyStyle.Render(r[0])+strings.Repeat(" ", pad)+descStyle.Render(r[1]))
	}
	lines = append(lines,
		"",
		hintStyle.Render(termd.T("类型 :help<Enter> 查看键位帮助，:q<Enter> 退出")),
	)
	// 整块水平居中：先求块内最大显示宽度（去 ANSI 后），对每行统一前置
	// 相同缩进，使整块（而非逐行）位于屏幕中央，键位列左缘对齐。
	blockW := 0
	for _, l := range lines {
		if w := termd.FBDisplayWidth(stripANSIForWidth(l)); w > blockW {
			blockW = w
		}
	}
	pad := (width - blockW) / 2
	if pad < 0 {
		pad = 0
	}
	indent := strings.Repeat(" ", pad)
	// 垂直居中：仿 vim 启动画面，正文块置于内容区竖直中央，
	// 上方与下方均用 '~' 填充。
	tildeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	topPad := (ch - len(lines)) / 2
	if topPad < 0 {
		topPad = 0
	}
	var b strings.Builder
	for i := 0; i < ch; i++ {
		if i < topPad || i >= topPad+len(lines) {
			b.WriteString(tildeStyle.Render("~") + "\n")
		} else {
			b.WriteString(indent + lines[i-topPad] + "\n")
		}
	}
	return b.String()
}

// ensurePreviewCursorVisible 让 viewport(scroll)跟随 previewCursor：
// 若当前高亮行（蓝底块）在可视区之外，则平移 scroll 使其进入可视区。
// 关键：preview 中一个 buffer 行可能展开成多个 preview 视觉行（代码块/表格/软换行），
// 因此必须基于 preview 视觉行索引判断，否则蓝底光标块会被推到窗口之外。
func (m *EditorModel) ensurePreviewCursorVisible() {
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	if len(m.previewLines) == 0 {
		return
	}
	m.clampPreviewScroll()
	if m.previewCursor < 0 || m.previewCursor >= m.Buf.LineCount() {
		return
	}
	if m.previewCursor >= len(m.bufferToPreview) {
		return
	}
	// previewCursor 对应的 preview 视觉行索引（bufferToPreview 已维护）。
	cpidx := m.bufferToPreview[m.previewCursor]
	if cpidx < m.previewScroll {
		// 高亮行在窗口上方：把视口移到该行。
		m.previewScroll = cpidx
	} else if cpidx >= m.previewScroll+ch {
		// 高亮行在窗口下方：把视口起点移到使 cpidx 落在窗口底部首行。
		m.previewScroll = cpidx - ch + 1
	}
	m.clampPreviewScroll()
}

// previewColKeep 在预览高亮行改变后，把列光标钳制到当前行渲染后的显示宽度内
// （仿 vim colKeep：上下移动时尽量保持列，短行则钳到行尾）。
func (m *EditorModel) previewColKeep() {
	m.previewCol = clamp(m.previewCol, 0, m.previewLineDisplayWidth(m.previewCursor))
}

// previewLineDisplayWidth 返回预览中 buffer 行 brow 渲染后的显示宽度
// （不含行号前缀；与 linkTargetAtDispCol 的列口径一致，供 h/l 列移动钳制）。
func (m *EditorModel) previewLineDisplayWidth(brow int) int {
	if brow < 0 || brow >= m.Buf.LineCount() {
		return 0
	}
	line := m.Buf.GetLine(brow)
	if len(line) == 0 {
		return 0
	}
	rendered := m.Rend.RenderLine(line, true, m.sm.SearchKeyword)
	return termd.FBDisplayWidth(stripANSIForWidth(rendered))
}

// cursorGoto 返回将真实硬件光标移动到绝对 (row, col) 的 ANSI 序列。
// 先确保光标可见（\x1b[?25h），再绝对定位（\x1b[<row>;<col>H）；
// 若 blink 为真，额外开启光标闪烁（\x1b[?12h），否则关闭（\x1b[?12l）。
// row/col 均为 1-based，符合终端 ANSI 约定；控制序列不占显示宽度，可安全注入文本末端。
func cursorGoto(row, col int, blink bool) string {
	blinkSeq := "\x1b[?12l"
	if blink {
		blinkSeq = "\x1b[?12h"
	}
	// \x1b[0m 先重置所有 SGR 样式（前景/背景/粗体等），清除渲染文本末尾可能
	// 残留的颜色控制码（如 \x1b[38;5;xxxm）。否则光标定位序列会继承未知的前
	// 景色，导致光标区被染成乱码颜色码（用户反馈的“光标变 [38m 啥的”）。
	return "\x1b[?25h" + blinkSeq + "\x1b[0m" + "\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "H"
}

// hideCursor 输出隐藏硬件光标的 ANSI 序列（预览模式沉浸式阅读时使用，
// 仅依赖蓝底块 injectPreviewCursor 指示当前行，避免硬件光标残留在底部状态栏位置）。
func hideCursor() string {
	return "\x1b[?25l"
}

// injectPreviewCursor 在预览/只读模式下给当前行注入一个蓝底光标块，
// 作为“当前行”的视觉指示（与编辑模式光标块样式一致）。
//
// 旧实现直接在 rune 数组中查找空格并替换，会切到 glamour 渲染后自带的 ANSI
// 序列内部，导致 ESC 上下文错乱，颜色码（如 [38;5;...m）变成可见文本。
// 现在改为在行首安全注入：光标块 + reset，再拼接原字符串，绝不破坏原 ANSI。
func injectPreviewCursor(s string) string {
	cursor := lipgloss.NewStyle().
		Background(lipgloss.Color("33")).
		Foreground(lipgloss.Color("231")).
		Render(" ")
	if s == "" {
		return cursor
	}
	// 先重置所有 SGR，再输出原字符串，保证光标块独立、不继承/不破坏原样式。
	return cursor + "\x1b[0m" + s
}

// injectPreviewColCursor 在当前高亮预览行内，按 previewCol（该 buffer 行渲染后的
// 显示列）注入一个反色列光标块。行可能被软换行成多段，先定位列光标所在的视觉段
// 与段内列，再调用 injectPreviewColBlock 覆盖式注入（不改变行宽）。
// 返回注入后的行；若列光标不在此视觉段内，原样返回。
func (m *EditorModel) injectPreviewColCursor(lineOut string, row int) string {
	if m.previewCol < 0 {
		return lineOut
	}
	if row < 0 || row >= len(m.previewRowToBuffer) {
		return lineOut
	}
	brow := m.previewRowToBuffer[row]
	if brow < 0 || brow >= m.Buf.LineCount() || brow >= len(m.bufferToPreview) {
		return lineOut
	}
	width := m.contentWidth()
	if width < 20 {
		width = 20
	}
	rendered := m.Rend.RenderLine(m.Buf.GetLine(brow), true, m.sm.SearchKeyword)
	rlines := termd.WrapText(rendered, width)
	startRow := m.bufferToPreview[brow]
	seg := row - startRow
	if seg < 0 || seg >= len(rlines) {
		return lineOut
	}
	// 该视觉段之前所有段累计的显示宽度 = 软换行偏移；previewCol 减去后即段内列。
	base := 0
	for i := 0; i < seg; i++ {
		base += termd.FBDisplayWidth(stripANSIForWidth(rlines[i]))
	}
	local := m.previewCol - base
	segPlain := stripANSIForWidth(rlines[seg])
	if local < 0 || local >= termd.FBDisplayWidth(segPlain) {
		return lineOut // 列光标不在此视觉段内
	}
	// 预览/命令模式下 previewLines 无行号前缀，rlines[seg] 即 lineOut 本身。
	return injectPreviewColBlock(rlines[seg], local)
}

// injectPreviewColBlock 在含 ANSI 的字符串 s 的显示列 dispCol 处覆盖一个反色光标块。
// 覆盖式保持行宽不变：块宽等于被覆盖字符的显示宽度（宽字符 2 列）；光标落点在
// 字符内部（含首列）即覆盖该字符；到行尾则覆盖最后一个可见字符。块后 reset，
// 再原样续接剩余内容（各行渲染自带的 ANSI 序列不受破坏）。
func injectPreviewColBlock(s string, dispCol int) string {
	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("231")). // 近白背景（区别于行首蓝底块）
		Foreground(lipgloss.Color("16"))   // 黑色前景
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
		if termd.FBDisplayWidth(string(r)) >= 2 {
			w = 2
		}
		if dispCol < col+w {
			// 光标落在此字符上：字符前内容 + 反色块（覆盖字符）+ reset + 后续内容
			return string(runes[:i]) + cursorStyle.Render(string(r)) + "\x1b[0m" + string(runes[i+1:])
		}
		col += w
	}
	// 行尾：把最后一个可见字符覆盖为光标块（保持行宽，避免行被软换行撑高）。
	last := len(runes) - 1
	for last >= 0 {
		if runes[last] == 0x1b {
			// 跳过结尾的 ANSI 序列（如行尾的 \x1b[0m）
			j := last - 1
			for j >= 0 && !(runes[j] >= '@' && runes[j] <= '~') {
				j--
			}
			last = j - 1
			continue
		}
		break
	}
	if last < 0 {
		return cursorStyle.Render(" ")
	}
	return string(runes[:last]) + cursorStyle.Render(string(runes[last])) + "\x1b[0m" + string(runes[last+1:])
}

// handleDoubleKey 处理双击按键（如 dd, yy），返回 true 表示已处理完毕（第二次按键已确认）。
func (m *EditorModel) handleDoubleKey(key string) bool {
	if m.lastKey == key && time.Since(m.lastKeyTime) < 500*time.Millisecond {
		m.lastKey = ""
		return true
	}
	m.lastKey = key
	m.lastKeyTime = time.Now()
	return false
}

// deleteCurrentPreviewLine 删除预览模式下当前高亮行（仿 vim dd）。
func (m *EditorModel) deleteCurrentPreviewLine() {
	if m.Buf.LineCount() <= 1 {
		return // 至少保留一行
	}
	m.Buf.DeleteLine(m.previewCursor)
	if m.previewCursor >= m.Buf.LineCount() {
		m.previewCursor = m.Buf.LineCount() - 1
	}
	m.ensurePreviewCursorVisible()
}

// linkTargetAtDispCol 检查预览模式下指定 buffer 行和显示列位置是否有链接，
// 返回链接目标（URL、文件路径或锚点），无则返回空字符串。
func (m *EditorModel) linkTargetAtDispCol(row, dispCol int) string {
	if row < 0 || row >= m.Buf.LineCount() {
		return ""
	}
	line := string(m.Buf.GetLine(row))
	// 简化实现：查找行中的 Markdown 链接 [text](url)
	// 实际应按显示列精确匹配，这里返回行内第一个链接
	linkRE := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	matches := linkRE.FindAllStringSubmatchIndex(line, -1)
	for _, match := range matches {
		// match[0], match[1] = 整个匹配的起止
		// match[2], match[3] = 第一个捕获组 [text]
		// match[4], match[5] = 第二个捕获组 (url)
		if match[4] >= 0 && match[5] <= len(line) {
			url := line[match[4]:match[5]]
			// 简单的列位置检查：如果 dispCol 在链接文本范围内
			if dispCol >= match[2] && dispCol <= match[3] {
				return url
			}
		}
	}
	return ""
}

// openLink 打开链接：http(s) 用浏览器，本地路径用 xdg-open/open，锚点跳转。
func (m *EditorModel) openLink(target string) {
	// 先检查是否为文档内锚点链接
	if relPath, anchor, ok := splitAnchorTarget(target); ok {
		if relPath == "" {
			// 当前文档内跳转
			m.jumpToAnchor(anchor)
			return
		}
		// 其它 md 文件锚点：加载文件再跳转
		if err := m.loadFile(relPath); err == nil {
			m.jumpToAnchor(anchor)
		} else {
			m.status = termd.T("打开文件失败: ") + err.Error()
		}
		return
	}
	// 外部链接：用系统默认程序打开
	var cmd *exec.Cmd
	switch {
	case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
		cmd = exec.Command("xdg-open", target)
	case strings.HasPrefix(target, "file://"):
		cmd = exec.Command("xdg-open", strings.TrimPrefix(target, "file://"))
	default:
		// 本地文件路径
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		m.status = termd.T("打开链接失败: ") + err.Error()
	} else {
		m.status = termd.T("已打开: ") + target
	}
}
