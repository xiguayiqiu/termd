package core

import (
	"fmt"
	"strings"
	"termd"

	"github.com/charmbracelet/lipgloss"
)

// ----------------------------------------------------------
// View 渲染
// ----------------------------------------------------------
func (m *EditorModel) View() string {
	if m.fb != nil && m.fb.Opened {
		// 文件浏览器：两栏主体 + 底部状态栏（位置/输入提示写入状态栏，避免行数叠加错乱）。
		// 强隔离：传 height-3 让 Render 内部产出 height-4 行主体（它自行预留状态栏 1 行），
		// 再由本分支拼接 顶线+分隔线+状态栏+底线，与其他视图保持一致的“上下分明”边框布局；
		// 完全不依赖 contentHeight()（后者会随 Preview/Command 模式变化，曾导致两栏布局被预览状态干扰）。
		body := m.fb.Render(m.width, m.height-3)
		switch m.fb.Mode {
		case termd.FBInput:
			// 输入名称：在状态栏显示 label + 带反显光标块的输入缓冲
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
		// 强制输出完整 height 行以全量覆盖（杜绝底部残留上一帧内容导致「两栏混合」）。
		// 顶线 + 主体 + 分隔线 + 状态栏 + 底线，与主界面“上下分明”的边框布局保持一致。
		// 末尾追加滚动区恢复保险：即使滚动帧的 DECSTBM 因异常未恢复，下一次渲染也强制恢复。
		return m.frameLine("+", "+") + "\n" + body + "\n" +
			m.frameLine("+", "+") + "\n" + m.statusBar() + "\n" +
			m.frameLine("+", "+") + "\x1b[r"
	}

	if m.helpMode {
		// 键位帮助视图：渲染集中注册的键位表（keymap.go），状态栏贴底
		return m.fixBottom(termd.RenderKeyMapHelp(), "") + "\x1b[r"
	}

	if m.cmdHelpMode {
		// 命令模式命令帮助视图：渲染所有命令及用法（keymap.go），状态栏贴底
		return m.fixBottom(termd.RenderCommandHelp(), "") + "\x1b[r"
	}

	if m.markdownLangMode {
		// Markdown 语法教程视图：渲染 termd.RenderMarkdownLanguage()（MarkdownLanguage.go），
		// 支持 j/k 与滚轮翻页（mlScroll 偏移）。
		return m.renderMarkdownLang() + "\x1b[r"
	}

	// 渲染可能因异常输入/终端环境而 panic；此处兜底，避免整个程序崩溃
	// （bubbletea 的 recover 会把 panic 直接暴露给用户并结束程序）。
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
		// 命令模式下，命令行内容（底部第二行）；否则为空
		cmdLine := ""
		if m.sm.Mode() == termd.ModeCommand {
			cmdLine = m.renderCmdLine()
		}
		bodyStr := body.String()
		// 大纲侧边栏打开时，把大纲列与正文逐行并排（总宽 = 终端宽，状态栏仍贴底）
		if m.outlineMode && m.outlineWidth() > 0 {
			bodyStr = m.composeOutlineWithBody(bodyStr)
		}
		rendered = m.fixBottom(bodyStr, cmdLine)
		// 光标定位策略：
		//  - 编辑/预览模式：光标位置已由 renderEdit/renderPreview 注入的蓝底块指示，
		//    隐藏硬件光标避免双光标重叠。
		//  - 命令模式：硬件光标定位到命令行输入框（前缀后紧跟输入位置，仿 vim），
		//    不再定位到预览当前行——命令模式不移动当前行，定位过去只会造成光标错位。
	}()

	// 末尾追加滚动区恢复保险：任何情况下都恢复全屏滚动区，防止滚动帧异常残留
	// DECSTBM 导致后续渲染持续触发终端滚动（界面逐步上移/消失）。
	// 注意：DECSTBM（\x1b[r）在多数终端会把光标移动到 home position（左上角），
	// 因此必须先恢复滚动区、再输出光标控制序列；否则命令模式的光标定位会被
	// \x1b[r 覆盖——光标跳到左上角被顶线遮挡，表现为「命令模式下不显示光标」。
	restoreScroll := "\x1b[r"
	switch m.sm.Mode() {
	case termd.ModeCommand:
		rendered += restoreScroll + m.cmdlineCursorGoto()
	default:
		rendered += restoreScroll + hideCursor()
	}
	return rendered
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
						s = m.Rend.RenderEditLineWithCursor(m.Buf.GetLine(brow), m.cursorCol)
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
						s = m.Rend.RenderEditLineWithCursor(m.Buf.GetLine(brow), m.cursorCol)
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
						s = m.Rend.RenderEditLineWithCursor(m.Buf.GetLine(brow), m.cursorCol)
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
				rendered = m.Rend.RenderEditLineWithCursor(line, m.cursorCol)
			} else {
				// 光标行：富文本 + 行内标记着色 + 反显光标块（保持行宽、光标列严格对齐），
				// 使进入编辑模式后 markdown 语法立即“渲染”出来，不再呈现纯原文。
				rendered = m.Rend.RenderEditLineWithCursorStyled(line, m.cursorCol)
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
