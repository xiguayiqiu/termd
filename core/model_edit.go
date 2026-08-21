package core

import (
	"strings"
	"termd"

	"github.com/charmbracelet/bubbletea"
)

// ----------------------------------------------------------
// Edit 模式按键处理
// ----------------------------------------------------------
func (m *EditorModel) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 大纲侧边栏打开时，导航按键优先交给大纲处理（未消费则继续常规逻辑）
	if m.outlineMode {
		if m2, c, handled := m.handleOutlineKey(msg); handled {
			return m2, c
		}
	}
	// ctrl+t 切换大纲（keymap: edit.outline）
	if msg.String() == "ctrl+t" {
		m.toggleOutline()
		return m, nil
	}
	// ctrl+o 打开文件浏览器（keymap: edit.fileBrowser）
	if msg.String() == "ctrl+o" {
		m.openFileBrowser()
		return m, nil
	}
	// Esc 语义（仿 vim + 用户需求“Esc 在 编辑↔预览 之间切换”）：
	//   - INSERT 态：退回 NORMAL 导航态（仿 vim，README 键位表约定）；
	//   - NORMAL 态：返回预览模式（用户需求）。
	if msg.Type == tea.KeyEsc {
		if m.sm.EditSub() != termd.EditNormal {
			m.sm.SetEditSub(termd.EditNormal)
			m.status = termd.T("NORMAL（Esc 退出编辑）")
			return m, nil
		}
		m.sm.ExitToPreview()
		// 同步预览高亮行到当前编辑行，保证回到预览时光标位置连续
		m.previewCursor = clamp(m.cursorRow, 0, m.Buf.LineCount()-1)
		m.ensurePreviewCursorVisible()
		m.status = termd.T("已退出编辑（Esc）")
		return m, tea.HideCursor
	}
	// 输入法（fcitx5）激活时，普通字母按键会被 normal 子态的字母快捷键（i/a/j/k…）
	// 误吞，造成“随机插入乱序”内容。此处把所有普通字符直接转入插入态写入文本，
	// 仅在 IME 激活期间生效，不影响无输入法时的正常 vim 风格快捷键。
	if m.imeActive && msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.sm.SetEditSub(termd.EditInsert)
		return m.handleInsertKey(msg)
	}
	// 粘贴（bracketed paste）整段文本时同样自动转入插入态写入：
	// 粘贴是明确的“插入文本”意图，不应被 NORMAL 态的字母快捷键（i/a/j/k…）误吞。
	if msg.Paste && msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.sm.SetEditSub(termd.EditInsert)
		return m.handleInsertKey(msg)
	}
	// 编辑模式内部再分 插入态(termd.EditInsert) 与 普通导航态(termd.EditNormal)（仿 vim INSERT/NORMAL）。
	if m.sm.EditSub() == termd.EditInsert {
		return m.handleInsertKey(msg)
	}
	return m.handleNormalKey(msg)
}

// openFileBrowser 打开文件浏览器（Ctrl+O 快捷键，与 :ex 命令等价；
// 若已存在实例则复用，保留上次浏览目录）。
func (m *EditorModel) openFileBrowser() {
	if m.fb == nil {
		m.fb = termd.NewFileBrowser(".")
	}
	m.fb.Open()
	m.status = termd.T("文件浏览器已打开")
}

