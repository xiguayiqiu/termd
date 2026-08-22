package core

// ============================================================
// mouse —— 鼠标支持
// ============================================================
//
// 参考 vim 源码 src/mouse.c：
//   - do_mousescroll()：滚轮上下翻页。无修饰键按固定行数滚动（vim 的
//     mouse_vert_step 默认 3 行）；带 Shift/Ctrl 修饰键则整页滚动
//     （vim 的 pagescroll）。
//   - win_drag_vsep_line()：左键按住窗口分隔列拖动可调整分隔宽度。
//     termd 中用同样的思路：鼠标按住「大纲侧边栏」与正文之间的边界拖动，
//     即可自由调整大纲列宽。
//
// 依赖 bubbletea v2 的鼠标事件（程序已用 tea.WithMouseCellMotion() 开启）：
//   - 滚轮：MouseWheelMsg (Button: MouseWheelUp/MouseWheelDown)；
//   - 点击/拖动：MouseClickMsg（按下）/ MouseMotionMsg（按住移动）/
//     MouseReleaseMsg（松开），配合 MouseLeft。
// 事件坐标 (X, Y) 均为 0-based。

import (
	"termd"

	"charm.land/bubbletea/v2"
)

// 鼠标相关常量。
const (
	// mouseScrollStep 普通滚轮一次滚动的行数（仿 vim mouse_vert_step 默认值 3）。
	mouseScrollStep = 3
	// outlineMinWidth 大纲列最小宽度（容纳层级标记与缩进）。
	outlineMinWidth = 16
	// outlineMaxWidth 大纲列最大宽度；超过则优先保证正文宽度。
	outlineMaxWidth = 60
	// outlineContentMin 拖动宽度时正文区至少保留的列数。
	outlineContentMin = 40
)

// handleMouse 统一处理所有鼠标事件（由 model.Update 在 case tea.MouseMsg 分发）。
// 优先级：
//  1. 正在拖拽大纲右边界时，motion/release 都用于调整宽度；
//  2. 左键按在大纲右边界分离器附近 => 开始拖拽；
//  3. 滚轮事件 => 上下翻阅（指针在大纲列内则滚大纲，否则滚正文）；
//  4. 其余鼠标事件忽略。
//
// 返回可选命令（Preview 滚动时携带终端滚动区序列，实现平滑行滚动）。
func (m *EditorModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	e := msg.Mouse()

	// 判断是否为滚轮事件
	isWheel := e.Button == tea.MouseWheelUp || e.Button == tea.MouseWheelDown

	// 帮助视图 / 文件浏览器等状态不响应鼠标拖拽。
	if m.helpMode || m.cmdHelpMode || m.markdownLangMode || (m.fb != nil && m.fb.Opened) {
		// 文件浏览器/帮助页里仍允许滚轮翻阅，但跳过拖拽与点击。
		if isWheel {
			// Markdown 语法教程视图：滚轮直接滚动教程内容（mlScroll），
			// 不经过 browseScroll（那会滚动正文/光标）。
			if m.markdownLangMode {
				delta := 0
				switch e.Button {
				case tea.MouseWheelUp:
					delta = -mouseScrollStep
				case tea.MouseWheelDown:
					delta = mouseScrollStep
				}
				if e.Mod&tea.ModShift != 0 || e.Mod&tea.ModCtrl != 0 {
					delta *= 3
				}
				m.mlScroll += delta
				return nil
			}
			return m.handleWheel(e)
		}
		return nil
	}

	// 1) 拖拽中：处理继续保持按下时的拖动与释放。
	if m.mouseDragging {
		switch msg.(type) {
		case tea.MouseMotionMsg:
			if e.Button == tea.MouseLeft {
				m.resizeOutlineDrag(e.X)
			}
		case tea.MouseReleaseMsg:
			m.mouseDragging = false
		}
		return nil
	}

	// 2) 左键按在大纲右边界分离器附近 => 开始拖拽。
	if m.outlineMode {
		if _, ok := msg.(tea.MouseClickMsg); ok {
			if e.Button == tea.MouseLeft {
				w := m.outlineWidth()
				if w > 0 && e.X >= w-1 && e.X <= w+1 {
					m.mouseDragging = true
					m.mouseDragStartX = e.X
					m.mouseDragStartW = m.outlineWidth()
					m.status = termd.T("拖动可调整大纲宽度")
					return nil
				}
			}
		}
	}

	// 2.5) 左键点击：Preview 模式检测超链接并打开（浏览器 / 本地文件）。
	if _, ok := msg.(tea.MouseClickMsg); ok {
		if e.Button == tea.MouseLeft {
			m.handlePreviewClick(e.X, e.Y)
			return nil
		}
	}

	// 3) 滚轮翻页。
	if isWheel {
		return m.handleWheel(e)
	}
	return nil
}

