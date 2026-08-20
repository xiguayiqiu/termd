package main

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
// 依赖 bubbletea 的鼠标事件（程序已用 tea.WithMouseCellMotion() 开启）：
//   - 滚轮：MouseActionPress + MouseButtonWheelUp/Down；
//   - 点击/拖动：MouseActionPress（按下）/ MouseActionMotion（按住移动）/
//     MouseActionRelease（松开），配合 MouseButtonLeft。
// 事件坐标 (X, Y) 均为 0-based。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbletea"
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
func (m *editorModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	e := tea.MouseEvent(msg)

	// 帮助视图 / 文件浏览器等状态不响应鼠标拖拽。
	if m.helpMode || m.cmdHelpMode || (m.fb != nil && m.fb.open) {
		// 文件浏览器/帮助页里仍允许滚轮翻阅，但跳过拖拽与点击。
		if e.IsWheel() {
			return m.handleWheel(e)
		}
		return nil
	}

	// 1) 拖拽中：处理继续保持按下时的拖动与释放。
	if m.mouseDragging {
		switch e.Action {
		case tea.MouseActionMotion:
			if e.Button == tea.MouseButtonLeft {
				m.resizeOutlineDrag(e.X)
			}
		case tea.MouseActionRelease:
			m.mouseDragging = false
		}
		return nil
	}

	// 2) 左键按在大纲右边界分离器附近 => 开始拖拽。
	if m.outlineMode && e.Action == tea.MouseActionPress &&
		e.Button == tea.MouseButtonLeft {
		w := m.outlineWidth()
		if w > 0 && e.X >= w-1 && e.X <= w+1 {
			m.mouseDragging = true
			m.mouseDragStartX = e.X
			m.mouseDragStartW = m.outlineWidth()
			m.status = T("拖动可调整大纲宽度")
			return nil
		}
	}

	// 2.5) 左键点击：Preview 模式检测超链接并打开（浏览器 / 本地文件）。
	if e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft {
		m.handlePreviewClick(e.X, e.Y)
		return nil
	}

	// 3) 滚轮翻页。
	if e.IsWheel() {
		return m.handleWheel(e)
	}
	return nil
}

// handlePreviewClick 在 Preview 模式左键点击 (x,y)（0-based 屏幕列/行）时，
// 定位点击处是否命中超链接：命中则用系统默认程序打开（http(s) 走浏览器，
// 本地路径走 xdg-open / open），并提示状态栏。
func (m *editorModel) handlePreviewClick(x, y int) {
	if m.sm.Mode() != ModePreview {
		return // 编辑/命令模式点击用于移动光标/输入，不跳转
	}
	if m.helpMode || m.cmdHelpMode || (m.fb != nil && m.fb.open) {
		return
	}
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	// 屏幕第 1 行是顶线边框，内容区从屏幕第 2 行（y=1）开始；y 越界即状态栏/边框区域
	if y < 1 || y > ch {
		return // 边框线 / 状态栏 / 命令行区域
	}
	if m.outlineMode && x < m.outlineWidth() {
		return // 大纲列
	}
	col := x - m.outlineWidth()
	if col < 0 {
		col = 0
	}
	// 屏幕行 y -> 预览行（去掉顶线偏移；previewScroll 是视口顶部视觉行，与滚动区/普通渲染均一致）
	row := m.previewScroll + (y - 1)
	if row < 0 || row >= len(m.previewRowToBuffer) {
		return
	}
	if target := m.linkAt(row, col); target != "" {
		m.openLink(target)
	}
}

