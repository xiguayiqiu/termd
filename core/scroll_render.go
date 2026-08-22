package core

// ============================================================
// scroll_render —— 视觉行滚动（借鉴 glow 的 viewport 模型）
// ============================================================
//
// glow（bubbletea + bubbles/viewport）丝滑浏览的核心理念：
//   1. 内容预先渲染并软换行成一维“视觉行”数组（glow 的 SetContent 切分 + 软换行）；
//   2. 维护一个整数偏移 yOffset（视觉行），滚轮/翻页按固定视觉行步长移动它
//      （bubbles 默认 MouseWheelDelta=3，即 vim 的 mouse_vert_step）；
//   3. 每帧只输出 lines[yOffset : yOffset+height] 这一可见切片，交给 bubbletea
//      的逐行 diff 只重绘变化行 —— 不依赖终端滚动区，天然正确、平滑、可回退。
//
// 旧实现（DECSTBM + CSI S/T 终端物理滚动 + 冻结帧）会与 bubbletea 的行 diff
// 渲染器失步：终端内容被物理移动而 bubbletea 的屏幕状态不知道，导致内容错位、
// 光标消失、滚动“跳一大片”。本模块彻底移除该机制，Preview 与 Edit 都改为
// 视觉行偏移滚动（vim 的 topline / mouse_vert_step 语义）：
//
//   - Preview 模式：m.previewScroll 是 previewLines（已含行号/软换行的视觉行）的
//     顶部偏移；滚轮/翻页按视觉行步进，高亮行（光标块）随内容同步移动。
//   - Edit 模式：m.scroll 仍是 vim 的 topline（buffer 行），但滚轮/翻页通过
//     editScrollByVisual 以“视觉行”为单位换算 topline，避免一个 buffer 行软换行成
//     多行时滚轮“跳一大片”。

import (
	"strings"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"termd"
)

// tildeLine 预览/编辑模式文件末尾的占位行（与 renderPreview / renderEdit 的 ~ 样式一致）。
func tildeLine() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Render("~")
}

// invalidateScrollRender 内容/宽度变化后调用：使预览视口偏移在下次渲染时按新内容
// 重新钳制（位置尽量保留，超出新边界时收缩到合法范围）。旧 DECSTBM 滚动帧缓存
// 已移除，此钩子仅保留钳制语义，供模式切换 / resize / 编辑等路径统一调用。
func (m *EditorModel) invalidateScrollRender() {
	if len(m.previewLines) > 0 {
		m.clampPreviewScroll()
	}
}

// clampPreviewScroll 把 previewScroll 钳制到 [0, max(0, len(previewLines)-ch)]。
func (m *EditorModel) clampPreviewScroll() {
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	maxScroll := len(m.previewLines) - ch
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.previewScroll > maxScroll {
		m.previewScroll = maxScroll
	}
	if m.previewScroll < 0 {
		m.previewScroll = 0
	}
}

// setPreviewCursorToRow 把 previewCursor 设置为 previewLines[ridx] 对应的 buffer 行。
// ridx 越界时钳制到最近的有效视觉行；若视觉行落在文末 ~ 占位区，取最后一个真实内容行。
func (m *EditorModel) setPreviewCursorToRow(ridx int) {
	if len(m.previewRowToBuffer) == 0 {
		return
	}
	if ridx < 0 {
		ridx = 0
	}
	if ridx >= len(m.previewRowToBuffer) {
		ridx = len(m.previewRowToBuffer) - 1
	}
	m.previewCursor = m.previewRowToBuffer[ridx]
}