// handleWheel 处理滚轮上下翻页（仿 vim do_mousescroll）。
//   - 无修饰键：按 mouseScrollStep 行滚动（vim mouse_vert_step）；
//   - Shift / Ctrl 修饰键：整页滚动（vim pagescroll）。
//
// 指针位于大纲列内时滚大纲高亮项（并联动正文），否则滚正文当前行/高亮行。
func (m *EditorModel) handleWheel(e tea.Mouse) tea.Cmd {
	var dir int
	switch e.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	}
	if dir == 0 {
		return nil
	}
	step := mouseScrollStep
	if e.Mod&tea.ModShift != 0 || e.Mod&tea.ModCtrl != 0 {
		step = visibleLines(m) // 整页
	}
	delta := dir * step

	// 指针在大纲列内 => 滚动大纲选项卡（并联动正文高亮）。
	if m.outlineMode && e.X < m.outlineWidth() {
		m.outlineScroll(delta)
		return nil
	}
	return m.browseScroll(delta)
}

// handlePreviewClick 在 Preview 模式左键点击 (x,y)（0-based 屏幕列/行）时，
// 定位点击处是否命中超链接：命中则用系统默认程序打开（http(s) 走浏览器，
// 本地路径走 xdg-open / open），并提示状态栏。
func (m *EditorModel) handlePreviewClick(x, y int) {
	if m.sm.Mode() != termd.ModePreview {
		return // 编辑/命令模式点击用于移动光标/输入，不跳转
	}
	if m.helpMode || m.cmdHelpMode || m.markdownLangMode || (m.fb != nil && m.fb.Opened) {
		return
	}

	// 将屏幕坐标转换为 buffer 行和显示列
	// y=0 是顶部边框，内容区从 y=1 开始
	contentY := y - 1
	if contentY < 0 || contentY >= m.contentHeight() {
		return
	}

	// 计算点击行对应的 buffer 行
	// 简化：假设无软换行，每行占 1 视觉行
	row := m.scroll + contentY
	if row >= m.Buf.LineCount() {
		return
	}

	// 计算显示列（减去左边框和行号 gutter）
	dispCol := x - 1 // 左边框
	if m.sm.LineNumMode() != termd.LNNone {
		dispCol -= 5 // 行号 gutter
	}
	if dispCol < 0 {
		return
	}

	// 检查链接
	if target := m.linkTargetAtDispCol(row, dispCol); target != "" {
		m.openLink(target)
	}
}

// resizeOutlineDrag 拖拽调整大纲列宽度。
func (m *EditorModel) resizeOutlineDrag(x int) {
	if !m.outlineMode {
		return
	}
	// 计算新宽度（相对于拖拽起始位置的偏移）
	delta := x - m.mouseDragStartX
	newWidth := m.mouseDragStartW + delta
	// 限制宽度范围
	if newWidth < outlineMinWidth {
		newWidth = outlineMinWidth
	}
	if newWidth > outlineMaxWidth {
		newWidth = outlineMaxWidth
	}
	// 确保正文区至少保留 outlineContentMin 列
	contentWidth := m.width - newWidth - 2 // 减去边框
	if contentWidth < outlineContentMin {
		newWidth = m.width - outlineContentMin - 2
	}
	m.outlineW = newWidth
	m.previewDirty = true
	m.invalidateScrollRender()
}

// outlineScroll 在大纲项列表中滚动高亮项 delta 步（并同步正文）。
func (m *EditorModel) outlineScroll(delta int) {
	n := len(m.outlineItems)
	if n == 0 {
		return
	}
	m.outlineCursor = clamp(m.outlineCursor+delta, 0, n-1)
	m.syncOutlineToContent()
}

// browseScroll 滚动正文内容区 delta 步，返回可选滚动区命令。
// 默认（smoothScroll 开启）以"视觉行"为单位（glow viewport 的 MouseWheelDelta /
// vim mouse_vert_step）：每 tick 恰好移动 mouseScrollStep 个屏幕行，滚动平滑、永不
// "跳一大片"；Preview 模式下返回终端滚动区序列（DECSTBM 物理移动内容，不闪烁）。
// :set nosmoothscroll 可退回"按 buffer 行移动光标"的兼容模式。
func (m *EditorModel) browseScroll(delta int) tea.Cmd {
	if m.sm.Mode() == termd.ModeEdit {
		// Edit 模式：移动光标行
		newRow := clamp(m.cursorRow+delta, 0, m.Buf.LineCount()-1)
		m.cursorRow = newRow
		m.ensureCursorVisible()
		return nil
	}
	// Preview 模式：滚动视口
	if m.smoothScroll && m.previewLines != nil {
		m.previewScroll = clamp(m.previewScroll+delta, 0, max(0, len(m.previewLines)-m.contentHeight()))
		m.previewDirty = false // 滚动不改变内容，不需重建缓存
		return m.previewScrollByVisual(delta)
	}
	// 兼容模式：按 buffer 行移动高亮行
	m.previewCursor = clamp(m.previewCursor+delta, 0, m.Buf.LineCount()-1)
	m.ensurePreviewCursorVisible()
	return nil
}