// ----------------------------------------------------------
// 编辑-插入态：键入即写入（与旧 Edit 行为一致）
// ----------------------------------------------------------
func (m *EditorModel) handleInsertKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	line := m.Buf.GetLine(m.cursorRow)
	byteCol := runeColToByte(line, m.cursorCol)

	switch msg.Type {
	case tea.KeyRunes:
		// 粘贴文本（bracketed paste）整体以 KeyRunes 送达，其中的 \n / \r 不会被
		// bubbletea 翻译成 KeyEnter，必须在此识别为真正的换行，否则多行粘贴会
		// 全部挤进当前行（表现为“无法保持原格式、始终单行”）。
		prevCR := false
		for _, r := range msg.Runes {
			if prevCR {
				prevCR = false
				if r == '\n' {
					continue // \r\n 已作为一次换行处理
				}
			}
			switch r {
			case '\n', '\r': // 换行：拆分当前行，光标移到新行行首
				nr, nc := m.Buf.InsertNewline(m.cursorRow, byteCol)
				m.cursorRow, m.cursorCol = nr, nc
				if r == '\r' {
					prevCR = true
				}
			default:
				m.Buf.InsertRune(m.cursorRow, byteCol, r)
				// 先推进光标列，再据此重算插入点字节偏移：否则下一轮会用“旧光标列”
				// 计算出的字节偏移（仍指向行首），把后续字符插到前一个字符之前，
				// 表现为中文等多 rune 提交时下标乱序（如“你好”变成“好你”）。
				m.cursorCol++
			}
			line = m.Buf.GetLine(m.cursorRow)
			byteCol = runeColToByte(line, m.cursorCol)
		}
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeySpace:
		m.Buf.InsertRune(m.cursorRow, byteCol, ' ')
		m.cursorCol++
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeyTab:
		// Tab 缩进：在光标处插入一个缩进单位（默认 2 空格，与渲染嵌套层级一致）
		for i := 0; i < TabSize; i++ {
			m.Buf.InsertRune(m.cursorRow, byteCol, ' ')
			m.cursorCol++
			byteCol++
		}
		m.cursWant = m.cursorCol
		m.status = termd.T("已插入缩进")
		m.touch()
	case tea.KeyShiftTab:
		// Shift+Tab 反缩进：从光标向左删除最多 TabSize 个空格（越行首则不删）
		removed := 0
		for removed < TabSize && m.cursorCol > 0 {
			line := m.Buf.GetLine(m.cursorRow)
			bc := runeColToByte(line, m.cursorCol-1)
			if bc < len(line) && line[bc] == ' ' {
				m.Buf.DeleteRune(m.cursorRow, bc)
				m.cursorCol--
				removed++
			} else {
				break
			}
		}
		m.cursWant = m.cursorCol
		if removed > 0 {
			m.status = termd.T("已反缩进")
			m.touch()
		}

	case tea.KeyEnter:
		nr, nc := m.Buf.InsertNewline(m.cursorRow, byteCol)
		m.cursorRow, m.cursorCol = nr, nc
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeyBackspace, tea.KeyDelete:
		if m.cursorCol > 0 {
			m.Buf.Backspace(m.cursorRow, byteCol)
			m.cursorCol--
		} else if m.cursorRow > 0 {
			// 行首退格：合并上一行
			prevLen := byteToRuneCol(m.Buf.GetLine(m.cursorRow-1), len(m.Buf.GetLine(m.cursorRow-1)))
			m.Buf.Backspace(m.cursorRow, byteCol)
			m.cursorRow--
			m.cursorCol = prevLen
		}
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeyLeft:
		if m.cursorCol > 0 {
			m.cursorCol--
		} else if m.cursorRow > 0 {
			m.cursorRow--
			m.cursorCol = byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
		}
		m.cursWant = m.cursorCol
	case tea.KeyRight:
		if line != nil && m.cursorCol < byteToRuneCol(line, len(line)) {
			m.cursorCol++
		} else if m.cursorRow < m.Buf.LineCount()-1 {
			m.cursorRow++
			m.cursorCol = 0
		}
		m.cursWant = m.cursorCol
	case tea.KeyUp:
		if m.cursorRow > 0 {
			m.cursorRow--
			m.colKeep()
		}
	case tea.KeyDown:
		if m.cursorRow < m.Buf.LineCount()-1 {
			m.cursorRow++
			m.colKeep()
		}
	case tea.KeyHome:
		m.cursorCol = 0
		m.cursWant = 0
	case tea.KeyEnd:
		m.cursorCol = byteToRuneCol(line, len(line))
		m.cursWant = m.cursorCol
	}
	m.ensureCursorVisible()
	return m, nil
}

