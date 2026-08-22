package core

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"termd"

	"github.com/charmbracelet/lipgloss"
)

// ----------------------------------------------------------
// View 渲染
// ----------------------------------------------------------
func (m *EditorModel) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.ReportFocus = true // 启用焦点报告，支持 fcitx5 等输入法
	v.MouseMode = tea.MouseModeCellMotion // 启用鼠标单元格移动模式

	if m.fb != nil && m.fb.Opened {
		// 文件浏览器：两栏主体 + 底部状态栏
		body := m.fb.Render(m.width, m.height-3)
		switch m.fb.Mode {
		case termd.FBInput:
			r := []rune(m.fb.InputBuf)
			if m.fb.InputPos > len(r) {
				m.fb.InputPos = len(r)
			}
			head := string(r[:m.fb.InputPos])
			tail := string(r[m.fb.InputPos:])
			cursorBlock := lipgloss.NewStyle().Reverse(true).Render(" ")
			m.status = m.fb.InputLabel + head + cursorBlock + tail
		case termd.FBConfirm:
			m.status = termd.T("确认删除 '") + m.fb.PendingDelete + termd.T("'? [y/n]")
		default:
			m.status = termd.T("位置: ") + m.fb.Dir
		}
		content := m.frameLine("+", "+") + "\n" + body + "\n" +
			m.frameLine("+", "+") + "\n" + m.statusBar() + "\n" +
			m.frameLine("+", "+")
		v.SetContent(content)
		return v
	}

	if m.helpMode {
		v.SetContent(m.fixBottom(termd.RenderKeyMapHelp(), ""))
		return v
	}

	if m.cmdHelpMode {
		v.SetContent(m.fixBottom(termd.RenderCommandHelp(), ""))
		return v
	}

	if m.markdownLangMode {
		v.SetContent(m.renderMarkdownLang())
		return v
	}

	// 渲染可能因异常输入/终端环境而 panic；此处兜底，避免整个程序崩溃
	var rendered string
	func() {
		defer func() {
			if r := recover(); r != nil {
				m.status = "渲染异常（已恢复）：" + fmt.Sprintf("%v", r)
				rendered = m.fixBottom(m.fallbackView(), "")
			}
		}()
		var body strings.Builder
		switch m.sm.Mode() {
		case termd.ModePreview:
			s, _ := m.renderPreview()
			body.WriteString(s)
		case termd.ModeEdit:
			s := m.renderEdit()
			body.WriteString(s)
		case termd.ModeCommand:
			s, _ := m.renderPreview()
			body.WriteString(s)
		}
		cmdLine := ""
		if m.sm.Mode() == termd.ModeCommand {
			cmdLine = m.renderCmdLine()
		}
		bodyStr := body.String()
		if m.outlineMode && m.outlineWidth() > 0 {
			bodyStr = m.composeOutlineWithBody(bodyStr)
		}
		rendered = m.fixBottom(bodyStr, cmdLine)
	}()

	v.SetContent(rendered)

	// 光标形状映射
	cursorShape := tea.CursorShape(m.cursorShape)
	if cursorShape < tea.CursorBlock || cursorShape > tea.CursorBar {
		cursorShape = tea.CursorBlock
	}

	// 根据模式设置硬件光标，类似 Vim 的 guicursor 行为：
	// - Command 模式：硬件光标定位到命令行输入框
	// - Edit/Preview 模式：也显示硬件光标（由用户配置形状），蓝底块作为辅助指示
	cursorStyle := encodeCursorStyle(cursorShape, m.blinkMode)

	// 设置光标位置
	switch m.sm.Mode() {
	case termd.ModeCommand:
		row := m.height - 1
		if row < 1 {
			row = 1
		}
		col := 2 // 前缀 ":" + 空正文时的光标块位置
		input := m.sm.CmdInput
		if r := []rune(input); len(r) > 1 {
			col = 1 + runewidth.StringWidth(string(r[1:])) + 1
		}
		v.Cursor = tea.NewCursor(col-1, row-1)
		v.Cursor.Shape = cursorShape
		v.Cursor.Blink = m.blinkMode
	case termd.ModeEdit:
		// Edit 模式：显示硬件光标（配置的形状），位置由计算得出
		if cursor := m.computeEditCursorPos(); cursor != nil {
			cursor.Shape = cursorShape
			cursor.Blink = m.blinkMode
			v.Cursor = cursor
		}
	default:
		// Preview 模式：隐藏硬件光标（由蓝底块指示）
		v.Cursor = nil
	}

	// 确保光标形状转义序列被发送（即使 bubbletea 的 renderer 不重复发送）
	// 通过在 View 内容中注入 ANSI 序列实现
	if m.lastCursorStyle != cursorStyle {
		m.lastCursorStyle = cursorStyle
		// 注入到内容开头，确保每次渲染都发送
		rendered = ansi.SetCursorStyle(cursorStyle) + rendered
		v.SetContent(rendered)
	}

	return v
}