// linkAt 返回预览行 row 的显示列 col 处命中的链接目标（[text](url) 的 url）。
// 列定位基于**渲染后**文本：原始行中的 markdown 标记（反引号/粗体等）在渲染后
// 消失，点击列与渲染后显示对齐（若用原始行计算列会偏移，导致链接点不准）。
// 内部复用 linkTargetAtDispCol 完成链接命中判定（含软换行偏移换算）。
func (m *editorModel) linkAt(row, col int) string {
	if row < 0 || row >= len(m.previewRowToBuffer) {
		return ""
	}
	brow := m.previewRowToBuffer[row]
	line := string(m.buf.GetLine(brow))
	if line == "" {
		return ""
	}
	// 该 buffer 行的预览视觉行（与 rebuildPreview 的渲染口径一致：RenderLine + wrapText）
	width := m.contentWidth()
	if width < 20 {
		width = 20
	}
	rendered := m.rend.RenderLine(m.buf.GetLine(brow), true, m.sm.searchKeyword)
	rlines := wrapText(rendered, width)
	if brow >= len(m.bufferToPreview) {
		return ""
	}
	startRow := m.bufferToPreview[brow]
	seg := row - startRow
	if seg < 0 || seg >= len(rlines) {
		return ""
	}
	// 点击列在该视觉段内；该段之前所有段累计的显示宽度即软换行偏移
	base := 0
	for i := 0; i < seg; i++ {
		base += fbDisplayWidth(stripANSIForWidth(rlines[i]))
	}
	return m.linkTargetAtDispCol(brow, base+col)
}

// linkTargetAtDispCol 返回 buffer 行 brow 在显示列 dispCol 处命中的链接 URL；未命中返回 ""。
// 列定位基于**渲染后**纯文本（渲染后列与预览显示一致），供鼠标点击（linkAt）与键盘
// 光标回车（预览 Enter / 编辑 Enter）统一复用。
// 跳过图片链接（![alt](url) 的 [alt](url) 部分）：图片占位不是超链接，光标停在
// 占位上回车不应跳转。
func (m *editorModel) linkTargetAtDispCol(brow, dispCol int) string {
	line := string(m.buf.GetLine(brow))
	if line == "" {
		return ""
	}
	// 原始行中按顺序提取所有链接文本与目标（跳过图片 ![alt](url)）
	type link struct{ text, url string }
	var links []link
	for _, loc := range reLink.FindAllStringIndex(line, -1) {
		if loc[0] > 0 && line[loc[0]-1] == '!' {
			continue // 图片占位，不是超链接
		}
		sub := reLink.FindStringSubmatch(line[loc[0]:loc[1]])
		if sub == nil || len(sub) < 3 {
			continue
		}
		links = append(links, link{text: sub[1], url: sub[2]})
	}
	if len(links) == 0 {
		return ""
	}
	// 在整行渲染后的纯文本中按顺序定位每个链接文本（渲染后列与预览显示一致）
	rendered := m.rend.RenderLine(m.buf.GetLine(brow), true, m.sm.searchKeyword)
	wholePlain := stripANSIForWidth(rendered)
	searchPos := 0
	for _, lk := range links {
		idx := strings.Index(wholePlain[searchPos:], lk.text)
		if idx < 0 {
			break // 链接文本渲染后变化（含嵌套格式），无法继续精确定位
		}
		abs := searchPos + idx
		colStart := fbDisplayWidth(wholePlain[:abs])
		colEnd := fbDisplayWidth(wholePlain[:abs+len(lk.text)])
		if dispCol >= colStart && dispCol < colEnd {
			return lk.url
		}
		searchPos = abs + len(lk.text)
	}
	return ""
}

// linkTargetAtCursorEdit 返回 Edit 模式（NORMAL 态）当前光标处命中的链接 URL；
// 未命中返回 ""。编辑模式光标是 rune 列，因此按原始行中 [text](url) 的 rune 列
// 区间判断（光标行显示原文 [text](url)，回车跳转语义直观）。跳过图片链接。
func (m *editorModel) linkTargetAtCursorEdit() string {
	line := string(m.buf.GetLine(m.cursorRow))
	if line == "" {
		return ""
	}
	for _, loc := range reLink.FindAllStringIndex(line, -1) {
		if loc[0] > 0 && line[loc[0]-1] == '!' {
			continue // 图片占位，不是超链接
		}
		sub := reLink.FindStringSubmatch(line[loc[0]:loc[1]])
		if sub == nil || len(sub) < 3 {
			continue
		}
		// 链接文本 [text] 的字节区间 → rune 列区间（光标列是 rune 列）。
		// loc[0] 指向 "["：text 起点 = "[" 的 rune 列 + 1；
		// loc[0]+1+len(text) 指向 "]"：text 终点（exclusive）= "]" 的 rune 列。
		// 修复：原先用 loc[0]+len(text) 指向 text 最后一个字符（"[" 占 1 字节），
		// 命中区间 [colStart, colEnd) 会漏掉 text 末尾字符；单字符链接 [a](url)
		// 区间为空、永远无法命中（Edit 模式回车打不开链接）。
		colStart := byteToRuneCol([]byte(line), loc[0]) + 1
		colEnd := byteToRuneCol([]byte(line), loc[0]+1+len(sub[1]))
		if m.cursorCol >= colStart && m.cursorCol < colEnd {
			return sub[2]
		}
	}
	return ""
}