// ----------------------------------------------------------
// 编辑-普通导航态：仿 vim Normal 模式（h/j/k/l/w/b/e/0/^/$/gg/G/{}/()/Ctrl+D/U + 计数）
// ----------------------------------------------------------
func (m *EditorModel) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 计数前缀：数字键累积 pendingCount（如 "3" 在 "3j" 中）
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		c := msg.Runes[0]
		if c >= '0' && c <= '9' {
			if m.pendingCount == 0 && c == '0' {
				// 单独的 '0' 是“跳到行首”，不作为计数前缀
				m.pendingG = false
				m.moveToLineStart()
				return m, nil
			}
			m.pendingCount = m.pendingCount*10 + int(c-'0')
			m.pendingG = false
			return m, nil
		}
	}

	count := m.pendingCount
	if count <= 0 {
		count = 1
	}

	// g 组合键：gj / gk（视觉行/屏幕行移动，仿 vim：j/k 按 buffer 行，gj/gk 按软换行后的屏幕行）
	if m.pendingG {
		switch msg.String() {
		case "j", termd.KeyDown: // keymap: normal.down（组合态：gj 下移一个屏幕行）
			for c := 0; c < count && m.visualLineStep(1); c++ {
			}
			m.pendingG = false
			m.pendingCount = 0
			m.ensureCursorVisible()
			return m, nil
		case "k", termd.KeyUp: // keymap: normal.up（组合态：gk 上移一个屏幕行）
			for c := 0; c < count && m.visualLineStep(-1); c++ {
			}
			m.pendingG = false
			m.pendingCount = 0
			m.ensureCursorVisible()
			return m, nil
		default:
			m.pendingG = false // 无效组合，放弃等待
		}
	}

	switch msg.String() {
	// ---- 回到插入态 ----
	case "i":
		m.enterInsertAt(m.cursorCol) // 光标处插入
		return m, nil
	case "a":
		m.enterInsertAt(m.cursorCol + 1) // 光标后插入
		return m, nil
	case "I":
		// 行首第一个非空白后插入
		col := firstNonBlank(m.Buf.GetLine(m.cursorRow))
		m.enterInsertAt(col)
		return m, nil
	case "A":
		// 行尾插入
		col := byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
		m.enterInsertAt(col)
		return m, nil
	case "o":
		// 在下方新建一行并插入（仿 vim 'o'）
		nr := m.Buf.OpenLineBelow(m.cursorRow)
		m.cursorRow = nr
		m.cursorCol = 0
		m.enterInsertAt(0)
		m.touch()
		return m, nil
	case "O":
		// 在上方新建一行并插入（仿 vim 'O'）
		nr := m.Buf.OpenLineAbove(m.cursorRow)
		m.cursorRow = nr
		m.cursorCol = 0
		m.enterInsertAt(0)
		m.touch()
		return m, nil

	// ---- 删除（仿 vim d + motion 的简化版：逐字符/行删除）----
	case "x", "delete", "dl":
		m.deleteForward(count)
		return m, nil
	case "X", "dh":
		m.deleteBackward(count)
		return m, nil
	case "dw":
		m.deleteWord(count)
		return m, nil
	case "dd":
		m.deleteLine(count)
		return m, nil

	// ---- 撤销 ----
	case "u":
		if m.Buf.Undo() {
			m.status = termd.T("已撤销")
			m.touch() // 撤销改内容：Preview/滚动缓存失效并同步 swap（与其它编辑路径一致）
		} else {
			m.status = termd.T("无可撤销操作")
		}
		m.clampCursor() // 撤销可能收缩行内容，先把光标钳制到合法范围（仿 vim check_cursor）
		m.cursWant = m.cursorCol
		m.ensureCursorVisible()
		return m, nil

	// ---- 导航 ----
	case "h", termd.KeyLeft: // keymap: normal.left
		m.cursorCol = max(0, m.cursorCol-count)
		m.cursWant = m.cursorCol
	case "l", termd.KeyRight, termd.KeySpace: // keymap: normal.right
		maxc := byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
		m.cursorCol = min(maxc, m.cursorCol+count)
		m.cursWant = m.cursorCol
	case "k", termd.KeyUp: // keymap: normal.up
		m.cursorRow = max(0, m.cursorRow-count)
		m.colKeep()
	case "j", termd.KeyDown: // keymap: normal.down
		m.cursorRow = min(m.Buf.LineCount()-1, m.cursorRow+count)
		m.colKeep()
	case termd.KeyEnter: // 回车：光标停在超链接上则跳转（URL / 本地 md 文件），否则下移（keymap: normal.down）
		if t := m.linkTargetAtCursorEdit(); t != "" {
			m.openLink(t)
		} else {
			m.cursorRow = min(m.Buf.LineCount()-1, m.cursorRow+count)
			m.colKeep()
		}
	case "w":
		m.moveWord(count, false)
	case "W":
		m.moveWord(count, true)
	case "b":
		m.moveBackWord(count, false)
	case "B":
		m.moveBackWord(count, true)
	case "e":
		m.moveWordEnd(count, false)
	case "E":
		m.moveWordEnd(count, true)
	case "^":
		m.moveToFirstNonBlank()
	case "$":
		m.moveToLineEnd()
	case "g":
		// 进入 g 组合等待态（下一个 j/k 触发视觉行移动）
		m.pendingG = true
		return m, nil
	case "gg":
		m.cursorRow = max(0, count-1)
		m.colKeep()
	case "G":
		m.cursorRow = m.Buf.LineCount() - 1
		m.colKeep()
	case "{":
		m.moveParagraph(-count)
	case "}":
		m.moveParagraph(count)
	case "(":
		m.moveSentence(-count)
	case ")":
		m.moveSentence(count)
	case termd.KeyCtrlD: // keymap: normal.halfDown
		m.scrollHalfPage(count)
	case termd.KeyCtrlU: // keymap: normal.halfUp
		m.scrollHalfPage(-count)
	case termd.KeyCtrlF: // keymap: normal.pageDown
		m.scrollHalfPageVisible(1)
	case termd.KeyCtrlB: // keymap: normal.pageUp
		m.scrollHalfPageVisible(-1)
	case "zz":
		m.centerCursor()
	case "zt":
		m.topCursor()
	case "zb":
		m.bottomCursor()
	}

	// 清除计数前缀（已消费）
	m.pendingCount = 0
	m.ensureCursorVisible()
	return m, nil
}

