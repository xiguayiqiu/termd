package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ============================================================
// outline —— Markdown 大纲侧边栏（目录跳转）
// ============================================================
//
// 设计目标（仿 vim 的 tagbar / :Toc）：
//   - 默认关闭，Preview / Edit 模式按 ctrl+t 切换。
//   - 打开时主内容区宽度缩减，左侧绘制 markdown 标题目录（# ~ ######）。
//   - 用 j/k/↑/↓ 在大纲内移动高亮项，内容区实时跟随定位（仿目录联动）；
//     Enter 关闭侧边栏并锁定到所选标题，Esc 仅关闭侧边栏。
//   - 状态栏与命令栏布局不变（仍固定在底部，与内容区隔离）。
//
// 与文件浏览器（:ex）互斥：文件浏览器打开时大纲列不缩减内容区，避免布局冲突。

// OutlineItem 表示一个 markdown 标题项。
type OutlineItem struct {
	Level int    // 标题层级 1~6
	Title string // 标题文本（已去除首尾 # 与空白）
	Line  int    // 标题所在的 Buffer 行（0-based），用于跳转
}

// atxHeadingRE 匹配 ATX 风格标题：允许最多 3 个前导空格，1~6 个 # 后接空格与标题文本，
// 并兼容行尾可选的闭合 #（goldmark 风格）。例： "# 标题" / "## 子标题 ###"。
// 需避开代码块内与转义（由 extractOutline 的围栏判断处理）。
var atxHeadingRE = regexp.MustCompile(`^[ \t]{0,3}(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$`)

// extractOutline 从缓冲区解析所有 markdown 标题，按出现顺序返回。
// 跳过代码块（``` / ~~~ 围栏）内的 # 行，避免误判。
func extractOutline(buf *Buffer) []OutlineItem {
	lines := buf.lines
	var items []OutlineItem
	inFence := false
	for i, lb := range lines {
		s := string(lb)
		trim := strings.TrimSpace(s)
		// 围栏切换（与 renderPreview 的 isFence* 判定一致）
		if isFenceStart(trim) {
			inFence = true
			continue
		}
		if isFenceEnd(trim) {
			inFence = false
			continue
		}
		if inFence {
			continue
		}
		m := atxHeadingRE.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		level := len(m[1]) // # 的个数
		title := strings.TrimSpace(m[2])
		if title == "" {
			// 纯 "#" 行也作为标题（空标题）
			title = "(空标题)"
		}
		items = append(items, OutlineItem{Level: level, Title: title, Line: i})
	}
	return items
}

// outlineWidth 返回大纲列宽度（像素列数）。未开启或文件浏览器打开时为 0。
func (m *editorModel) outlineWidth() int {
	if !m.outlineMode {
		return 0
	}
	if m.fb != nil && m.fb.open {
		return 0 // 与文件浏览器互斥，不缩减内容区
	}
	// 用户拖拽设定的宽度优先；否则用自动默认公式。
	if m.outlineW > 0 {
		w := m.outlineW
		if w < outlineMinWidth {
			w = outlineMinWidth
		}
		// 保证主内容区至少保留 40 列
		if w > m.width-outlineContentMin {
			w = m.width - outlineContentMin
		}
		if w < 0 {
			w = 0
		}
		return w
	}
	w := m.width * 28 / 100
	if w < 20 {
		w = 20
	}
	if w > 40 {
		w = 40
	}
	// 保证主内容区至少保留 40 列，避免过窄
	if m.width-w < 40 {
		w = m.width - 40
		if w < 0 {
			w = 0
		}
	}
	return w
}

// contentWidth 返回主内容区可用宽度（终端宽减去大纲列宽）。
// 文件浏览器打开时返回全宽（大纲列不参与布局）。
func (m *editorModel) contentWidth() int {
	w := m.width - m.outlineWidth()
	if w < 20 {
		w = 20
	}
	return w
}

// toggleOutline 打开/关闭大纲侧边栏；打开时惰性提取大纲并定位高亮项到当前行。
func (m *editorModel) toggleOutline() {
	// 大纲列宽变化 => 内容区重排，滚动帧冻结缓存失效。
	m.invalidateScrollRender()
	if m.outlineMode {
		m.outlineMode = false
		m.status = T("已关闭大纲")
		return
	}
	m.outlineMode = true
	m.outlineItems = extractOutline(m.buf)
	// 高亮项定位到当前可见标题（取 <= 当前行的最后一个标题）
	cur := m.previewCursor
	if m.sm.Mode() == ModeEdit {
		cur = m.cursorRow
	}
	m.outlineCursor = 0
	for i, it := range m.outlineItems {
		if it.Line <= cur {
			m.outlineCursor = i
		} else {
			break
		}
	}
	m.status = Tf("大纲：%d 个标题（j/k 移动，Enter 跳转，Esc 关闭）", len(m.outlineItems))
}

// handleOutlineKey 处理大纲打开时的导航按键。
// 返回 (model, cmd)；handled 表示是否消费了该按键（true 时由调用方跳过常规处理）。
func (m *editorModel) handleOutlineKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+t":
		m.toggleOutline()
		return m, nil, true
	case "esc":
		m.outlineMode = false
		m.invalidateScrollRender() // 内容区恢复全宽
		m.status = T("已关闭大纲")
		return m, nil, true
	case "j", "down":
		if len(m.outlineItems) > 0 {
			m.outlineCursor = min(m.outlineCursor+1, len(m.outlineItems)-1)
			m.syncOutlineToContent()
		}
		return m, nil, true
	case "k", "up":
		if len(m.outlineItems) > 0 {
			m.outlineCursor = max(m.outlineCursor-1, 0)
			m.syncOutlineToContent()
		}
		return m, nil, true
	case "enter":
		m.jumpToOutline(m.outlineCursor)
		m.outlineMode = false      // 跳转后关闭侧边栏
		m.invalidateScrollRender() // 内容区恢复全宽
		m.status = T("已跳转到标题")
		return m, nil, true
	}
	// 其余按键不消费，交回常规模式处理（例如 i 进入编辑）
	return m, nil, false
}