// fallbackView 在渲染 panic 兜底时返回一个尽量简单、不可能再 panic 的视图，
// 保证终端不会被破坏，用户仍可操作（切换模式/退出）。
func (m *EditorModel) fallbackView() string {
	var b strings.Builder
	top := m.scroll
	if m.sm.Mode() == termd.ModePreview || m.sm.Mode() == termd.ModeCommand {
		// Preview/Command 以预览光标行为视口锚点（纯文本兜底，无需视觉行映射）
		top = m.previewCursor
		if top < 0 {
			top = 0
		}
	}
	end := min(m.Buf.LineCount(), top+visibleLines(m))
	for i := top; i < end; i++ {
		b.WriteString(string(m.Buf.GetLine(i)))
		b.WriteString("\n")
	}
	for i := end; i < top+visibleLines(m); i++ {
		b.WriteString("~\n")
	}
	return b.String()
}

// fixBottom 将内容固定在窗口顶部，并把底部区域改造成“上下分明”的边框结构：
//
//	+----------------------------+   <- 顶线
//	[ 内容区 contentHeight 行 ]
//	+----------------------------+   <- 分隔线（状态栏与编辑区彻底分离）
//	[ 状态栏 ]
//	[ 命令行（仅命令模式）]
//	+----------------------------+   <- 底线
//
// 任何模式下输出都恰好铺满 height 行：顶线 + 内容区 + 分隔线 + 状态栏 + (命令行) + 底线，
// 杜绝“Edit 模式输出少 1 行、底部残留上一帧内容”导致的状态栏/内容重叠（乐高遮挡）问题。
func (m *EditorModel) fixBottom(body, cmdLine string) string {
	ch := m.contentHeight()
	lines := strings.Split(body, "\n")
	// 去掉 split 产生的尾部空切片干扰
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// 用空行补齐到内容区高度
	for len(lines) < ch {
		lines = append(lines, "")
	}
	// 截断超出行（理论上不会发生，因为渲染已限制）
	if len(lines) > ch {
		lines = lines[:ch]
	}
	var b strings.Builder
	b.WriteString(m.frameLine("+", "+"))
	b.WriteString("\n")
	b.WriteString(strings.Join(lines, "\n"))
	if len(lines) > 0 {
		b.WriteString("\n")
	}
	// 分隔线：内容区与状态栏彻底分离
	b.WriteString(m.frameLine("+", "+"))
	b.WriteString("\n")
	b.WriteString(m.statusBar())
	if cmdLine != "" {
		b.WriteString("\n" + cmdLine)
	}
	b.WriteString("\n")
	// 底线：封闭底部框
	b.WriteString(m.frameLine("+", "+"))
	return b.String()
}