// enterInsertAt 在指定 rune 列处进入插入态（钳制到行内有效范围）。
func (m *EditorModel) enterInsertAt(col int) {
	maxc := byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
	m.cursorCol = clamp(col, 0, maxc)
	m.cursWant = m.cursorCol
	m.sm.SetEditSub(termd.EditInsert)
	m.status = termd.T("INSERT（Esc 回到 NORMAL）")
}

// colKeep 垂直移动后按虚拟列 cursWant 维持列位置（仿 vim coladvance）。
func (m *EditorModel) colKeep() {
	maxc := byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
	if m.cursWant > maxc {
		m.cursorCol = maxc
	} else {
		m.cursorCol = m.cursWant
	}
}

// moveToLineStart 跳到行首（'0'）。
func (m *EditorModel) moveToLineStart() {
	m.cursorCol = 0
	m.cursWant = 0
}

// moveToFirstNonBlank 跳到行首第一个非空白（'^'）。
func (m *EditorModel) moveToFirstNonBlank() {
	m.cursorCol = firstNonBlank(m.Buf.GetLine(m.cursorRow))
	m.cursWant = m.cursorCol
}

// moveToLineEnd 跳到行尾（'$'）。
func (m *EditorModel) moveToLineEnd() {
	m.cursorCol = byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
	m.cursWant = m.cursorCol
}

// editAvailWidth 返回 Edit 模式下内容区可用列宽（与 renderEdit / ensureCursorVisible 的
// 软换行逻辑保持一致：行号 gutter 占 5 列）。
func (m *EditorModel) editAvailWidth() int {
	av := m.contentWidth()
	if m.sm.LineNumMode() != termd.LNNone {
		av -= 5
	}
	if av < 10 {
		av = 10
	}
	return av
}

// screenPos 返回当前光标所在 buffer 行的“视觉行”信息（仿 vim 的 w_wrow/w_wcol，
// 用于 gj/gk 的屏幕行移动）：
//   - wrapped：该行软换行后的视觉行列表（termd.WrapText 输出）；
//   - seg：光标位于第几个视觉行（0-based）；
//   - visCol：光标在该视觉行内的显示列（0-based，按终端单元格计，CJK 占 2 列）。
func (m *EditorModel) screenPos() (wrapped []string, seg, visCol int) {
	av := m.editAvailWidth()
	line := string(m.Buf.GetLine(m.cursorRow))
	wrapped = termd.WrapText(line, av)
	// 计算光标前所有 rune 的显示宽度（与 editAvailWidth 的列口径一致）
	prefixW := 0
	rc := 0
	for _, r := range line {
		if rc >= m.cursorCol {
			break
		}
		prefixW += termd.CellWidth(r)
		rc++
	}
	if av < 1 {
		av = 1
	}
	seg = prefixW / av
	visCol = prefixW % av
	return wrapped, seg, visCol
}

// runeColAtDisplay 返回 buffer 行 line 中第 displayCol 个显示列（0-based）内所对应的
// rune 列。displayCol 超出该行显示总宽时钳制到行尾 rune 列（仿 vim coladvance 在
// 行长不足时停在行尾）。用于把“显示列”目标的移动换算回可编辑的 rune 列。
func runeColAtDisplay(line []byte, displayCol int) int {
	acc := 0
	rc := 0
	for _, r := range string(line) {
		w := termd.CellWidth(r)
		if acc+w > displayCol {
			// 光标落在当前字符所占据的显示单元格 [acc, acc+w) 内
			return rc
		}
		acc += w
		rc++
	}
	return rc // 行尾（含空行）
}