// openLink 用系统默认程序打开链接目标：
//   - http(s):// 及其它 URL scheme（mailto: / ftp: / tel: 等）→ 系统默认程序；
//   - 裸域名 / 裸 IP（www.baidu.com、127.0.0.1:8080）→ 自动补协议后系统默认程序；
//   - 本地路径（含相对路径）→ 相对当前文件目录解析后交给系统默认程序。
//
// 打开失败不影响编辑（仅状态栏提示）。
func (m *editorModel) openLink(target string) {
	url := target
	switch {
	case isURLScheme(url):
		// http(s)://、mailto:、ftp: 等：原样交给系统默认程序
	case looksLikeBareDomain(url) || looksLikeBareIP(url):
		// 裸域名 / 裸 IP 自动补协议（带端口走 http，否则 https），
		// 避免 "www.baidu.com" 被误当本地路径导致 xdg-open 失败
		if strings.Contains(url, ":") {
			url = "http://" + url
		} else {
			url = "https://" + url
		}
	default:
		// 本地路径：相对路径基于当前文件所在目录解析
		if m.buf.filePath != "" && !filepath.IsAbs(url) {
			url = filepath.Join(filepath.Dir(m.buf.filePath), url)
		}
	}
	// 本地 markdown 文件：直接在编辑器内打开（跳转编辑），URL 则用系统浏览器打开
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && isMarkdownFile(url) {
		if err := m.loadFile(url); err != nil {
			m.status = T("无法打开链接: ") + target + " (" + err.Error() + ")"
			return
		}
		m.status = T("已打开链接: ") + target
		return
	}
	if err := launchSystemOpen(url); err != nil {
		m.status = T("无法打开链接: ") + target + " (" + err.Error() + ")"
		return
	}
	m.status = T("已打开链接: ") + target
}

// isMarkdownFile 判断是否为本地 markdown 文件（.md / .markdown）。
func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// urlSchemeRE 匹配 "scheme:..." 形式的 URL（mailto: / ftp: / tel: 等）。
// 这类目标不做本地路径解析，直接交给系统默认程序处理。
var urlSchemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func isURLScheme(s string) bool {
	return urlSchemeRE.MatchString(s)
}

// bareDomainRE 匹配裸域名：www.baidu.com / baidu.com / example.co.uk（可带路径）。
// 仅识别常见顶级域，避免把本地文件（guide.md、a.txt 等）误判为域名。
var bareDomainRE = regexp.MustCompile(`(?i)^(?:www\.)?[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:com|net|org|io|cn|co|me|dev|app|edu|gov|info|xyz|top|site|cc|vip|tv|biz|online|store|tech|club|fun|live|link|pro|name|space|world|cloud|digital|uk|jp|de|fr|ru|au|in|us|ca|eu|es|it|kr|br|mx|za|nz|sg|my|hk|tw|th|ph|vn|id|pl|se|no|fi|dk|nl|be|ch|at|pt|gr|il|ae|tr|asia|work|team|blog|life|news|media|design|agency|solutions|company|zone|games|art|film|music|email|money|care|family|chat|social|network|center|plus|city|pub|shop|group)(?::[0-9]+)?(?:/.*)?$`)

func looksLikeBareDomain(s string) bool {
	return bareDomainRE.MatchString(s)
}