// previewScrollByVisual 以视觉行为单位滚动 Preview 视口（仿 vim 滚轮 do_mousescroll /
// glow viewport 的 MouseWheelDelta）：视口移动 delta 个视觉行，高亮行（光标块）跟随
// 同一内容行（屏幕行随内容同步位移，保持 vim 的“光标随内容走”体验）。
//
// 【关键】返回 nil：仅更新 m.previewScroll 偏移，由 renderPreview 每帧输出
// previewLines[scroll:scroll+ch] 切片、交还 bubbletea 逐行 diff 重绘。
// 早期版本这里返回 tea.SyncScrollArea / ScrollDown / ScrollUp 终端物理滚动区命令，
// 但该机制会维护一份“已知屏幕内容”缓存，与每帧全量切片输出、以及 View() 末尾的
// 滚动区重置（\x1b[r）互相冲突——结果就是鼠标/键盘滚动时内容错位累积（俗称“内容飘逸”）。
// 彻底弃用物理滚动区后，鼠标与键盘走同一条纯偏移路径，永不与 bubbletea 失步，与
// Edit 模式的纯偏移渲染（editScrollByVisual）行为完全一致。
func (m *EditorModel) previewScrollByVisual(delta int) tea.Cmd {
	if len(m.previewLines) == 0 {
		return nil
	}
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	m.clampPreviewScroll()
	// 光标（高亮行）当前在窗口内的视觉行（0-based）
	crow := 0
	if m.previewCursor >= 0 && m.previewCursor < len(m.bufferToPreview) {
		crow = m.bufferToPreview[m.previewCursor] - m.previewScroll
	}
	if crow < 0 {
		crow = 0
	}
	if crow >= ch {
		crow = ch - 1
	}
	oldScroll := m.previewScroll
	// 视口移动 delta 个视觉行
	m.previewScroll = clamp(m.previewScroll+delta, 0, max(0, len(m.previewLines)-ch))
	// 光标跟随同一内容：新窗口内视觉行 = crow - 实际位移（内容上移 shift 行）
	shift := m.previewScroll - oldScroll
	newCrow := crow - shift
	if newCrow < 0 {
		newCrow = 0
	}
	if newCrow >= ch {
		newCrow = ch - 1
	}
	m.setPreviewCursorToRow(m.previewScroll + newCrow)
	// 纯视觉行偏移：不激活任何终端物理滚动区，始终由 bubbletea 逐行 diff 重绘。
	// 光标随内容移动后请求重建：使“光标落在代码块/表格内则降级为原始文本”的
	// 逻辑即时生效（滚轮滚入块→显示源码，滚出块→恢复渲染），彻底规避飘逸。
	m.previewDirty = true
	return nil
}

// previewVisibleRows 返回当前 previewScroll 下内容区的可视行（恰好 ch 行，末尾用 ~ 补齐）。
func (m *EditorModel) previewVisibleRows(ch int) []string {
	rows := make([]string, 0, ch)
	for i := m.previewScroll; i < m.previewScroll+ch && i < len(m.previewLines); i++ {
		rows = append(rows, m.previewLines[i])
	}
	for len(rows) < ch {
		rows = append(rows, tildeLine())
	}
	return rows
}

// initScrollRegionCmd 初始化内容区滚动（v2 中无需物理滚动区，仅标记需要重绘）。
func (m *EditorModel) initScrollRegionCmd() tea.Cmd {
	// v2 中通过重新渲染 View() 处理滚动，无需终端物理滚动区
	m.previewDirty = true
	return nil
}

// clearScrollRegionCmd 清除滚动状态（v2 中无需物理滚动区）。
func (m *EditorModel) clearScrollRegionCmd() tea.Cmd {
	m.previewDirty = true
	return nil
}

// centerPreviewOnCursor 把视口居中定位到当前光标行（仿 vim zz，用于 :数字 / /搜索 跳转）。
func (m *EditorModel) centerPreviewOnCursor() {
	if m.previewDirty {
		// 预览缓存可能尚未构建（如刚打开文件就执行 :数字 跳转）：先重建
		m.rebuildPreview()
		m.previewDirty = false
	}
	if len(m.previewLines) == 0 {
		return
	}
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	if m.previewCursor < 0 || m.previewCursor >= len(m.bufferToPreview) {
		return
	}
	crow := m.bufferToPreview[m.previewCursor]
	m.previewScroll = clamp(crow-ch/2, 0, max(0, len(m.previewLines)-ch))
}

// ---- Edit 模式视觉行工具（termd.WrapText 口径与 renderEdit / ensureCursorVisible 一致）----

// editVisualHeight 返回第 i 个 buffer 行渲染后的视觉行数（软换行后，至少 1）。
// mermaid 代码块整块按其渲染后的 ANSI 字符画行数计（renderCodeRect 实际输出行数，
// 含矩形上下盖）：滚动/光标计算与 renderEdit 的真实渲染高度一致，否则大图被估算成
// 源码行数，滚动窗口无法跳过它，画面被图钉死（无法上下移动光标）。
func (m *EditorModel) editVisualHeight(i int) int {
	if h, ok := m.editMermaidVisualHeight(i); ok {
		return h
	}
	n := len(termd.WrapText(string(m.Buf.GetLine(i)), m.editAvailWidth()))
	if n < 1 {
		n = 1
	}
	return n
}