// visualLineStep 在“视觉行（屏幕行）”空间中移动一步（仿 vim 的 gj / gk）：
//   - dir > 0：下移一个屏幕行；
//   - dir < 0：上移一个屏幕行。
//
// 与 j/k（按 buffer 行移动）不同，这里尊重软换行：光标行若因超宽被 termd.WrapText 拆成
// 多个视觉行，则先在同一 buffer 行的相邻视觉行间移动；到该行最后/首个视觉行后才
// 跨到相邻 buffer 行的首/末视觉行，并尽量保持同一显示列 visCol。
// 无法再移动（已到文件首/末屏行）时返回 false，光标保持不变。
func (m *EditorModel) visualLineStep(dir int) bool {
	n := m.Buf.LineCount()
	av := m.editAvailWidth()
	wrapped, seg, visCol := m.screenPos()
	row := m.cursorRow

	if dir > 0 { // 下移一个屏幕行
		if seg+1 < len(wrapped) {
			// 同一 buffer 行的下一个视觉行
			m.cursorCol = runeColAtDisplay(m.Buf.GetLine(row), (seg+1)*av+visCol)
			return true
		}
		if row+1 >= n {
			return false // 已到文件末屏行
		}
		// 跨到下一 buffer 行的首个视觉行（显示列仍按 visCol 对齐）
		m.cursorRow = row + 1
		m.cursorCol = runeColAtDisplay(m.Buf.GetLine(m.cursorRow), visCol)
		return true
	}

	// 上移一个屏幕行
	if seg > 0 {
		m.cursorCol = runeColAtDisplay(m.Buf.GetLine(row), (seg-1)*av+visCol)
		return true
	}
	if row <= 0 {
		return false // 已到文件首屏行
	}
	// 跨到上一 buffer 行的最后一个视觉行（显示列按 visCol 对齐）
	prev := string(m.Buf.GetLine(row - 1))
	pw := termd.WrapText(prev, av)
	m.cursorRow = row - 1
	m.cursorCol = runeColAtDisplay(m.Buf.GetLine(m.cursorRow), (len(pw)-1)*av+visCol)
	return true
}

// clampCursor 把光标钳制到合法范围（仿 vim 的 check_cursor / validate_cursor）：
// cursorRow 限制在 [0, 行数-1]，cursorCol 限制在 [0, 当前行 rune 数]。
// 用于编辑/撤销导致行内容收缩后，防止光标悬停在行尾空白甚至越界位置。
func (m *EditorModel) clampCursor() {
	n := m.Buf.LineCount()
	if n <= 0 {
		n = 1
	}
	if m.cursorRow < 0 {
		m.cursorRow = 0
	}
	if m.cursorRow >= n {
		m.cursorRow = n - 1
	}
	maxc := byteToRuneCol(m.Buf.GetLine(m.cursorRow), len(m.Buf.GetLine(m.cursorRow)))
	if m.cursorCol > maxc {
		m.cursorCol = maxc
	}
	if m.cursorCol < 0 {
		m.cursorCol = 0
	}
}

// firstNonBlank 返回该行首个非空白字符的 rune 列（行尾全空则返回 0）。
func firstNonBlank(line []byte) int {
	runes := []rune(string(line))
	for i, r := range runes {
		if !isSpaceRune(r) {
			return i
		}
	}
	return 0
}

// isSpaceRune 判断是否为空白字符。
func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

// moveWord 向前移动到第 count 个词首（w / W：W 忽略标点，按空白分词）。
func (m *EditorModel) moveWord(count int, big bool) {
	for c := 0; c < count; c++ {
		if !m.stepWordForward(big) {
			break
		}
	}
	m.cursWant = m.cursorCol
}

// moveBackWord 向后移动到第 count 个词首（b / B）。
func (m *EditorModel) moveBackWord(count int, big bool) {
	for c := 0; c < count; c++ {
		if !m.stepWordBackward(big) {
			break
		}
	}
	m.cursWant = m.cursorCol
}

// moveWordEnd 向前移动到第 count 个词尾（e / E）。
func (m *EditorModel) moveWordEnd(count int, big bool) {
	for c := 0; c < count; c++ {
		if !m.stepWordEnd(big) {
			break
		}
	}
	m.cursWant = m.cursorCol
}

// stepWordForward 仿 vim 'w'：跳过当前词及后续空白，停在下一个词首。
func (m *EditorModel) stepWordForward(big bool) bool {
	n := m.Buf.LineCount()
	for row := m.cursorRow; row < n; row++ {
		runes := []rune(string(m.Buf.GetLine(row)))
		start := m.cursorCol
		if row > m.cursorRow {
			start = 0
		}
		i := start
		// 跳过当前位置所在的词内字符
		if i < len(runes) && isWordRune(runes[i], big) {
			for i < len(runes) && isWordRune(runes[i], big) {
				i++
			}
		} else if i < len(runes) {
			// 当前在空白/标点：先跳过非词字符
			for i < len(runes) && !isWordRune(runes[i], big) {
				i++
			}
			if i < len(runes) {
				m.cursorRow, m.cursorCol = row, i
				return true
			}
			continue
		}
		// 跳过词后空白
		for i < len(runes) && isSpaceRune(runes[i]) {
			i++
		}
		if i < len(runes) {
			m.cursorRow, m.cursorCol = row, i
			return true
		}
	}
	return false
}