// syncOutlineToContent 把当前高亮大纲项对应的 buffer 行同步到内容区当前行，
// 实现“目录-正文联动”（不关闭侧边栏）。
func (m *editorModel) syncOutlineToContent() {
	if m.outlineCursor < 0 || m.outlineCursor >= len(m.outlineItems) {
		return
	}
	line := m.outlineItems[m.outlineCursor].Line
	if m.sm.Mode() == ModeEdit {
		m.cursorRow = clamp(line, 0, m.buf.LineCount()-1)
		m.cursorCol = 0
		m.cursWant = 0
		m.ensureCursorVisible()
	} else {
		m.previewCursor = clamp(line, 0, m.buf.LineCount()-1)
		m.ensurePreviewCursorVisible()
	}
}

// jumpToOutline 跳转到指定大纲项（仅设置行，不关闭侧边栏）。
func (m *editorModel) jumpToOutline(idx int) {
	if idx < 0 || idx >= len(m.outlineItems) {
		return
	}
	m.syncOutlineToContent()
}

// renderOutline 渲染左侧大纲列（固定宽 outlineWidth，高度 = 内容区可视行数）。
// 内容占 outlineWidth()-1 列，最右 1 列留给 composeOutlineWithBody 的分隔线。
// 返回按行拼接的字符串；每行已用 ANSI 着色，可直接与正文逐行并排。
func (m *editorModel) renderOutline() string {
	w := m.outlineWidth()
	if w <= 0 {
		return ""
	}
	// 内容列宽：为右侧分隔线预留 1 列。
	contentW := w - 1
	if contentW < 1 {
		contentW = 1
	}
	h := visibleLines(m)
	if h < 1 {
		h = 1
	}

	// 标题行
	title := lipgloss.NewStyle().
		Background(lipgloss.Color("63")).Foreground(lipgloss.Color("255")).
		Render(padRightRunes(" 大纲 OUTLINE", contentW))
	rows := []string{title}

	items := m.outlineItems
	if len(items) == 0 {
		empty := lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Render(padRightRunes(" (无标题)", contentW))
		rows = append(rows, empty)
		for len(rows) < h {
			rows = append(rows, strings.Repeat(" ", contentW))
		}
		return strings.Join(rows, "\n")
	}

	// 列内滚动：让高亮项居中
	top := m.outlineCursor - (h-2)/2
	if top < 0 {
		top = 0
	}
	if bottom := top + (h - 2); bottom > len(items) {
		top = max(0, len(items)-(h-1))
	}

	// 轮替层级标记（仿代码嵌套：• ◦ ▪），并按层级缩进
	markers := []string{"•", "◦", "▪", "▸", "▹", "◂"}
	var body []string
	for r := 0; r < h-1; r++ {
		idx := top + r
		if idx >= len(items) {
			body = append(body, strings.Repeat(" ", contentW))
			continue
		}
		it := items[idx]
		indent := strings.Repeat("  ", it.Level-1)
		mk := "•"
		if it.Level-1 < len(markers) {
			mk = markers[it.Level-1]
		}
		label := fmt.Sprintf("%s%s %s", indent, mk, it.Title)
		label = runewidth.Truncate(label, contentW-1, "…")
		label = padRightRunes(label, contentW)
		if idx == m.outlineCursor {
			body = append(body, lipgloss.NewStyle().
				Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255")).
				Render(label))
		} else {
			body = append(body, lipgloss.NewStyle().
				Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).
				Render(label))
		}
	}
	rows = append(rows, body...)
	return strings.Join(rows, "\n")
}

// padRightRunes 用空格右补齐到 width 个显示列（按 rune 宽度，中文算 2 列由 runewidth 处理）。
func padRightRunes(s string, width int) string {
	n := runewidth.StringWidth(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// composeOutlineWithBody 把大纲列、分隔线列与正文逐行并排，返回内容区完整字符串。
// 大纲列内容宽 = outlineWidth()-1，分隔线 1 列，正文宽 = contentWidth()，
// 三者之和 = 终端宽，因此并排后总宽与状态栏对齐（状态栏仍由 fixBottom 固定贴底）。
func (m *editorModel) composeOutlineWithBody(bodyStr string) string {
	ow := m.outlineWidth()
	if ow <= 0 {
		return bodyStr
	}
	// 分隔线列：竖直分隔线。注意用 ASCII '|' 而非 '│'（U+2502 是 Unicode East Asian
	// Ambiguous 宽度，在中文 locale + kitty 等终端下会渲染为 2 列，导致每行超宽
	// 自动换行、整个界面被向上推动）。ASCII '|' 在任意终端恒为 1 列。
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("239")).Render("|")
	oc := m.renderOutline() // 已按 \n 分行，行数 = 内容区可视行数
	bodyLines := strings.Split(bodyStr, "\n")
	ocLines := strings.Split(oc, "\n")
	n := len(bodyLines)
	if len(ocLines) > n {
		n = len(ocLines)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		bl := ""
		if i < len(bodyLines) {
			bl = bodyLines[i]
		}
		ol := ""
		if i < len(ocLines) {
			ol = ocLines[i]
		}
		out[i] = ol + sep + bl
	}
	return strings.Join(out, "\n")
}