// renderMarkdownLang 渲染 Markdown 语法教程视图：按 mlScroll 偏移切片教程文本，
// 内容不足一屏时夹紧滚动偏移，随后走 fixBottom 铺满边框布局。
func (m *EditorModel) renderMarkdownLang() string {
	lines := strings.Split(termd.RenderMarkdownLanguage(), "\n")
	// 去掉 split 产生的尾部空切片干扰
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	ch := m.contentHeight()
	maxScroll := max(0, len(lines)-ch)
	m.mlScroll = clamp(m.mlScroll, 0, maxScroll)
	end := min(len(lines), m.mlScroll+ch)
	return m.fixBottom(strings.Join(lines[m.mlScroll:end], "\n"), "")
}

// frameLine 渲染一条全宽边框线（+-----+），用于把底部状态栏区域与内容区彻底分隔。
// 使用纯 ASCII 的 + 和 -，避免 Unicode 盒线字符在中文 locale 下被按 2 列渲染而撑破行宽。
func (m *EditorModel) frameLine(l, r string) string {
	w := m.width
	if w < 3 {
		w = 3
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	return style.Render(l + strings.Repeat("-", w-2) + r)
}

// renderEdit 渲染 Edit 模式（WYSIWYG + 光标精确列对齐）。
//   - 非光标行：与 Preview 一致，富文本渲染（语法行着色）。
//   - 光标行：调用 RenderEditLineWithCursor，仅对语法前缀上色，光标用反显空格标记，
//     确保光标所在显示列与输入字符严格对齐，零输入延迟。
func (m *EditorModel) renderEdit() string {
	var b strings.Builder
	lines := m.Buf.LineCount()
	// 当前行背景（仿 vim cursorline）：普通态用偏蓝灰，插入态用偏暗的强调色
	cursorBg := lipgloss.Color("236")
	if m.sm.EditSub() == termd.EditNormal {
		cursorBg = lipgloss.Color("238")
	}
	cursorLineStyle := lipgloss.NewStyle().Background(cursorBg)
	// 续行/未编辑行的 gutter 占位（与 renderLineNum 的 "%4d " 列宽一致，均为 5 列）。
	// 无行号时必须为空串，否则续行被多塞 5 个空格，窄终端下整行超宽触发硬折行。
	gutterEdit := ""
	if m.sm.LineNumMode() != termd.LNNone {
		gutterEdit = strings.Repeat(" ", 5)
	}
	// 可用宽度：内容区宽度减去行号 gutter（无行号时 gutter 为 0，内容占满整行）
	availWidth := m.contentWidth()
	if m.sm.LineNumMode() != termd.LNNone {
		availWidth -= 5
	}
	if availWidth < 10 {
		availWidth = 10
	}
	// 让 renderer 知道可用宽度：分隔线/标题下划线等占满整行
	m.Rend.SetWrap(availWidth)
	ch := visibleLines(m)
	vLines := 0 // 已占用的视觉行数
	for i := m.scroll; i < lines; i++ {
		line := m.Buf.GetLine(i)
		// 代码块检测：```` ``` ```` 或 ~~~ 围栏起始行，收集到结束围栏整块送 glamour
		// 渲染（chroma 语法高亮 + 语言标注 + 代码背景），否则编辑模式下代码块内容行
		// 会被逐行当普通文本渲染，完全没有高亮（与 Preview 行为不一致）。
		if termd.IsFenceStart(strings.TrimSpace(string(line))) {
			j := i + 1
			for j < lines && !termd.IsFenceEnd(strings.TrimSpace(string(m.Buf.GetLine(j)))) {
				j++
			}
			if j >= lines {
				j = lines - 1 // 无结束围栏，渲染到文末
			}
			// 光标是否仍在该代码块范围内（含起始/结束围栏行）。
			// 只要光标没有离开最后一个 ```（即 cursorRow <= j），就保持原始 Markdown
			// 文本 + 灰底、暂不渲染，避免编辑过程中高亮/行对齐干扰输入；
			// 光标离开整个代码块后才整块送 glamour 高亮渲染。
			cursorInside := m.cursorRow >= i && m.cursorRow <= j
			if cursorInside {
				// 编辑中：逐行显示原始文本，光标行加反显光标块（保持列对齐），
				// 整块仍包成自适应宽度灰底矩形（与渲染后外观一致，避免编辑时形状跳变）。
				raw := make([]string, 0, j-i+1)
				for k := i; k <= j; k++ {
					brow := k
					var s string
					if brow == m.cursorRow {
						s = m.Rend.RenderEditLineWithCursor(m.Buf.GetLine(brow), m.cursorCol, false)
					} else {
						s = string(m.Buf.GetLine(brow))
					}
					raw = append(raw, s)
				}
				firstP, lidP := "", gutterEdit
				if ln := m.sm.LineNumMode(); ln != termd.LNNone {
					firstP = renderLineNum(ln, i, m.cursorRow)
				}
				rect := renderCodeRect(raw, raw, firstP, lidP, availWidth-1)
				for _, rl := range rect {
					if vLines+1 > ch {
						break
					}
					b.WriteString(rl + "\n")
					vLines++
				}
				i = j // 跳过块内其余行
				continue
			}
			// 光标已离开：chroma 直接高亮代码（不走 glamour 的伪灰底），再套自适应灰底矩形。
			block := []string{}
			for k := i; k <= j; k++ {
				block = append(block, string(m.Buf.GetLine(k)))
			}
			rawInner := block[1 : len(block)-1] // 去掉首尾围栏行
			fenceStart := strings.TrimSpace(block[0])
			lang := strings.TrimPrefix(fenceStart, "```")
			lang = strings.TrimPrefix(lang, "~~~")
			code := strings.Join(rawInner, "\n")
			availW := availWidth - 1
			var rb []string
			widthRef := rawInner
			if termd.IsMermaidFence(lang) {
				// Mermaid 代码块：渲染为 ANSI 字符画；不支持的图类型降级为 chroma 高亮。
				if ml, ok := termd.RenderMermaid(code, availW); ok {
					rb = ml
					widthRef = ml
				} else {
					rb = highlightCode(lang, code)
				}
			} else {
				rb = highlightCode(lang, code)
			}
			// 自适应宽度的完整灰底矩形：首行带行号 gutter，其余行用 gutterEdit 对齐，
			// 上/下盖勾出轮廓，右侧按最宽行补齐，形成闭合矩形而非铺满整行。
			firstP, lidP := "", gutterEdit
			if ln := m.sm.LineNumMode(); ln != termd.LNNone {
				firstP = renderLineNum(ln, i, m.cursorRow)
			}
			rect := renderCodeRect(rb, widthRef, firstP, lidP, availW)
			for _, rl := range rect {
				if vLines+1 > ch {
					break
				}
				b.WriteString(rl + "\n")
				vLines++
			}
			i = j // 跳过块内其余行，下一轮从结束围栏之后开始
			continue
		}
		// 表格块检测：表头行(含 |) + 下一行分隔行(|---|) + 后续连续含 | 的数据行。
		// 表格是块级语法，需整块送 glamour 渲染（对齐线/列分隔/表头边框），
		// 不能逐行近似，否则与原始文本无异。
		// 表格块检测：表头行(含 |) + 下一行分隔行(|---|) + 后续连续含 | 的数据行。
		// 采用与代码块完全相同的矩形逻辑：光标在块内逐行显示原始文本（编辑不跳变），
		// 光标离开后整块 glamour 渲染，并套自适应宽度灰底矩形（renderCodeRect）。
		if i+1 < lines && termd.IsTableStart(string(line), string(m.Buf.GetLine(i+1))) {
			tbl := []string{string(line), string(m.Buf.GetLine(i + 1))}
			j := i + 2
			for j < lines {
				r2 := strings.TrimSpace(string(m.Buf.GetLine(j)))
				if r2 != "" && strings.Contains(r2, "|") {
					tbl = append(tbl, string(m.Buf.GetLine(j)))
					j++
				} else {
					break
				}
			}
			cursorInside := m.cursorRow >= i && m.cursorRow < j
			firstP, lidP := "", gutterEdit
			if ln := m.sm.LineNumMode(); ln != termd.LNNone {
				firstP = renderLineNum(ln, i, m.cursorRow)
			}
			if cursorInside {
				// 编辑中：逐行显示原始文本，光标行加反显光标块（保持列对齐），
				// 整块仍包成自适应宽度灰底矩形（与渲染后外观一致，避免编辑时形状跳变）。
				raw := make([]string, 0, j-i)
				for k := i; k < j; k++ {
					brow := k
					var s string
					if brow == m.cursorRow {
						s = m.Rend.RenderEditLineWithCursor(m.Buf.GetLine(brow), m.cursorCol, false)
					} else {
						s = string(m.Buf.GetLine(brow))
					}
					raw = append(raw, s)
				}
				rect := renderCodeRect(raw, raw, firstP, lidP, availWidth-1)
				for _, rl := range rect {
					if vLines+1 > ch {
						break
					}
					b.WriteString(rl + "\n")
					vLines++
				}
				i = j - 1
				continue
			}
			// 光标已离开：glamour 渲染表格，再套自适应宽度灰底矩形。
			block := strings.Join(tbl, "\n")
			rendered := m.Rend.RenderBlock(block, availWidth)
			rlines := strings.Split(rendered, "\n")
			// 矩形宽度以 glamour 输出（去 ANSI）为基准：表格渲染后含 │ 边框与列 padding，
			// 显示宽度与原始 `|` 行不同，不能像代码块那样以原始行测宽。
			widthRef := make([]string, 0, len(rlines))
			for _, rl := range rlines {
				widthRef = append(widthRef, stripANSIForWidth(rl))
			}
			rect := renderCodeRect(rlines, widthRef, firstP, lidP, availWidth-1)
			for _, rl := range rect {
				if vLines+1 > ch {
					break
				}
				b.WriteString(rl + "\n")
				vLines++
			}
			// 越界检查（确保不覆盖状态栏/命令行）
			if vLines >= ch {
				break
			}
			i = j - 1 // 跳过块内其余行，下一轮从该块之后开始
			continue
		}
		// 多行注释块检测：%% 起始行，收集到结束 %%（含）或文末。
		// 注释即"不参与渲染"的文本：与代码块/表格同一"双态"机制——
		// 光标在块内（编辑中）逐行显示原始文本（含 %% 标记，反显光标块），
		// 光标离开后才以注释样式（暗灰斜体）渲染（%% 标记剥离）。
		// 整块套自适应宽度灰底矩形，但用 renderCommentRect（无上下盖）：
		// 注释块内容行少，有盖时上下各占一行纯灰条浪费空间。
		if termd.IsCommentStart(strings.TrimSpace(string(line))) {
			j := i + 1
			for j < lines && !termd.IsCommentEnd(strings.TrimSpace(string(m.Buf.GetLine(j)))) {
				j++
			}
			if j >= lines {
				j = lines - 1 // 无结束 %%，渲染到文末
			}
			cursorInside := m.cursorRow >= i && m.cursorRow <= j
			firstP, lidP := "", gutterEdit
			if ln := m.sm.LineNumMode(); ln != termd.LNNone {
				firstP = renderLineNum(ln, i, m.cursorRow)
			}
			var rect []string
			if cursorInside {
				// 编辑中：逐行显示原始文本，光标行加反显光标块（保持列对齐），
				// 整块仍包灰底矩形（与渲染后外观一致，避免编辑时形状跳变）。
				raw := make([]string, 0, j-i+1)
				for k := i; k <= j; k++ {
					brow := k
					var s string
					if brow == m.cursorRow {
						s = m.Rend.RenderEditLineWithCursor(m.Buf.GetLine(brow), m.cursorCol, false)
					} else {
						s = string(m.Buf.GetLine(brow))
					}
					raw = append(raw, s)
				}
				rect = renderCommentRect(raw, raw, firstP, lidP, availWidth-1)
			} else {
				// 光标离开：注释样式（暗灰斜体）渲染，%% 标记剥离。
				rendered := make([]string, 0, j-i+1)
				widthRef := make([]string, 0, j-i+1)
				for k := i; k <= j; k++ {
					brow := k
					raw := string(m.Buf.GetLine(brow))
					// 剥离 %% 标记符号（起始行行首与结束行整行），使注释标记不参与显示
					content, _ := termd.StripCommentMarkers(raw, brow == i,
						termd.IsCommentEnd(strings.TrimSpace(raw)))
					widthRef = append(widthRef, content)
					rendered = append(rendered, termd.CommentStyle().Render(content))
				}
				rect = renderCommentRect(rendered, widthRef, firstP, lidP, availWidth-1)
			}
			for _, rl := range rect {
				if vLines+1 > ch {
					break
				}
				b.WriteString(rl + "\n")
				vLines++
			}
			i = j // 跳过块内其余行，下一轮从结束 %% 之后开始
			continue
		}
		var rendered string
		if i == m.cursorRow {
			// 整行闭合单行注释（%%xxx%%）：光标编辑时显示全部纯文本（不解析注释样式、
			// 不隐藏标记），保证输入所见即所得；光标离开后由 RenderLine→RenderInline
			// 渲染为灰色注释。
			if termd.IsWholeLineComment(string(line)) {
				rendered = m.Rend.RenderEditLineWithCursor(line, m.cursorCol, false)
			} else {
				// 光标行：富文本 + 行内标记着色 + 反显光标块（保持行宽、光标列严格对齐），
				// 使进入编辑模式后 markdown 语法立即“渲染”出来，不再呈现纯原文。
				rendered = m.Rend.RenderEditLineWithCursorStyled(line, m.cursorCol, false)
			}
			rendered = cursorLineStyle.Render(rendered)
		} else {
			// 非光标行：与 Preview 一致富文本
			rendered = m.Rend.RenderLine(line, true, m.sm.SearchKeyword)
		}
		// 软换行到可用宽度，使编辑内容占满终端宽度
		wrapped := termd.WrapText(rendered, availWidth)
		// 若加入本行会越过状态栏所在行，且本行不是光标行，则停止。
		// 光标行始终完整渲染，确保其全部换行都落在状态栏之上，不被覆盖。
		if vLines+len(wrapped) > ch && i != m.cursorRow {
			break
		}
		// 行号前缀（仅首行显示，续行用空白占位，保持标题下划线等与行号列对齐）
		numPrefix := ""
		if ln := m.sm.LineNumMode(); ln != termd.LNNone {
			num := renderLineNum(ln, i, m.cursorRow)
			if i == m.cursorRow {
				num = cursorLineStyle.Render(num)
			}
			numPrefix = num
		}
		// wrapped 可能含续行（含软换行与标题下划线），逐行写入并对续行补 gutter 占位
		for k, p := range wrapped {
			if k == 0 {
				b.WriteString(numPrefix + p + "\n")
			} else {
				b.WriteString(gutterEdit + p + "\n")
			}
		}
		vLines += len(wrapped)
		if vLines >= ch {
			break
		}
	}
	// 文件末尾之后的空白行：用 vim 风格的 '~' 标记未编辑区域。
	// 仿 vim 放在最左列（第 1 列），不占用行号 gutter，紧贴边缘。
	tildeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	for vLines < ch {
		b.WriteString(tildeStyle.Render("~") + "\n")
		vLines++
	}
	return b.String()
}

// encodeCursorStyle 将光标形状和闪烁状态编码为 DECSCUSR 参数值。
// 与 bubbletea v2 的 encodeCursorStyle 保持一致：
//   - CursorBlock (0): blink=1, steady=2
//   - CursorUnderline (1): blink=3, steady=4
//   - CursorBar (2): blink=5, steady=6
func encodeCursorStyle(shape tea.CursorShape, blink bool) int {
	style := int(shape)*2 + 1
	if !blink {
		style++
	}
	return style
}

// computeEditCursorPos 计算 Edit 模式下光标在屏幕上的位置（0-based）。
// 返回 *tea.Cursor，包含位置、形状、颜色和闪烁设置。
// 供 fcitx5 等输入法跟随光标位置显示候选窗口。
func (m *EditorModel) computeEditCursorPos() *tea.Cursor {
	if m.cursorRow < 0 || m.cursorRow >= m.Buf.LineCount() {
		return nil
	}

	ch := m.contentHeight()
	availWidth := m.contentWidth()
	gutterWidth := 0
	if m.sm.LineNumMode() != termd.LNNone {
		availWidth -= 5
		gutterWidth = 5
	}
	if availWidth < 10 {
		availWidth = 10
	}

	// 计算光标行之前的视觉行数
	vLines := 0
	lines := m.Buf.LineCount()
	for i := m.scroll; i < m.cursorRow && i < lines; i++ {
		line := m.Buf.GetLine(i)
		// 使用与 renderEdit 相同的渲染逻辑
		var rendered string
		if termd.IsFenceStart(strings.TrimSpace(string(line))) {
			// 代码块：原始文本，每行 1 视觉行
			vLines++
		} else if i+1 < lines && termd.IsTableStart(string(line), string(m.Buf.GetLine(i+1))) {
			// 表格：跳过整个表格块
			j := i + 2
			for j < lines {
				r2 := strings.TrimSpace(string(m.Buf.GetLine(j)))
				if r2 != "" && strings.Contains(r2, "|") {
					j++
				} else {
					break
				}
			}
			// 表格按渲染行数计算，简化为 buffer 行数
			vLines += (j - i)
			i = j - 1
		} else if termd.IsCommentStart(strings.TrimSpace(string(line))) {
			// 注释块：每行 1 视觉行
			vLines++
		} else {
			rendered = m.Rend.RenderLine(line, true, m.sm.SearchKeyword)
			wrapped := termd.WrapText(rendered, availWidth)
			vLines += len(wrapped)
		}
		if vLines >= ch {
			break
		}
	}

	if vLines >= ch {
		return nil // 光标行不在可见区域内
	}

	// 计算光标行内的光标列位置（显示列）
	line := m.Buf.GetLine(m.cursorRow)
	// 找到光标在 buffer 中的字节位置
	byteCol := runeColToByte(line, m.cursorCol)
	
	// 计算光标前所有字符的显示宽度（光标位于字符之前）
	cursorPrefix := string(line[:byteCol])
	// 渲染光标前的部分来计算显示宽度
	prefixRendered := m.Rend.RenderEditLineWithCursorStyled([]byte(cursorPrefix), len([]rune(cursorPrefix)), false)
	prefixPlain := stripANSIForWidth(prefixRendered)
	dispCol := runewidth.StringWidth(prefixPlain)
	
	col := gutterWidth + dispCol

	// 布局结构（1-based 行号）：
	// 1: 顶边框
	// 2: 第1个内容行
	// ...
	// ch+1: 最后1个内容行
	// 转为 0-based：顶边框 Y=0，第一内容行 Y=1
	row := 1 + vLines // 0-based: 顶边框占 Y=0，内容从 Y=1 开始

	return &tea.Cursor{
		Position: tea.Position{X: col, Y: row}, // col 已是 0-based (左边框 X=0，内容从 X=1)
		Shape:    tea.CursorBlock,
		Blink:    m.blinkMode,
	}
}