// stepWordBackward 仿 vim 'b'：向后移动到词首。
func (m *EditorModel) stepWordBackward(big bool) bool {
	for row := m.cursorRow; row >= 0; row-- {
		runes := []rune(string(m.Buf.GetLine(row)))
		end := m.cursorCol
		if row < m.cursorRow {
			end = len(runes)
		}
		i := end - 1
		// 跳过光标前的空白
		for i >= 0 && isSpaceRune(runes[i]) {
			i--
		}
		if i < 0 {
			if row == 0 {
				return false
			}
			continue
		}
		// 跳过当前词
		for i >= 0 && isWordRune(runes[i], big) {
			i--
		}
		m.cursorRow, m.cursorCol = row, i+1
		return true
	}
	return false
}

// stepWordEnd 仿 vim 'e'：向前移动到词尾。
func (m *EditorModel) stepWordEnd(big bool) bool {
	n := m.Buf.LineCount()
	for row := m.cursorRow; row < n; row++ {
		runes := []rune(string(m.Buf.GetLine(row)))
		start := m.cursorCol
		if row > m.cursorRow {
			start = 0
		}
		// 若当前在词内，先跳过当前词剩余部分
		i := start
		if i < len(runes) && isWordRune(runes[i], big) {
			for i < len(runes) && isWordRune(runes[i], big) {
				i++
			}
		} else {
			// 当前在非词字符（空白/标点）：先跳到下一个词
			for i < len(runes) && !isWordRune(runes[i], big) {
				i++
			}
		}
		if i >= len(runes) {
			if row == n-1 {
				m.cursorRow, m.cursorCol = row, max(0, len(runes)-1)
				return false
			}
			continue
		}
		// i 现在指向某词首，向后找词尾
		for i < len(runes) && isWordRune(runes[i], big) {
			i++
		}
		m.cursorRow, m.cursorCol = row, i-1
		return true
	}
	return false
}

// isWordRune 判定是否为“词字符”。big=true 时仅按空白分词（仿 vim 'W'），
// big=false 时按 iskeyword 语义（字母/数字/下划线/_/-，并包含中文等），仿 vim 'w'。
func isWordRune(r rune, big bool) bool {
	if big {
		return !isSpaceRune(r)
	}
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	if r == '_' || r == '-' {
		return true
	}
	// 中文/日文/韩文等表意文字视为词的一部分
	if r >= 0x4E00 && r <= 0x9FFF || r >= 0x3040 && r <= 0x30FF || r >= 0xAC00 && r <= 0xD7AF {
		return true
	}
	return false
}

// moveParagraph 仿 vim '{' / '}'：在段落边界间移动（空行分隔）。
func (m *EditorModel) moveParagraph(count int) {
	n := m.Buf.LineCount()
	if count > 0 {
		// '}'：向下找到第 count 个段落起始（空行之后的首个非空行）
		for c := 0; c < count; c++ {
			row := m.cursorRow + 1
			for row < n && strings.TrimSpace(string(m.Buf.GetLine(row))) != "" {
				row++
			}
			for row < n && strings.TrimSpace(string(m.Buf.GetLine(row))) == "" {
				row++
			}
			if row >= n {
				row = n - 1
			}
			m.cursorRow = row
		}
	} else {
		count = -count
		for c := 0; c < count; c++ {
			row := m.cursorRow - 1
			for row > 0 && strings.TrimSpace(string(m.Buf.GetLine(row))) != "" {
				row--
			}
			for row > 0 && strings.TrimSpace(string(m.Buf.GetLine(row))) == "" {
				row--
			}
			if row < 0 {
				row = 0
			}
			m.cursorRow = row
		}
	}
	m.colKeep()
}

// moveSentence 仿 vim '(' / ')'：在句子边界间移动（. ! ? 后空白分隔）。
func (m *EditorModel) moveSentence(count int) {
	n := m.Buf.LineCount()
	if count > 0 {
		for c := 0; c < count; c++ {
			if !m.stepSentenceForward() {
				m.cursorRow = n - 1
				break
			}
		}
	} else {
		count = -count
		for c := 0; c < count; c++ {
			if !m.stepSentenceBackward() {
				m.cursorRow, m.cursorCol = 0, 0
				break
			}
		}
	}
	m.colKeep()
}