// editMermaidVisualHeight 若第 i 行是 mermaid 围栏块起始行，返回 (渲染后视觉高度, true)；
// 否则 (0, false)。渲染高度以 renderCodeRect 实际输出行数为准（与 renderEdit 的
// mermaid 分支同一口径：可用宽度 = editAvailWidth()-1，含顶/底盖）。
func (m *EditorModel) editMermaidVisualHeight(i int) (int, bool) {
	if i < 0 || i >= m.Buf.LineCount() {
		return 0, false
	}
	if !termd.IsFenceStart(strings.TrimSpace(string(m.Buf.GetLine(i)))) {
		return 0, false
	}
	j := i + 1
	for j < m.Buf.LineCount() && !termd.IsFenceEnd(strings.TrimSpace(string(m.Buf.GetLine(j)))) {
		j++
	}
	if j >= m.Buf.LineCount() {
		j = m.Buf.LineCount() - 1
	}
	fence := strings.TrimSpace(string(m.Buf.GetLine(i)))
	lang := strings.TrimPrefix(fence, "```")
	lang = strings.TrimPrefix(lang, "~~~")
	if !termd.IsMermaidFence(lang) {
		return 0, false
	}
	var inner []string
	for k := i + 1; k < j; k++ {
		inner = append(inner, string(m.Buf.GetLine(k)))
	}
	// 与 renderEdit 的 mermaid 分支一致：可用宽度再减 1（左侧边框）。
	ml, ok := termd.RenderMermaid(strings.Join(inner, "\n"), m.editAvailWidth()-1)
	if !ok {
		return 0, false
	}
	rect := renderCodeRect(ml, ml, "", "", m.editAvailWidth()-1)
	return len(rect), true
}

// fixScrollInsideMermaidBlock 确保 Edit 滚动起点不落在 mermaid 渲染块内部：
//   - 光标在块之后 → scroll 跳到块后首行（否则整块字符画占满视口，后面的内容
//     永远画不出来，画面被"钉死"，无法上下移动光标）；
//   - 光标在块内 → scroll 回到块起始行（与 renderEdit 的 cursorInside 分支显示
//     原始源码的行为一致）。
//
// 反向扫描在遇到任何围栏行（起始或结束）时即终止，不会扫完整份文件；另设 4096
// 行回看上限防御超长无围栏文档。
func (m *EditorModel) fixScrollInsideMermaidBlock() {
	if m.scroll <= 0 || m.scroll >= m.Buf.LineCount() {
		return
	}
	s := m.scroll
	for s > 0 && m.scroll-s < 4096 {
		ts := strings.TrimSpace(string(m.Buf.GetLine(s)))
		if termd.IsFenceStart(ts) {
			break
		}
		if termd.IsFenceEnd(ts) {
			return // 往回先遇到围栏结束：scroll 在该块之后，不在任何更早的块内
		}
		s--
	}
	if s <= 0 {
		return
	}
	fence := strings.TrimSpace(string(m.Buf.GetLine(s)))
	lang := strings.TrimPrefix(fence, "```")
	lang = strings.TrimPrefix(lang, "~~~")
	if !termd.IsMermaidFence(lang) {
		return
	}
	e := s + 1
	for e < m.Buf.LineCount() && !termd.IsFenceEnd(strings.TrimSpace(string(m.Buf.GetLine(e)))) {
		e++
	}
	if e >= m.Buf.LineCount() {
		e = m.Buf.LineCount() - 1
	}
	if m.scroll < s || m.scroll > e {
		return
	}
	switch {
	case m.cursorRow > e: // 光标在块之后：窗口必须整体跳过该块
		m.scroll = e + 1
	case m.cursorRow >= s: // 光标在块内：窗口回到块首（显示原始源码）
		m.scroll = s
	}
}

// editVisTotal 返回 [from, to) 区间 buffer 行的视觉行数总和。
func (m *EditorModel) editVisTotal(from, to int) int {
	acc := 0
	for i := from; i < to; i++ {
		acc += m.editVisualHeight(i)
	}
	return acc
}