// bareIPRE 匹配裸 IP：127.0.0.1 / 127.0.0.1:8080。
var bareIPRE = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]{1,3}){3}(?::[0-9]+)?$`)

func looksLikeBareIP(s string) bool {
	return bareIPRE.MatchString(s)
}

// launchSystemOpen 调用系统默认程序打开目标。
// 候选依次为 $BROWSER → xdg-open（Linux）→ open（macOS）。
//   - $BROWSER 支持冒号分隔多个候选与 "%s" 占位符（如 "firefox %s"）；直接浏览器
//     命令会保持前台运行，因此只做启动检查（Start），启动成功即视为打开。
//   - xdg-open / open 是"启动默认应用即返回"的工具，用 Run() 等待并校验退出码，
//     避免"进程启动了但实际没打开"（如无桌面环境 / 未注册 http handler 时
//     xdg-open 立即以非零码退出）被误判为成功。
func launchSystemOpen(target string) error {
	var errs []string
	if b := strings.TrimSpace(os.Getenv("BROWSER")); b != "" {
		for _, part := range strings.Split(b, ":") {
			fields := strings.Fields(part)
			if len(fields) == 0 {
				continue
			}
			args := make([]string, 0, len(fields)+1)
			hasPlaceholder := false
			for _, f := range fields {
				if strings.Contains(f, "%s") {
					args = append(args, strings.ReplaceAll(f, "%s", target))
					hasPlaceholder = true
				} else {
					args = append(args, f)
				}
			}
			if !hasPlaceholder {
				args = append(args, target)
			}
			cmd := exec.Command(args[0], args[1:]...)
			if err := cmd.Start(); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("%s: %v", part, err))
			}
		}
	}
	for _, cmdName := range []string{"xdg-open", "open"} {
		cmd := exec.Command(cmdName, target)
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", cmdName, err))
			continue
		}
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

// handleWheel 处理滚轮上下翻页（仿 vim do_mousescroll）。
//   - 无修饰键：按 mouseScrollStep 行滚动（vim mouse_vert_step）；
//   - Shift / Ctrl 修饰键：整页滚动（vim pagescroll）。
//
// 指针位于大纲列内时滚大纲高亮项（并联动正文），否则滚正文当前行/高亮行。
func (m *editorModel) handleWheel(e tea.MouseEvent) tea.Cmd {
	var dir int
	switch e.Button {
	case tea.MouseButtonWheelUp:
		dir = -1
	case tea.MouseButtonWheelDown:
		dir = 1
	}
	if dir == 0 {
		return nil
	}
	step := mouseScrollStep
	if e.Shift || e.Ctrl {
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

// outlineScroll 在大纲项列表中滚动高亮项 delta 步（并同步正文）。
func (m *editorModel) outlineScroll(delta int) {
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
func (m *editorModel) browseScroll(delta int) tea.Cmd {
	if m.sm.Mode() == ModeEdit {
		if !m.smoothScroll {
			// 兼容模式：按 buffer 行移动光标，视口跟随（老终端/习惯行为）
			m.cursorRow = clamp(m.cursorRow+delta, 0, m.buf.LineCount()-1)
			m.colKeep()
			m.ensureCursorVisible()
			return nil
		}
		m.editScrollByVisual(delta)
		return nil
	}
	if !m.smoothScroll {
		// 兼容模式：按 buffer 行移动高亮行，视口跟随
		m.previewCursor = clamp(m.previewCursor+delta, 0, m.buf.LineCount()-1)
		m.ensurePreviewCursorVisible()
		return nil
	}
	return m.previewScrollByVisual(delta)
}

// resizeOutlineDrag 在按住拖动时，按鼠标列位移实时调整大纲列宽。
func (m *editorModel) resizeOutlineDrag(x int) {
	// 新宽度 = 拖拽起点宽度 + 鼠标横向位移
	delta := x - m.mouseDragStartX
	w := m.mouseDragStartW + delta
	// 同时钳制在 [outlineMinWidth, max(outlineMaxWidth, 终端宽-正文最小宽)]
	maxW := outlineMaxWidth
	if m.width-outlineContentMin > maxW {
		maxW = m.width - outlineContentMin
	}
	w = clamp(w, outlineMinWidth, maxW)
	m.outlineW = w
	m.previewDirty = true      // 正文可用宽度变化，重新软换行
	m.invalidateScrollRender() // 宽度变化，滚动帧缓存失效
}