// stepSentenceForward 找到下一个句子起始（仿 vim ')'）。
func (m *EditorModel) stepSentenceForward() bool {
	n := m.Buf.LineCount()
	for row := m.cursorRow; row < n; row++ {
		runes := []rune(string(m.Buf.GetLine(row)))
		start := 0
		if row == m.cursorRow {
			start = m.cursorCol
		}
		for i := start; i < len(runes); i++ {
			if runes[i] == '.' || runes[i] == '!' || runes[i] == '?' {
				// 句末符号后需接空白（或行尾）才算句子结束
				j := i + 1
				for j < len(runes) && isSpaceRune(runes[j]) {
					j++
				}
				if j >= len(runes) {
					if row < n-1 {
						m.cursorRow = row + 1
						m.cursorCol = 0
						return true
					}
					continue
				}
				m.cursorRow, m.cursorCol = row, j
				return true
			}
		}
	}
	return false
}

// stepSentenceBackward 找到上一个句子起始（仿 vim '('）。
func (m *EditorModel) stepSentenceBackward() bool {
	for row := m.cursorRow; row >= 0; row-- {
		runes := []rune(string(m.Buf.GetLine(row)))
		end := len(runes)
		if row == m.cursorRow {
			end = m.cursorCol
		}
		for i := end - 1; i >= 0; i-- {
			if runes[i] == '.' || runes[i] == '!' || runes[i] == '?' {
				// 该句末符号之前的句子起点：向前找前一个句末符后的空白
				m.cursorRow, m.cursorCol = row, i+1
				return true
			}
		}
	}
	return false
}

// scrollHalfPage 仿 vim Ctrl+D / Ctrl+U：视口以视觉行滚动半屏（光标随内容移动，仿 vim）。
func (m *EditorModel) scrollHalfPage(count int) {
	ch := visibleLines(m)
	if ch <= 0 {
		ch = 1
	}
	m.editScrollByVisual(max(1, ch/2) * count)
}

// scrollHalfPageVisible 仿 vim Ctrl+F / Ctrl+B：视口以视觉行滚动整屏（保留一行重叠），
// 光标移到新窗口顶部/底部（仿 vim）。
func (m *EditorModel) scrollHalfPageVisible(dir int) {
	ch := visibleLines(m)
	if ch <= 0 {
		ch = 1
	}
	m.editScrollByVisual(max(1, ch-1) * dir)
	if dir > 0 {
		// Ctrl+F：光标移到新窗口底部
		m.cursorRow = m.editBufferLineAtVisual(m.scroll, ch-1)
	} else {
		// Ctrl+B：光标移到新窗口顶部
		m.cursorRow = m.editBufferLineAtVisual(m.scroll, 0)
	}
	m.colKeep()
}

// centerCursor / topCursor / bottomCursor 仿 vim zz / zt / zb。
func (m *EditorModel) centerCursor() {
	ch := visibleLines(m)
	m.scroll = clamp(m.cursorRow-ch/2, 0, max(0, m.Buf.LineCount()-ch))
}
func (m *EditorModel) topCursor() {
	m.scroll = clamp(m.cursorRow, 0, max(0, m.Buf.LineCount()-visibleLines(m)))
}
func (m *EditorModel) bottomCursor() {
	ch := visibleLines(m)
	m.scroll = clamp(m.cursorRow-ch+1, 0, max(0, m.Buf.LineCount()-ch))
}

// deleteForward 仿 vim 'x'：删除光标下 count 个字符。
func (m *EditorModel) deleteForward(count int) {
	line := m.Buf.GetLine(m.cursorRow)
	maxc := byteToRuneCol(line, len(line))
	for i := 0; i < count && m.cursorCol < maxc; i++ {
		bc := runeColToByte(m.Buf.GetLine(m.cursorRow), m.cursorCol)
		m.Buf.Backspace(m.cursorRow, bc+1) // 删除光标右侧字符
	}
	m.cursWant = m.cursorCol
	m.touch()
}

// deleteBackward 仿 vim 'X'：删除光标左侧 count 个字符。
func (m *EditorModel) deleteBackward(count int) {
	for i := 0; i < count && m.cursorCol > 0; i++ {
		bc := runeColToByte(m.Buf.GetLine(m.cursorRow), m.cursorCol)
		m.Buf.Backspace(m.cursorRow, bc)
		m.cursorCol--
	}
	m.cursWant = m.cursorCol
	m.touch()
}