// editBufferLineAtVisual 返回从 top 行起视觉偏移 off（0-based）所在的 buffer 行；
// off 超出内容末尾时返回最后一个有内容的 buffer 行。
func (m *EditorModel) editBufferLineAtVisual(top, off int) int {
	n := m.Buf.LineCount()
	if n == 0 {
		return 0
	}
	if top < 0 {
		top = 0
	}
	if top >= n {
		return n - 1
	}
	acc := 0
	for i := top; i < n; i++ {
		h := m.editVisualHeight(i)
		if acc+h > off {
			return i
		}
		acc += h
	}
	return n - 1
}

// editBottomTopline 返回使窗口底部恰好贴住内容末尾的 topline（视口至少显示 ch 个
// 视觉行；内容不足 ch 时返回 0，由 ~ 占位填充）。用于滚轮触底时对齐底部（仿 vim）。
func (m *EditorModel) editBottomTopline(ch int) int {
	n := m.Buf.LineCount()
	if n == 0 {
		return 0
	}
	if m.editVisTotal(0, n) < ch {
		return 0
	}
	acc := 0
	top := n - 1
	for i := n - 1; i >= 0; i-- {
		h := m.editVisualHeight(i)
		if acc+h >= ch {
			return i
		}
		acc += h
		top = i
	}
	return top
}

// editScrollByVisual 以视觉行为单位滚动 Edit 模式视口（仿 vim 滚轮 do_mousescroll）：
//   - 视口整体移动 delta 个视觉行；
//   - 光标保持在同一内容上（屏幕行随内容同步位移，越界钳制到窗口首/末行）；
//   - delta>0 且超出文末时，视口底部对齐内容末尾（vim 触底行为）。
func (m *EditorModel) editScrollByVisual(delta int) {
	n := m.Buf.LineCount()
	ch := visibleLines(m)
	if ch < 1 || n == 0 {
		return
	}
	// 光标当前在窗口内的视觉行（0-based，钳制到窗口内）
	crow := m.editVisTotal(m.scroll, m.cursorRow)
	if crow < 0 {
		crow = 0
	}
	if crow >= ch {
		crow = ch - 1
	}
	// 找到新 topline（从当前 topline 起累计视觉行，落到下一个行边界）
	newTop := m.scroll
	if delta > 0 {
		need := delta
		for i := m.scroll; i < n; i++ {
			h := m.editVisualHeight(i)
			newTop = i + 1 // 消费完第 i 行后，窗口起点落到下一行边界
			if need > h {
				need -= h
				continue
			}
			break
		}
		if newTop > n-1 {
			newTop = n - 1
		}
		// 触底对齐：新视口视觉行不足 ch 时上移，使窗口底部贴住内容末尾
		if m.editVisTotal(newTop, n) < ch {
			newTop = m.editBottomTopline(ch)
		}
	} else if delta < 0 {
		need := -delta
		newTop = 0
		for i := m.scroll - 1; i >= 0; i-- {
			h := m.editVisualHeight(i)
			if need <= h {
				newTop = i
				break
			}
			need -= h
		}
	}
	if newTop < 0 {
		newTop = 0
	}
	if newTop >= n {
		newTop = max(0, n-1)
	}
	// 实际移动的视觉行数（新 topline 与旧 topline 之间）
	shift := 0
	if delta > 0 {
		shift = m.editVisTotal(m.scroll, newTop)
	} else {
		shift = m.editVisTotal(newTop, m.scroll)
	}
	m.scroll = newTop
	// 光标跟随同一内容：新窗口内视觉行 = crow - 实际位移（内容上移 shift 行）
	newCrow := crow - shift
	if newCrow < 0 {
		newCrow = 0
	}
	if newCrow >= ch {
		newCrow = ch - 1
	}
	m.cursorRow = m.editBufferLineAtVisual(m.scroll, newCrow)
	// 滚动起点不得落在 mermaid 块内部（否则大图占满视口，画面钉死）。
	// 光标已按旧 topline 对齐，若 scroll 被修正到块首/块后，光标行仍在窗口内。
	m.fixScrollInsideMermaidBlock()
	m.colKeep()
}