// deleteWord 仿 vim 'dw'：删除从光标到下一个词首的 count 个词（跨行则删除到下一行行首）。
func (m *EditorModel) deleteWord(count int) {
	n := m.Buf.LineCount()
	for c := 0; c < count; c++ {
		line := m.Buf.GetLine(m.cursorRow)
		if line == nil {
			break
		}
		// 先移动到下一个词首（或行尾）
		beforeRow, beforeCol := m.cursorRow, m.cursorCol
		if !m.stepWordForward(false) {
			// 已到文末：删除到当前行尾剩余内容
			bc := runeColToByte(m.Buf.GetLine(beforeRow), beforeCol)
			for m.cursorCol < byteToRuneCol(m.Buf.GetLine(beforeRow), len(m.Buf.GetLine(beforeRow))) {
				m.Buf.DeleteRune(beforeRow, bc)
				m.cursorCol = byteToRuneCol(m.Buf.GetLine(beforeRow), bc)
			}
			break
		}
		// 删除 [before, now) 之间的内容（可能跨行）
		for (m.cursorRow > beforeRow) || (m.cursorRow == beforeRow && m.cursorCol > beforeCol) {
			if m.cursorRow == beforeRow {
				bc := runeColToByte(m.Buf.GetLine(m.cursorRow), beforeCol)
				m.Buf.DeleteRune(m.cursorRow, bc)
				m.cursorCol = byteToRuneCol(m.Buf.GetLine(m.cursorRow), bc)
				beforeCol++
			} else {
				// 跨行：删除下一行整行（退化为逐字符删到行首）
				m.Buf.DeleteLine(beforeRow + 1)
				m.cursorRow = beforeRow
				m.cursorCol = beforeCol
				if m.cursorRow >= n {
					break
				}
			}
		}
	}
	m.cursWant = m.cursorCol
	m.touch()
}

// deleteLine 仿 vim 'dd'：删除 count 行（保留至少一行空行）。
func (m *EditorModel) deleteLine(count int) {
	n := m.Buf.LineCount()
	end := min(n-1, m.cursorRow+count-1)
	for row := end; row >= m.cursorRow; row-- {
		m.Buf.DeleteLine(row)
	}
	if m.cursorRow >= m.Buf.LineCount() {
		m.cursorRow = max(0, m.Buf.LineCount()-1)
	}
	m.cursorCol = 0
	m.cursWant = 0
	m.touch()
}

// ensureCursorVisible 调整 Edit 模式的 scroll，使 cursorRow 落在可视区域内。
// 关键：渲染时单行会因软换行展开为多行视觉行，因此必须按“视觉行”而非“termd.Buffer 行”
// 计算滚动窗口，否则光标行的软换行尾部会溢出到状态栏/界面之外。
func (m *EditorModel) ensureCursorVisible() {
	// 先钳制光标到合法范围（仿 vim validate_cursor），再做基于视觉行的滚动计算。
	m.clampCursor()
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	// 估算某 buffer 行的视觉行数：统一走 editVisualHeight（与 renderEdit 一致，
	// mermaid 代码块按其渲染高度整块计），保证光标滚动计算与渲染高度一致。
	visOf := func(i int) int {
		return m.editVisualHeight(i)
	}
	// 累计 scroll..cursorRow-1 的视觉行数
	acc := 0
	for i := m.scroll; i < m.cursorRow; i++ {
		acc += visOf(i)
	}
	curH := visOf(m.cursorRow)
	if m.cursorRow < m.scroll {
		// 光标行在窗口上方（如 gg 从文件中部/底部跳回）：把窗口移到光标行。
		m.scroll = m.cursorRow
	} else if acc+curH > ch {
		// 光标行（含其软换行）放不下，需上移 scroll。
		if curH >= ch {
			// 单行即超过整屏：从光标行顶部开始，超出部分自然滚出。
			m.scroll = m.cursorRow
		} else {
			// 从光标行往上逐行累加，直到再放一行就超出窗口为止。
			newScroll := m.cursorRow
			acc2 := curH
			for i := m.cursorRow - 1; i >= 0; i-- {
				h := visOf(i)
				if acc2+h > ch {
					break
				}
				acc2 += h
				newScroll = i
			}
			m.scroll = newScroll
		}
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll >= m.Buf.LineCount() {
		m.scroll = max(0, m.Buf.LineCount()-1)
	}
	// 滚动起点不得落在 mermaid 块内部（否则大图占满视口，画面钉死）
	m.fixScrollInsideMermaidBlock()
}
