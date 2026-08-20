package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TabSize 是编辑模式下 Tab 键插入的缩进宽度（空格数），与渲染嵌套层级（每 2 空格一级）保持一致。
const TabSize = 2

// editorModel 是 bubbletea 的核心 Model，整合 Buffer / Renderer / StateMachine。
type editorModel struct {
	buf    *Buffer
	rend   *Renderer
	sm     *StateMachine
	fb     *FileBrowser
	width  int
	height int
	// 编辑态光标（Buffer 行列，基于字节偏移换算为 rune 列）
	cursorRow int
	cursorCol int
	// 虚拟列：垂直移动（j/k）时记住的"目标列"，使得短行换行后列位置稳定（仿 vim curswant）
	cursWant int
	// 计数前缀：编辑模式下输入数字键累积的 count（如 "3" 在 "3j" 中）
	pendingCount int
	// 待组合的 'g' 前缀（用于 gj/gk 视觉行移动）
	pendingG bool
	// 滚动偏移（Edit 模式 viewport 顶部的 Buffer 行，仿 vim topline）
	scroll int
	// 预览模式下“当前高亮行”（Buffer 行）。与 viewport 解耦：用户可在预览中任意
	// 移动高亮行，viewport 跟随；进入编辑时 cursorRow 直接接续此行。
	previewCursor int
	// 预览模式列光标（当前高亮行渲染后的显示列，0-based）。配合 previewCursor，
	// 光标可在预览中“随意走动”，定位到超链接后回车即可跳转（URL / 本地 md 文件）。
	previewCol int
	// 预览模式视口顶部在 previewLines（视觉行）中的偏移（仿 glow viewport 的
	// yOffset / vim 的 topline）。与 scroll（Edit 模式 buffer 行 topline）解耦：
	// 预览滚动始终以“视觉行”为单位，滚轮每 tick 恰好移动 mouseScrollStep 个屏幕行
	// （glow MouseWheelDelta=3 / vim mouse_vert_step），这是“丝滑浏览”的核心。
	previewScroll int
	// 内容区终端滚动区（Preview/Command 模式，scroll_render.go）：
	//   scrollActive  内容区已注册为终端滚动区（renderer 忽略这些行，滚动由终端
	//                 DECSTBM 序列物理移动，bubbletea 不再逐行重绘，消除"闪来闪去"）；
	//   scrollContent 滚动区当前显示的内容行（与终端一致，由滚动序列更新；
	//                 内容/尺寸变化后失效，经连续性校验自动重新同步）。
	scrollActive  bool
	scrollContent []string
	// glamour 渲染缓存（Preview 模式每行的"行号+单行渲染"结果）
	previewCache string
	previewLines []string
	// preview 行 → buffer 行 的映射（block 渲染会使 preview 行数 ≠ buffer 行数）
	previewRowToBuffer []int
	// buffer 行 → 其首个 preview 行索引 的映射（用于 :数字 跳转）
	bufferToPreview []int
	previewDirty    bool
	// 状态栏消息（命令执行结果反馈）
	status string
	// 是否请求退出程序
	quitting bool
	// helpMode：键位帮助视图是否打开（由 :keymap 打开，Esc 关闭）
	helpMode bool
	// cmdHelpMode：命令模式命令帮助视图是否打开（由 :help 打开，Esc 关闭）
	cmdHelpMode bool
	// outlineMode：大纲侧边栏是否打开（默认关闭；Preview/Edit 模式按 ctrl+t 切换）。
	// 打开时主内容区宽度缩减，左侧绘制 markdown 标题目录，用于快速跳转。
	outlineMode bool
	// outlineCursor：大纲侧边栏中当前高亮项索引（0-based）。
	outlineCursor int
	// outlineItems：最近一次提取的大纲项（按 buffer 行升序，由 extractOutline 填充）。
	outlineItems []OutlineItem
	// outlineW 用户拖拽设定的大纲列宽（0 = 使用 outlineWidth 的自动默认公式）。
	// 由 mouse.go 在拖拽大纲右边界分离器时更新。范围 [outlineMinWidth, 终端宽-40]。
	outlineW int
	// 鼠标拖拽状态（mouse.go）：拖拽大纲右边界调整宽度时使用。
	mouseDragging   bool
	mouseDragStartX int // 开始拖拽时的鼠标列（0-based）
	mouseDragStartW int // 开始拖拽时的大纲列宽
	// blinkMode：硬件光标是否闪烁（默认开启；:set nocursorblink 可关闭）
	blinkMode bool
	// smoothScroll：vim 式平滑滚动是否启用（默认开启；:set nosmoothscroll 可关闭，
	// 退回"按 buffer 行移动光标"的兼容模式，老终端习惯）。
	smoothScroll bool
	// imeActive：fcitx5 等输入法是否处于激活态（由 fcitx5.go 的过滤器经
	// fcitx5IMEMsg 转发）。用于规避输入法激活期间的快捷键冲突。
	imeActive bool
	// swap：崩溃恢复用的 SwapManager（swap.go）。管理本文件 .swp 的后台写盘。
	// 为 nil 表示未启用（未命名缓冲区或无文件路径）。
	swap *SwapManager
	// sendMsg 向 bubbletea 主循环注入消息（后台图片加载完成时触发重绘）。
	// 由 main.go 在创建 Program 后注入 *tea.Program.Send。
	sendMsg func(tea.Msg)
}

// newEditorModel 构造编辑器模型。
func newEditorModel(path string) (*editorModel, error) {
	buf := NewBuffer()
	if err := buf.LoadFile(path); err != nil {
		return nil, err
	}
	rend := NewRenderer()
	rend.SetProfile(termenv.ColorProfile()) // 实际能力在 main 中按终端设置
	return &editorModel{
		buf:          buf,
		rend:         rend,
		sm:           NewStateMachine(),
		fb:           nil,
		status:       "就绪",
		previewDirty: true,
		blinkMode:    true,             // 光标闪烁默认开启
		smoothScroll: true,             // vim 式行滚动默认开启
		sendMsg:      func(tea.Msg) {}, // 占位；main.go 会注入真实 *Program.Send
	}, nil
}

// Init 实现 bubbletea.Model（无异步启动任务）。
func (m *editorModel) Init() tea.Cmd { return tea.ShowCursor }

// runeColToByte 将 rune 列转换为字节列，确保光标落在正确字节起点。
func runeColToByte(line []byte, col int) int {
	bc := 0
	rc := 0
	for bc < len(line) && rc < col {
		_, size := decodeRune(line[bc:])
		bc += size
		rc++
	}
	return bc
}

// byteToRuneCol 将字节列转换为 rune 列。
func byteToRuneCol(line []byte, bc int) int {
	rc := 0
	i := 0
	for i < bc && i < len(line) {
		_, size := decodeRune(line[i:])
		i += size
		rc++
	}
	return rc
}

// swapTickMsg 是 SwapManager 后台线程投递给 UI 线程的“需要采集快照”的信号。
// 由 model.Update 处理：在编辑线程（UI）内安全采集快照并交给后台写盘，
// 从而保证采集快照与 Buffer 编辑发生在同一 goroutine，无数据竞争。
type swapTickMsg struct{}

// Update 处理所有消息（键盘、窗口尺寸、退出等）。
func (m *editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.previewDirty = true      // 宽度变化需要重新软换行
		m.invalidateScrollRender() // 视口变化，滚动帧缓存失效
		// 窗口尺寸变化：内容区高度改变，滚动区边界失效，先清除让 bubbletea 恢复渲染
		return m, m.clearScrollRegionCmd()

	case tea.MouseMsg:
		// 鼠标支持（mouse.go）：滚轮翻阅 + 拖拽大纲右边界调整宽度。
		// 返回滚动区命令（Preview 滚动平滑化）。
		return m, m.handleMouse(msg)

	case swapTickMsg:
		// 后台请求采集快照（编辑线程）：交给 SwapManager 在 UI 线程内安全采集。
		if m.swap != nil {
			m.swap.Produce()
		}
		return m, nil

	case fcitx5IMEMsg:
		// fcitx5 输入法激活/反激活通知（来自 fcitx5.go 过滤器）。
		// 激活时记录状态：编辑模式下跳过单字母快捷键拦截，让候选词字母
		// 直接写入文本，避免输入法选词字符被当作命令吞掉。
		m.imeActive = msg.Active
		return m, nil

	case tea.KeyMsg:
		// 键位帮助视图打开时，仅响应 Esc 关闭（其余按键忽略）
		if m.helpMode {
			if msg.String() == "esc" {
				m.helpMode = false
				m.status = T("已关闭键位帮助")
			}
			return m, nil
		}
		// 命令模式帮助视图打开时，仅响应 Esc 关闭（其余按键忽略）
		if m.cmdHelpMode {
			if msg.String() == "esc" {
				m.cmdHelpMode = false
				m.status = T("已关闭命令帮助")
			}
			return m, nil
		}
		// 文件浏览器打开时，事件优先交给浏览器处理
		if m.fb != nil && m.fb.open {
			return m.handleFileBrowserKey(msg)
		}
		// 集中控制硬件光标：编辑模式隐藏光标（用反显块表示位置），
		// 预览/命令模式显示光标并定位到当前行（由 renderPreview 注入定位序列）。
		var cmd tea.Cmd
		switch m.sm.Mode() {
		case ModePreview:
			m2, c := m.handlePreviewKey(msg)
			m = m2.(*editorModel)
			cmd = c
		case ModeEdit:
			m2, c := m.handleEditKey(msg)
			m = m2.(*editorModel)
			cmd = c
		case ModeCommand:
			m2, c := m.handleCommandKey(msg)
			m = m2.(*editorModel)
			cmd = c
		}
		if m.sm.Mode() == ModePreview {
			// 预览模式隐藏硬件光标（View 亦输出 ?25l 兜底），靠蓝底块指示当前行；
			// 状态栏常驻显示，但硬件光标不落在状态栏位置。
			cmd = tea.Batch(cmd, tea.HideCursor)
		} else if m.sm.Mode() == ModeEdit {
			// 编辑模式同样隐藏硬件光标：光标行已由 renderEdit 注入蓝底块
			// （RenderEditLineWithCursorStyled / insertCursor）指示输入位置，
			// 若再显示硬件光标会与蓝底块重叠成“双光标”。
			cmd = tea.Batch(cmd, tea.HideCursor)
		} else {
			// 命令模式显示硬件光标（命令行输入框定位）
			cmd = tea.Batch(cmd, tea.ShowCursor)
		}
		return m, cmd

	case tea.QuitMsg:
		m.quitting = true
		return m, tea.Quit

	case imgReadyMsg:
		// 后台图片加载完成：标记 Preview 需要重建，下一次 View 即可渲染真实图片。
		// 注意：切换回 Edit 模式后无需重建，这里仅在 Preview 时生效（rebuildPreview 自带脏检查）。
		if m.sm.Mode() == ModePreview {
			m.previewDirty = true
			m.invalidateScrollRender() // 图片渲染变化，滚动帧缓存失效
			// 图片内容变化 → 滚动区内容失效，清除让 bubbletea 恢复渲染
			m.scrollActive = false
			m.scrollContent = nil
		}
		return m, nil
	}
	return m, nil
}

// ----------------------------------------------------------
// Preview 模式按键处理
// ----------------------------------------------------------
func (m *editorModel) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "i", "e", "a": // KeyI/KeyE/KeyA 见 keymap.go（编辑模式进入）
		// 进入编辑模式：光标接续到预览当前高亮行（i/e 行首插入，a 行尾插入）
		if err := m.sm.EnterEdit(); err == nil {
			m.cursorRow = clamp(m.previewCursor, 0, m.buf.LineCount()-1)
			if msg.String() == "a" {
				m.cursorCol = byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
			} else {
				m.cursorCol = 0
			}
			m.cursWant = m.cursorCol
			// 让 Edit 视口（scroll）跟随光标行，进入编辑立刻可见（仿 vim）
			m.ensureCursorVisible()
			m.status = T("INSERT（Esc 进入 NORMAL 导航态）")
			// 离开预览渲染：清除内容区滚动区，交还 bubbletea 渲染
			return m, m.clearScrollRegionCmd()
		}
	case "o", "O": // 在光标行下方/上方插入空行并进入插入模式（仿 vim o/O）
		if err := m.sm.EnterEdit(); err == nil {
			if msg.String() == "O" {
				m.cursorRow = m.buf.OpenLineAbove(m.previewCursor)
			} else {
				m.cursorRow = m.buf.OpenLineBelow(m.previewCursor)
			}
			m.cursorCol = 0
			m.cursWant = m.cursorCol
			// 让 Edit 视口（scroll）跟随光标行，进入编辑立刻可见（仿 vim）
			m.ensureCursorVisible()
			m.status = T("INSERT（Esc 进入 NORMAL 导航态）")
			// 离开预览渲染：清除内容区滚动区，交还 bubbletea 渲染
			return m, m.clearScrollRegionCmd()
		}
	case KeyColon: // : 进入命令模式（keymap: preview.command）
		_ = m.sm.EnterCommand()
		// 命令模式多占一行命令行，内容区高度变化，滚动区失效
		return m, m.clearScrollRegionCmd()
	case KeySlash: // / 进入搜索模式（keymap: preview.search）
		_ = m.sm.EnterSearch()
		// 搜索高亮变化 + 高度变化，滚动区失效
		return m, m.clearScrollRegionCmd()
	case KeyUp, "k": // 上移高亮行（keymap: preview.up）
		m.previewCursor = max(0, m.previewCursor-1)
		m.previewColKeep()
		m.ensurePreviewCursorVisible()
	case KeyDown, "j": // 下移高亮行（keymap: preview.down）
		m.previewCursor = min(m.buf.LineCount()-1, m.previewCursor+1)
		m.previewColKeep()
		m.ensurePreviewCursorVisible()
	case KeyLeft, "h": // 左移列光标（keymap: preview.left）
		m.previewCol = max(0, m.previewCol-1)
	case KeyRight, "l": // 右移列光标（keymap: preview.right）
		m.previewCol = min(m.previewLineDisplayWidth(m.previewCursor), m.previewCol+1)
	case KeyEnter: // 回车：光标停在超链接上则跳转（URL / 本地 md 文件），否则下移（keymap: preview.down）
		if t := m.linkTargetAtDispCol(m.previewCursor, m.previewCol); t != "" {
			m.openLink(t)
			return m, nil
		}
		m.previewCursor = min(m.buf.LineCount()-1, m.previewCursor+1)
		m.previewColKeep()
		m.ensurePreviewCursorVisible()
	case KeyPgUp: // 上翻页（keymap: preview.pageUp）：按视觉行翻页，光标移到新窗口顶部（仿 vim C-b）
		ch := visibleLines(m)
		if ch < 1 {
			ch = 1
		}
		if len(m.previewLines) > 0 {
			m.previewScroll = clamp(m.previewScroll-max(1, ch-1), 0, max(0, len(m.previewLines)-ch))
			m.setPreviewCursorToRow(m.previewScroll)
		}
	case KeyPgDown: // 下翻页（keymap: preview.pageDown）：按视觉行翻页，光标移到新窗口底部（仿 vim C-f）
		ch := visibleLines(m)
		if ch < 1 {
			ch = 1
		}
		if len(m.previewLines) > 0 {
			m.previewScroll = clamp(m.previewScroll+max(1, ch-1), 0, max(0, len(m.previewLines)-ch))
			m.setPreviewCursorToRow(m.previewScroll + ch - 1)
		}
	}
	// 光标移动后重建渲染：使“光标是否落在代码块/表格内”的降级逻辑即时生效
	// （进入块→显示原始文本，离开块→立即恢复渲染），避免块级宽内容导致飘逸。
	m.previewDirty = true
	return m, nil
}

// ----------------------------------------------------------
// Edit 模式按键处理
// ----------------------------------------------------------
func (m *editorModel) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	// Esc 语义（仿 vim + 用户需求“Esc 在 编辑↔预览 之间切换”）：
	//   - INSERT 态：退回 NORMAL 导航态（仿 vim，README 键位表约定）；
	//   - NORMAL 态：返回预览模式（用户需求）。
	if msg.Type == tea.KeyEsc {
		if m.sm.EditSub() != editNormal {
			m.sm.SetEditSub(editNormal)
			m.status = T("NORMAL（Esc 退出编辑）")
			return m, nil
		}
		m.sm.ExitToPreview()
		// 同步预览高亮行到当前编辑行，保证回到预览时光标位置连续
		m.previewCursor = clamp(m.cursorRow, 0, m.buf.LineCount()-1)
		m.ensurePreviewCursorVisible()
		m.status = T("已退出编辑（Esc）")
		return m, tea.HideCursor
	}
	// 输入法（fcitx5）激活时，普通字母按键会被 normal 子态的字母快捷键（i/a/j/k…）
	// 误吞，造成“随机插入乱序”内容。此处把所有普通字符直接转入插入态写入文本，
	// 仅在 IME 激活期间生效，不影响无输入法时的正常 vim 风格快捷键。
	if m.imeActive && msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.sm.SetEditSub(editInsert)
		return m.handleInsertKey(msg)
	}
	// 编辑模式内部再分 插入态(editInsert) 与 普通导航态(editNormal)（仿 vim INSERT/NORMAL）。
	if m.sm.EditSub() == editInsert {
		return m.handleInsertKey(msg)
	}
	return m.handleNormalKey(msg)
}

// ----------------------------------------------------------
// 编辑-插入态：键入即写入（与旧 Edit 行为一致）
// ----------------------------------------------------------
func (m *editorModel) handleInsertKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	line := m.buf.GetLine(m.cursorRow)
	byteCol := runeColToByte(line, m.cursorCol)

	switch msg.Type {
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			m.buf.InsertRune(m.cursorRow, byteCol, r)
			// 先推进光标列，再据此重算插入点字节偏移：否则下一轮会用“旧光标列”
			// 计算出的字节偏移（仍指向行首），把后续字符插到前一个字符之前，
			// 表现为中文等多 rune 提交时下标乱序（如“你好”变成“好你”）。
			m.cursorCol++
			line = m.buf.GetLine(m.cursorRow)
			byteCol = runeColToByte(line, m.cursorCol)
		}
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeySpace:
		m.buf.InsertRune(m.cursorRow, byteCol, ' ')
		m.cursorCol++
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeyTab:
		// Tab 缩进：在光标处插入一个缩进单位（默认 2 空格，与渲染嵌套层级一致）
		for i := 0; i < TabSize; i++ {
			m.buf.InsertRune(m.cursorRow, byteCol, ' ')
			m.cursorCol++
			byteCol++
		}
		m.cursWant = m.cursorCol
		m.status = T("已插入缩进")
		m.touch()
	case tea.KeyShiftTab:
		// Shift+Tab 反缩进：从光标向左删除最多 TabSize 个空格（越行首则不删）
		removed := 0
		for removed < TabSize && m.cursorCol > 0 {
			line := m.buf.GetLine(m.cursorRow)
			bc := runeColToByte(line, m.cursorCol-1)
			if bc < len(line) && line[bc] == ' ' {
				m.buf.DeleteRune(m.cursorRow, bc)
				m.cursorCol--
				removed++
			} else {
				break
			}
		}
		m.cursWant = m.cursorCol
		if removed > 0 {
			m.status = T("已反缩进")
			m.touch()
		}

	case tea.KeyEnter:
		nr, nc := m.buf.InsertNewline(m.cursorRow, byteCol)
		m.cursorRow, m.cursorCol = nr, nc
		m.cursWant = m.cursorCol
		m.touch()

	case tea.KeyBackspace, tea.KeyDelete:
		if m.cursorCol > 0 {
			m.buf.Backspace(m.cursorRow, byteCol)
			m.cursorCol--
		} else if m.cursorRow > 0 {
			// 行首退格：合并上一行
			prevLen := byteToRuneCol(m.buf.GetLine(m.cursorRow-1), len(m.buf.GetLine(m.cursorRow-1)))
			m.buf.Backspace(m.cursorRow, byteCol)
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
			m.cursorCol = byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
		}
		m.cursWant = m.cursorCol
	case tea.KeyRight:
		if line != nil && m.cursorCol < byteToRuneCol(line, len(line)) {
			m.cursorCol++
		} else if m.cursorRow < m.buf.LineCount()-1 {
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
		if m.cursorRow < m.buf.LineCount()-1 {
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
func (m *editorModel) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		case "j", KeyDown: // keymap: normal.down（组合态：gj 下移一个屏幕行）
			for c := 0; c < count && m.visualLineStep(1); c++ {
			}
			m.pendingG = false
			m.pendingCount = 0
			m.ensureCursorVisible()
			return m, nil
		case "k", KeyUp: // keymap: normal.up（组合态：gk 上移一个屏幕行）
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
		col := firstNonBlank(m.buf.GetLine(m.cursorRow))
		m.enterInsertAt(col)
		return m, nil
	case "A":
		// 行尾插入
		col := byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
		m.enterInsertAt(col)
		return m, nil
	case "o":
		// 在下方新建一行并插入（仿 vim 'o'）
		nr := m.buf.OpenLineBelow(m.cursorRow)
		m.cursorRow = nr
		m.cursorCol = 0
		m.enterInsertAt(0)
		m.touch()
		return m, nil
	case "O":
		// 在上方新建一行并插入（仿 vim 'O'）
		nr := m.buf.OpenLineAbove(m.cursorRow)
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
		if m.buf.Undo() {
			m.status = T("已撤销")
		} else {
			m.status = T("无可撤销操作")
		}
		m.clampCursor() // 撤销可能收缩行内容，先把光标钳制到合法范围（仿 vim check_cursor）
		m.cursWant = m.cursorCol
		m.ensureCursorVisible()
		return m, nil

	// ---- 导航 ----
	case "h", KeyLeft: // keymap: normal.left
		m.cursorCol = max(0, m.cursorCol-count)
		m.cursWant = m.cursorCol
	case "l", KeyRight, KeySpace: // keymap: normal.right
		maxc := byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
		m.cursorCol = min(maxc, m.cursorCol+count)
		m.cursWant = m.cursorCol
	case "k", KeyUp: // keymap: normal.up
		m.cursorRow = max(0, m.cursorRow-count)
		m.colKeep()
	case "j", KeyDown: // keymap: normal.down
		m.cursorRow = min(m.buf.LineCount()-1, m.cursorRow+count)
		m.colKeep()
	case KeyEnter: // 回车：光标停在超链接上则跳转（URL / 本地 md 文件），否则下移（keymap: normal.down）
		if t := m.linkTargetAtCursorEdit(); t != "" {
			m.openLink(t)
		} else {
			m.cursorRow = min(m.buf.LineCount()-1, m.cursorRow+count)
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
		m.cursorRow = m.buf.LineCount() - 1
		m.colKeep()
	case "{":
		m.moveParagraph(-count)
	case "}":
		m.moveParagraph(count)
	case "(":
		m.moveSentence(-count)
	case ")":
		m.moveSentence(count)
	case KeyCtrlD: // keymap: normal.halfDown
		m.scrollHalfPage(count)
	case KeyCtrlU: // keymap: normal.halfUp
		m.scrollHalfPage(-count)
	case KeyCtrlF: // keymap: normal.pageDown
		m.scrollHalfPageVisible(1)
	case KeyCtrlB: // keymap: normal.pageUp
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
func (m *editorModel) enterInsertAt(col int) {
	maxc := byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
	m.cursorCol = clamp(col, 0, maxc)
	m.cursWant = m.cursorCol
	m.sm.SetEditSub(editInsert)
	m.status = T("INSERT（Esc 回到 NORMAL）")
}

// colKeep 垂直移动后按虚拟列 cursWant 维持列位置（仿 vim coladvance）。
func (m *editorModel) colKeep() {
	maxc := byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
	if m.cursWant > maxc {
		m.cursorCol = maxc
	} else {
		m.cursorCol = m.cursWant
	}
}

// moveToLineStart 跳到行首（'0'）。
func (m *editorModel) moveToLineStart() {
	m.cursorCol = 0
	m.cursWant = 0
}

// moveToFirstNonBlank 跳到行首第一个非空白（'^'）。
func (m *editorModel) moveToFirstNonBlank() {
	m.cursorCol = firstNonBlank(m.buf.GetLine(m.cursorRow))
	m.cursWant = m.cursorCol
}

// moveToLineEnd 跳到行尾（'$'）。
func (m *editorModel) moveToLineEnd() {
	m.cursorCol = byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
	m.cursWant = m.cursorCol
}

// editAvailWidth 返回 Edit 模式下内容区可用列宽（与 renderEdit / ensureCursorVisible 的
// 软换行逻辑保持一致：行号 gutter 占 5 列）。
func (m *editorModel) editAvailWidth() int {
	av := m.contentWidth()
	if m.sm.LineNumMode() != lnNone {
		av -= 5
	}
	if av < 10 {
		av = 10
	}
	return av
}

// screenPos 返回当前光标所在 buffer 行的“视觉行”信息（仿 vim 的 w_wrow/w_wcol，
// 用于 gj/gk 的屏幕行移动）：
//   - wrapped：该行软换行后的视觉行列表（wrapText 输出）；
//   - seg：光标位于第几个视觉行（0-based）；
//   - visCol：光标在该视觉行内的显示列（0-based，按终端单元格计，CJK 占 2 列）。
func (m *editorModel) screenPos() (wrapped []string, seg, visCol int) {
	av := m.editAvailWidth()
	line := string(m.buf.GetLine(m.cursorRow))
	wrapped = wrapText(line, av)
	// 计算光标前所有 rune 的显示宽度（与 editAvailWidth 的列口径一致）
	prefixW := 0
	rc := 0
	for _, r := range line {
		if rc >= m.cursorCol {
			break
		}
		prefixW += cellWidth(r)
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
		w := cellWidth(r)
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
// 与 j/k（按 buffer 行移动）不同，这里尊重软换行：光标行若因超宽被 wrapText 拆成
// 多个视觉行，则先在同一 buffer 行的相邻视觉行间移动；到该行最后/首个视觉行后才
// 跨到相邻 buffer 行的首/末视觉行，并尽量保持同一显示列 visCol。
// 无法再移动（已到文件首/末屏行）时返回 false，光标保持不变。
func (m *editorModel) visualLineStep(dir int) bool {
	n := m.buf.LineCount()
	av := m.editAvailWidth()
	wrapped, seg, visCol := m.screenPos()
	row := m.cursorRow

	if dir > 0 { // 下移一个屏幕行
		if seg+1 < len(wrapped) {
			// 同一 buffer 行的下一个视觉行
			m.cursorCol = runeColAtDisplay(m.buf.GetLine(row), (seg+1)*av+visCol)
			return true
		}
		if row+1 >= n {
			return false // 已到文件末屏行
		}
		// 跨到下一 buffer 行的首个视觉行（显示列仍按 visCol 对齐）
		m.cursorRow = row + 1
		m.cursorCol = runeColAtDisplay(m.buf.GetLine(m.cursorRow), visCol)
		return true
	}

	// 上移一个屏幕行
	if seg > 0 {
		m.cursorCol = runeColAtDisplay(m.buf.GetLine(row), (seg-1)*av+visCol)
		return true
	}
	if row <= 0 {
		return false // 已到文件首屏行
	}
	// 跨到上一 buffer 行的最后一个视觉行（显示列按 visCol 对齐）
	prev := string(m.buf.GetLine(row - 1))
	pw := wrapText(prev, av)
	m.cursorRow = row - 1
	m.cursorCol = runeColAtDisplay(m.buf.GetLine(m.cursorRow), (len(pw)-1)*av+visCol)
	return true
}

// clampCursor 把光标钳制到合法范围（仿 vim 的 check_cursor / validate_cursor）：
// cursorRow 限制在 [0, 行数-1]，cursorCol 限制在 [0, 当前行 rune 数]。
// 用于编辑/撤销导致行内容收缩后，防止光标悬停在行尾空白甚至越界位置。
func (m *editorModel) clampCursor() {
	n := m.buf.LineCount()
	if n <= 0 {
		n = 1
	}
	if m.cursorRow < 0 {
		m.cursorRow = 0
	}
	if m.cursorRow >= n {
		m.cursorRow = n - 1
	}
	maxc := byteToRuneCol(m.buf.GetLine(m.cursorRow), len(m.buf.GetLine(m.cursorRow)))
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
func (m *editorModel) moveWord(count int, big bool) {
	for c := 0; c < count; c++ {
		if !m.stepWordForward(big) {
			break
		}
	}
	m.cursWant = m.cursorCol
}

// moveBackWord 向后移动到第 count 个词首（b / B）。
func (m *editorModel) moveBackWord(count int, big bool) {
	for c := 0; c < count; c++ {
		if !m.stepWordBackward(big) {
			break
		}
	}
	m.cursWant = m.cursorCol
}

// moveWordEnd 向前移动到第 count 个词尾（e / E）。
func (m *editorModel) moveWordEnd(count int, big bool) {
	for c := 0; c < count; c++ {
		if !m.stepWordEnd(big) {
			break
		}
	}
	m.cursWant = m.cursorCol
}

// stepWordForward 仿 vim 'w'：跳过当前词及后续空白，停在下一个词首。
func (m *editorModel) stepWordForward(big bool) bool {
	n := m.buf.LineCount()
	for row := m.cursorRow; row < n; row++ {
		runes := []rune(string(m.buf.GetLine(row)))
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
func (m *editorModel) stepWordBackward(big bool) bool {
	for row := m.cursorRow; row >= 0; row-- {
		runes := []rune(string(m.buf.GetLine(row)))
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
func (m *editorModel) stepWordEnd(big bool) bool {
	n := m.buf.LineCount()
	for row := m.cursorRow; row < n; row++ {
		runes := []rune(string(m.buf.GetLine(row)))
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
func (m *editorModel) moveParagraph(count int) {
	n := m.buf.LineCount()
	if count > 0 {
		// '}'：向下找到第 count 个段落起始（空行之后的首个非空行）
		for c := 0; c < count; c++ {
			row := m.cursorRow + 1
			for row < n && strings.TrimSpace(string(m.buf.GetLine(row))) != "" {
				row++
			}
			for row < n && strings.TrimSpace(string(m.buf.GetLine(row))) == "" {
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
			for row > 0 && strings.TrimSpace(string(m.buf.GetLine(row))) != "" {
				row--
			}
			for row > 0 && strings.TrimSpace(string(m.buf.GetLine(row))) == "" {
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
func (m *editorModel) moveSentence(count int) {
	n := m.buf.LineCount()
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
func (m *editorModel) stepSentenceForward() bool {
	n := m.buf.LineCount()
	for row := m.cursorRow; row < n; row++ {
		runes := []rune(string(m.buf.GetLine(row)))
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
func (m *editorModel) stepSentenceBackward() bool {
	for row := m.cursorRow; row >= 0; row-- {
		runes := []rune(string(m.buf.GetLine(row)))
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
func (m *editorModel) scrollHalfPage(count int) {
	ch := visibleLines(m)
	if ch <= 0 {
		ch = 1
	}
	m.editScrollByVisual(max(1, ch/2) * count)
}

// scrollHalfPageVisible 仿 vim Ctrl+F / Ctrl+B：视口以视觉行滚动整屏（保留一行重叠），
// 光标移到新窗口顶部/底部（仿 vim）。
func (m *editorModel) scrollHalfPageVisible(dir int) {
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
func (m *editorModel) centerCursor() {
	ch := visibleLines(m)
	m.scroll = clamp(m.cursorRow-ch/2, 0, max(0, m.buf.LineCount()-ch))
}
func (m *editorModel) topCursor() {
	m.scroll = clamp(m.cursorRow, 0, max(0, m.buf.LineCount()-visibleLines(m)))
}
func (m *editorModel) bottomCursor() {
	ch := visibleLines(m)
	m.scroll = clamp(m.cursorRow-ch+1, 0, max(0, m.buf.LineCount()-ch))
}

// deleteForward 仿 vim 'x'：删除光标下 count 个字符。
func (m *editorModel) deleteForward(count int) {
	line := m.buf.GetLine(m.cursorRow)
	maxc := byteToRuneCol(line, len(line))
	for i := 0; i < count && m.cursorCol < maxc; i++ {
		bc := runeColToByte(m.buf.GetLine(m.cursorRow), m.cursorCol)
		m.buf.Backspace(m.cursorRow, bc+1) // 删除光标右侧字符
	}
	m.cursWant = m.cursorCol
	m.touch()
}

// deleteBackward 仿 vim 'X'：删除光标左侧 count 个字符。
func (m *editorModel) deleteBackward(count int) {
	for i := 0; i < count && m.cursorCol > 0; i++ {
		bc := runeColToByte(m.buf.GetLine(m.cursorRow), m.cursorCol)
		m.buf.Backspace(m.cursorRow, bc)
		m.cursorCol--
	}
	m.cursWant = m.cursorCol
	m.touch()
}

// deleteWord 仿 vim 'dw'：删除从光标到下一个词首的 count 个词（跨行则删除到下一行行首）。
func (m *editorModel) deleteWord(count int) {
	n := m.buf.LineCount()
	for c := 0; c < count; c++ {
		line := m.buf.GetLine(m.cursorRow)
		if line == nil {
			break
		}
		// 先移动到下一个词首（或行尾）
		beforeRow, beforeCol := m.cursorRow, m.cursorCol
		if !m.stepWordForward(false) {
			// 已到文末：删除到当前行尾剩余内容
			bc := runeColToByte(m.buf.GetLine(beforeRow), beforeCol)
			for m.cursorCol < byteToRuneCol(m.buf.GetLine(beforeRow), len(m.buf.GetLine(beforeRow))) {
				m.buf.DeleteRune(beforeRow, bc)
				m.cursorCol = byteToRuneCol(m.buf.GetLine(beforeRow), bc)
			}
			break
		}
		// 删除 [before, now) 之间的内容（可能跨行）
		for (m.cursorRow > beforeRow) || (m.cursorRow == beforeRow && m.cursorCol > beforeCol) {
			if m.cursorRow == beforeRow {
				bc := runeColToByte(m.buf.GetLine(m.cursorRow), beforeCol)
				m.buf.DeleteRune(m.cursorRow, bc)
				m.cursorCol = byteToRuneCol(m.buf.GetLine(m.cursorRow), bc)
				beforeCol++
			} else {
				// 跨行：删除下一行整行（退化为逐字符删到行首）
				m.buf.DeleteLine(beforeRow + 1)
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
func (m *editorModel) deleteLine(count int) {
	n := m.buf.LineCount()
	end := min(n-1, m.cursorRow+count-1)
	for row := end; row >= m.cursorRow; row-- {
		m.buf.DeleteLine(row)
	}
	if m.cursorRow >= m.buf.LineCount() {
		m.cursorRow = max(0, m.buf.LineCount()-1)
	}
	m.cursorCol = 0
	m.cursWant = 0
	m.touch()
}

// ensureCursorVisible 调整 Edit 模式的 scroll，使 cursorRow 落在可视区域内。
// 关键：渲染时单行会因软换行展开为多行视觉行，因此必须按“视觉行”而非“Buffer 行”
// 计算滚动窗口，否则光标行的软换行尾部会溢出到状态栏/界面之外。
func (m *editorModel) ensureCursorVisible() {
	// 先钳制光标到合法范围（仿 vim validate_cursor），再做基于视觉行的滚动计算。
	m.clampCursor()
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	// 估算某 buffer 行的视觉行数（与 renderEdit 的 wrapText 一致：可用宽度 = 内容区宽 - 行号占位）。
	av := m.contentWidth()
	if m.sm.LineNumMode() != lnNone {
		av -= 5
	}
	if av < 10 {
		av = 10
	}
	visOf := func(i int) int {
		n := len(wrapText(string(m.buf.GetLine(i)), av))
		if n < 1 {
			n = 1
		}
		return n
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
	if m.scroll >= m.buf.LineCount() {
		m.scroll = max(0, m.buf.LineCount()-1)
	}
}

// renderLineNum 生成左侧行号前缀。
// lnNone: 空；lnAbs: 绝对行号（i+1）；lnRel: 当前光标行显示绝对行号，其余显示距光标行距离。
func renderLineNum(ln lineNumMode, row, cursorRow int) string {
	var n int
	switch ln {
	case lnAbs:
		n = row + 1
	case lnRel:
		if row == cursorRow {
			n = cursorRow + 1 // 当前行显示绝对行号
		} else {
			n = abs(row - cursorRow)
		}
	default:
		return ""
	}
	// 焦黄色显示行号：普通行 214（琥珀金），当前光标行 228（更亮的焦黄）以突出焦点
	fg := lipgloss.Color("214")
	if row == cursorRow {
		fg = lipgloss.Color("228")
	}
	return lipgloss.NewStyle().Foreground(fg).Render(fmt.Sprintf("%4d ", n))
}

// abs 返回整数的绝对值（Go 1.21+ 有内置 abs，但保留以兼容旧版本）。
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// expandPercent 将命令行参数中的 `%` 展开为当前文件名，与 Vim 的 cmdline 变量语义保持一致：
//   - `%`           展开为当前缓冲区文件名（Vim 的 curbuf->b_fname）。
//   - `\%`          反斜杠转义，保留字面 `%`（Vim 中同样用反斜杠取消特殊含义）。
//
// loadFile 加载文件到当前缓冲区并重置光标/视口（供 :e 重新加载与
// 光标回车跳转本地 md 文件复用）。
func (m *editorModel) loadFile(target string) error {
	if err := m.buf.LoadFile(target); err != nil {
		return err
	}
	m.scroll = 0
	m.previewScroll = 0
	m.previewCursor = 0
	m.previewCol = 0
	m.previewDirty = true
	return nil
}

//   - 缓冲区无文件名时 `%` 展开为空字符串（Vim 中 b_fname==NULL 时 result 为空，
//     由调用方按"无文件名"语义判断是否报错，等价于 Vim 的 check_fname）。
//
// 注意：仅在「文件名参数」上下文展开（如 :w / :e / :r 之后的参数），不在命令名本身展开。
func expandPercent(s, fname string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '%' {
			b.WriteRune('%') // 转义：保留字面 %
			i++
			continue
		}
		if runes[i] == '%' {
			b.WriteString(fname) // 展开为当前文件名
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

// ----------------------------------------------------------
// Command 模式按键处理
// ----------------------------------------------------------
func (m *editorModel) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc: // keymap: command.cancel
		m.sm.ExitToPreview()
		m.status = T("已取消命令")
		return m, nil

	case tea.KeyEnter: // keymap: command.execute
		return m, m.executeCommand(m.sm.cmdInput)

	case tea.KeyBackspace, tea.KeyDelete: // keymap: command.backspace
		m.sm.BackspaceCmd()

	case tea.KeySpace: // keymap: command.space
		m.sm.AppendCmd(' ')

	case tea.KeyRunes: // keymap: command.rune
		for _, r := range msg.Runes {
			m.sm.AppendCmd(r)
		}
	}
	return m, nil
}

// executeCommand 解析并执行命令模式输入（返回可选的 quit 命令）。
func (m *editorModel) executeCommand(input string) tea.Cmd {
	cmd := strings.TrimSpace(input)
	m.sm.ExitToPreview()

	switch {
	case cmd == ":q":
		if m.buf.IsDirty {
			m.status = T("有未保存改动，使用 :q! 强制退出")
		} else {
			m.quitting = true
			return tea.Quit
		}
	case cmd == ":q!":
		m.quitting = true
		return tea.Quit

	case cmd == ":w":
		// 保存到当前文件：若启动时未指定文件名，提示用 :w 文件名 创建
		if err := m.buf.Save(false); err != nil {
			m.status = T("保存失败: ") + err.Error()
		} else {
			m.status = T("已保存 ") + m.buf.filePath
		}
	case cmd == ":w!" || cmd == ":wq!" || cmd == ":wq":
		// 强制保存（含退出）：文件不存在时自动创建
		if err := m.buf.Save(true); err != nil {
			m.status = T("保存失败: ") + err.Error()
		} else {
			m.status = T("已保存 ") + m.buf.filePath
			if cmd == ":wq" || cmd == ":wq!" {
				m.quitting = true
				return tea.Quit
			}
		}

	case strings.HasPrefix(cmd, ":w") || strings.HasPrefix(cmd, ":wq"):
		// 带文件名参数的保存：:w 文件名 / :w! 文件名 / :wq 文件名 / :wq! 文件名
		// 例如 ":w test.md" 会创建（或覆盖）test.md 并设为当前文件。
		// 提取冒号后第一个 token 之后的文件名部分。
		rest := strings.TrimSpace(cmd[2:]) // 去掉 ":w" 或 ":wq" 前缀
		// 去掉可能的强制符 '!'
		rest = strings.TrimPrefix(rest, "!")
		rest = strings.TrimSpace(rest)
		// 与 Vim 一致：文件名参数中的 `%` 展开为当前文件名；无文件名时展开为空。
		fname := expandPercent(rest, m.buf.filePath)
		if rest == "%" && m.buf.filePath == "" {
			m.status = T("无文件名（缓冲区未命名，使用 :w 文件名 指定）")
			break
		}
		if fname == "" {
			m.status = T("用法: :w 文件名（创建/另存为）")
			break
		}
		if err := m.buf.SaveAs(fname); err != nil {
			m.status = T("保存失败: ") + err.Error()
		} else {
			m.status = T("已保存为 ") + fname
			// :wq / :wq! 带文件名 => 保存后退出
			if strings.HasPrefix(cmd, ":wq") {
				m.quitting = true
				return tea.Quit
			}
		}

	case cmd == ":u":
		if m.buf.Undo() {
			m.status = T("已撤销")
		} else {
			m.status = T("无可撤销操作")
		}

	case cmd == ":set nu", cmd == ":set number":
		m.sm.SetLineNum(lnAbs)
		m.status = T("行号显示: 绝对行号 (nu)")
	case cmd == ":set rnu", cmd == ":set relativenumber":
		m.sm.SetLineNum(lnRel)
		m.status = T("行号显示: 相对行号 (rnu)")
	case cmd == ":set nonu", cmd == ":set nonumber", cmd == ":set norelativenumber":
		m.sm.SetLineNum(lnNone)
		m.status = T("行号显示: 关闭 (nonu)")
	case cmd == ":set cursorblink":
		m.blinkMode = true
		m.status = T("光标闪烁: 开启")
	case cmd == ":set nocursorblink":
		m.blinkMode = false
		m.status = T("光标闪烁: 关闭")
	case cmd == ":set fileicons":
		fbUseIcons = true
		m.status = T("文件浏览器图标: 开启（需 Nerd Font 终端）")
	case cmd == ":set nofileicons":
		fbUseIcons = false
		m.status = T("文件浏览器图标: 关闭")
	case cmd == ":set smoothscroll":
		m.smoothScroll = true
		m.status = T("平滑行滚动: 开启（vim 式行刷新）")
	case cmd == ":set nosmoothscroll":
		m.smoothScroll = false
		m.invalidateScrollRender() // 停用平滑滚动并清滚动缓存
		m.status = T("平滑行滚动: 关闭（老终端兼容，改为全量重绘）")

	case cmd == ":ex":
		// 打开极简文件浏览器
		m.fb = NewFileBrowser(".")
		m.fb.Open()
		m.status = T("文件浏览器已打开")

	case cmd == ":help":
		// 打开命令模式命令帮助视图（RenderCommandHelp），Esc 关闭
		m.cmdHelpMode = true
		m.status = T("命令模式帮助（Esc 关闭）")
	case cmd == ":keymap":
		// 打开键位帮助视图（集中注册于 keymap.go），Esc 关闭
		m.helpMode = true
		m.status = T("键位帮助（Esc 关闭）")

	case cmd == ":toc":
		// 打开/关闭大纲目录侧边栏（默认关闭，等价 ctrl+t）
		m.toggleOutline()

	case cmd == ":e" || cmd == ":e!" || strings.HasPrefix(cmd, ":e "):
		// :e [文件名] / :e! [文件名]：重新加载文件（与 Vim 一致）。
		// 文件名参数中的 `%` 展开为当前文件名，故 `:e %` 等价于「重开当前文件」。
		// 有未保存改动时 `:e` 拒绝（"未保存的修改"），`:e!` 强制丢弃改动重载。
		arg := strings.TrimSpace(strings.TrimPrefix(cmd, ":e"))
		arg = strings.TrimPrefix(arg, "!") // 去掉强制符
		arg = strings.TrimSpace(arg)
		// 无参 :e 等价于 :e %（重开当前文件）；否则把参数中的 % 展开为当前文件名。
		var target string
		if arg == "" {
			target = m.buf.filePath
		} else {
			target = expandPercent(arg, m.buf.filePath)
		}
		if target == "" {
			m.status = T("无文件名（缓冲区未命名，使用 :e 文件名 指定）")
			break
		}
		if !m.buf.IsDirty || strings.HasPrefix(cmd, ":e!") {
			if err := m.loadFile(target); err != nil {
				m.status = T("加载失败: ") + err.Error()
			} else {
				m.status = T("已重新加载 ") + target
			}
		} else {
			m.status = T("未保存的修改，使用 :e! 强制重载")
		}

	case strings.HasPrefix(cmd, ":") && isNumber(cmd[1:]):
		// :数字 跳转行
		n, _ := strconv.Atoi(cmd[1:])
		n-- // 1-based -> 0-based
		if n < 0 {
			n = 0
		}
		if n >= m.buf.LineCount() {
			n = m.buf.LineCount() - 1
		}
		m.previewCursor = n
		// 视口居中定位到目标行（仿 vim zz / glow 的 EnsureVisible）
		m.centerPreviewOnCursor()
		m.status = Tf("跳转到第 %d 行", n+1)

	// ---- 删除命令（当前行以 Preview 高亮行为准，命令模式只能从 Preview 进入）----
	case cmd == ":%d":
		// 清空当前文件所有内容（保留一行空行）。
		last := m.buf.LineCount() - 1
		for row := last; row >= 0; row-- {
			m.buf.DeleteLine(row)
		}
		m.previewCursor = 0
		m.cursorRow = 0
		m.scroll = 0
		m.previewScroll = 0
		m.previewDirty = true
		m.status = T("已清空当前文件所有内容")

	case strings.HasPrefix(cmd, ":dk"), strings.HasPrefix(cmd, ":dj"):
		// :dk 行号 / :dj 行号（行号 1-based）：从当前行向上(k)/向下(j)删除到指定行（含两端）。
		// 也接受无空格写法，如 :dk12 / :dj2。
		dir := cmd[2:3] // 'k' 或 'j'
		rest := strings.TrimSpace(cmd[3:])
		var target int
		if rest == "" {
			// 无行号：k 删到文件首行，j 删到文件末行。
			if dir == "k" {
				target = 0
			} else {
				target = m.buf.LineCount() - 1
			}
		} else if _, err := fmt.Sscanf(rest, "%d", &target); err != nil || target < 1 {
			m.status = T("用法: :dk 行号 / :dj 行号（行号为 1-based）")
			break
		} else {
			target-- // 1-based -> 0-based
		}
		cur := m.previewCursor
		from := min(cur, target)
		to := max(cur, target)
		for row := to; row >= from; row-- {
			m.buf.DeleteLine(row)
		}
		m.previewCursor = clamp(from, 0, m.buf.LineCount()-1)
		m.cursorRow = m.previewCursor
		m.scroll = 0
		m.previewScroll = 0
		m.previewDirty = true
		m.status = Tf("已删除第 %d-%d 行", from+1, to+1)

	case cmd == ":d", cmd == ":dd":
		// 删除整行（当前行）。:d 与 :dd 等价，均删除当前高亮行。
		cur := m.previewCursor
		m.buf.DeleteLine(cur)
		m.previewCursor = clamp(cur, 0, m.buf.LineCount()-1)
		m.cursorRow = m.previewCursor
		m.previewDirty = true
		m.status = Tf("已删除第 %d 行", cur+1)

	case cmd == ":%":
		// 单独输入 `:%` 时，与 Vim 中 `%`=当前文件名的语义一致：显示当前文件名。
		// （Vim 中 `%` 也作行范围 1,$ 使用，但本编辑器无带地址命令，此处以「当前文件」语义呈现。）
		if m.buf.filePath == "" {
			m.status = T("当前文件: （未命名缓冲区）")
		} else {
			m.status = T("当前文件: ") + m.buf.filePath
		}

	case strings.HasPrefix(cmd, "/"):
		// /关键字 搜索：记录关键字并跳到首个匹配 Buffer 行（previewLines 与 buffer 1:1）
		kw := strings.TrimSpace(cmd[1:])
		if kw == "" {
			m.status = T("搜索关键字为空")
			break
		}
		m.sm.searchKeyword = kw
		if row := m.findFirstMatch(kw); row >= 0 {
			m.previewCursor = row
			// 视口居中定位到匹配行（仿 vim zz）
			m.centerPreviewOnCursor()
			m.status = Tf("匹配 '%s' 于第 %d 行", kw, row+1)
		} else {
			m.status = Tf("未找到 '%s'", kw)
		}

	default:
		m.status = T("未知命令: ") + cmd
	}
	// 打开极简文件浏览器属于从 Preview 全屏切换到另一种全屏视图，
	// 必须先清除 Preview 模式遗留的终端物理滚动区（DECSTBM），
	// 否则该滚动区的 diff 缓存会与新两栏内容叠加，造成「预览残留与文件列表混合」。
	if m.fb.open {
		m.invalidateScrollRender()
		return m.clearScrollRegionCmd()
	}
	return nil
}

// findFirstMatch 从当前光标位置向后查找首个包含关键字的行。
func (m *editorModel) findFirstMatch(kw string) int {
	kw = strings.ToLower(kw)
	start := m.previewCursor
	if start < 0 {
		start = 0
	}
	for i := start; i < m.buf.LineCount(); i++ {
		if strings.Contains(strings.ToLower(string(m.buf.GetLine(i))), kw) {
			return i
		}
	}
	// 环形回绕：从头再找一次
	for i := 0; i < start; i++ {
		if strings.Contains(strings.ToLower(string(m.buf.GetLine(i))), kw) {
			return i
		}
	}
	return -1
}

// (findPreviewMatch 已删除：previewLines 现已与 buffer 行 1:1，复用 findFirstMatch 即可)

// ----------------------------------------------------------
// 文件浏览器按键处理
// ----------------------------------------------------------
func (m *editorModel) handleFileBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fb := m.fb
	switch fb.mode {
	case fbInput:
		// 输入名称子模式：捕获字符、退格、光标移动、回车提交、Esc 取消
		switch msg.Type {
		case tea.KeyEsc:
			fb.Cancel()
			m.status = T("已取消")
		case tea.KeyEnter:
			m.status = fb.SubmitInput()
		case tea.KeyBackspace:
			fb.BackspaceInput()
		case tea.KeyDelete:
			fb.DeleteForward()
		case tea.KeyLeft:
			fb.MoveInput(-1)
		case tea.KeyRight:
			fb.MoveInput(1)
		case tea.KeyHome:
			fb.InputJump(false)
		case tea.KeyEnd:
			fb.InputJump(true)
		case tea.KeyRunes:
			fb.AppendInput(msg.Runes)
		}
		return m, nil
	case fbConfirm:
		// 确认删除子模式：y/Y/enter 确认，n/N/esc 取消
		switch msg.Type {
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				switch msg.Runes[0] {
				case 'y', 'Y':
					m.status = fb.ConfirmDelete(true)
					return m, nil
				case 'n', 'N':
					m.status = fb.ConfirmDelete(false)
					return m, nil
				}
			}
		case tea.KeyEnter:
			m.status = fb.ConfirmDelete(true)
		case tea.KeyEsc:
			m.status = fb.ConfirmDelete(false)
		}
		return m, nil
	default: // fbList 列表浏览模式
		switch msg.String() {
		case KeyEsc: // keymap: fb.cancel
			fb.Close()
			m.status = T("已退出文件浏览器")
		case "tab": // keymap: fb.toggleFocus
			// 焦点在左栏列表 / 右栏预览之间切换
			fb.ToggleFocus()
		case "j", KeyDown: // keymap: fb.down
			if fb.focusRight {
				fb.ScrollPreview(1) // 右栏：滚动预览
			} else {
				fb.Move(1) // 左栏：移动光标
			}
		case "k", KeyUp: // keymap: fb.up
			if fb.focusRight {
				fb.ScrollPreview(-1) // 右栏：滚动预览
			} else {
				fb.Move(-1) // 左栏：移动光标
			}
		case KeyPgDown: // keymap: fb.pageDown
			if fb.focusRight {
				fb.ScrollPreview(visibleLines(m) - 2) // 右栏：半屏/整页下滚
			} else {
				fb.Move(visibleLines(m) - 2)
			}
		case KeyPgUp: // keymap: fb.pageUp
			if fb.focusRight {
				fb.ScrollPreview(-(visibleLines(m) - 2)) // 右栏：整页上滚
			} else {
				fb.Move(-(visibleLines(m) - 2))
			}
		case KeyEnter: // keymap: fb.confirm
			if path := fb.Confirm(); path != "" {
				// 选定文件：关闭浏览器并加载该文件
				fb.Close()
				if err := m.buf.LoadFile(path); err != nil {
					m.status = T("打开失败: ") + err.Error()
				} else {
					m.scroll = 0
					m.previewScroll = 0
					m.previewCursor = 0
					m.previewDirty = true // 强制重建 Preview 缓存，否则会显示旧文件内容
					m.status = T("已打开 ") + path
				}
			}
		case "d": // keymap: fb.mkdir
			// 创建文件夹（输入名称后回车确认）
			fb.StartCreate("dir")
			m.status = T("输入文件夹名后回车确认")
		case "t": // keymap: fb.mkfile
			// 创建文件（输入名称后回车确认）
			fb.StartCreate("file")
			m.status = T("输入文件名后回车确认")
		case "r": // keymap: fb.delete
			// 删除光标选中项（弹确认框）
			if s := fb.StartDelete(); s != "" {
				m.status = s
			}
		case "m": // keymap: fb.rename
			// 重命名光标选中项（进入输入模式，预填原名）
			if s := fb.StartRename(); s != "" {
				m.status = s
			} else {
				m.status = T("输入新名称后回车确认")
			}
		}
		return m, nil
	}
}

// ----------------------------------------------------------
// View 渲染
// ----------------------------------------------------------
func (m *editorModel) View() string {
	if m.fb != nil && m.fb.open {
		// 文件浏览器：两栏主体 + 底部状态栏（位置/输入提示写入状态栏，避免行数叠加错乱）。
		// 强隔离：传 height-3 让 Render 内部产出 height-4 行主体（它自行预留状态栏 1 行），
		// 再由本分支拼接 顶线+分隔线+状态栏+底线，与其他视图保持一致的“上下分明”边框布局；
		// 完全不依赖 contentHeight()（后者会随 Preview/Command 模式变化，曾导致两栏布局被预览状态干扰）。
		body := m.fb.Render(m.width, m.height-3)
		switch m.fb.mode {
		case fbInput:
			// 输入名称：在状态栏显示 label + 带反显光标块的输入缓冲
			r := []rune(m.fb.inputBuf)
			if m.fb.inputPos > len(r) {
				m.fb.inputPos = len(r)
			}
			head := string(r[:m.fb.inputPos])
			tail := string(r[m.fb.inputPos:])
			cursorBlock := lipgloss.NewStyle().Reverse(true).Render(" ")
			m.status = m.fb.inputLabel + head + cursorBlock + tail
		case fbConfirm:
			m.status = T("确认删除 '") + m.fb.pendingDelete + T("'? [y/n]")
		default:
			m.status = T("位置: ") + m.fb.dir
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
		return m.fixBottom(RenderKeyMapHelp(), "") + "\x1b[r"
	}

	if m.cmdHelpMode {
		// 命令模式命令帮助视图：渲染所有命令及用法（keymap.go），状态栏贴底
		return m.fixBottom(RenderCommandHelp(), "") + "\x1b[r"
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
		cursorRow := -1 // 预览/命令模式下当前行的绝对行号，用于将硬件光标定位过去
		switch m.sm.Mode() {
		case ModePreview:
			s, row := m.renderPreview()
			body.WriteString(s)
			cursorRow = row
		case ModeEdit:
			s := m.renderEdit()
			body.WriteString(s)
		case ModeCommand:
			s, row := m.renderPreview()
			body.WriteString(s)
			cursorRow = row
		}
		// 命令模式下，命令行内容（底部第二行）；否则为空
		cmdLine := ""
		if m.sm.Mode() == ModeCommand {
			cmdLine = m.renderCmdLine()
		}
		bodyStr := body.String()
		// 大纲侧边栏打开时，把大纲列与正文逐行并排（总宽 = 终端宽，状态栏仍贴底）
		if m.outlineMode && m.outlineWidth() > 0 {
			bodyStr = m.composeOutlineWithBody(bodyStr)
		}
		rendered = m.fixBottom(bodyStr, cmdLine)
		// 光标定位策略：
		//  - 编辑模式：光标行已由 renderEdit 注入蓝底块（RenderEditLineWithCursorStyled/
		//    insertCursor）指示输入位置，不再输出硬件光标，避免蓝底块+白光标双光标重叠。
		//  - 命令模式：预览当前行 + 命令行输入框都需要硬件光标，定位到预览当前行文本起点。
		//  - 预览模式：状态栏常驻（与内容区隔离），仍隐藏硬件光标，仅用 injectPreviewCursor
		//    注入的蓝底块指示当前行位置，避免硬件光标残留在底部状态栏位置。
		// 光标行号 +1：顶部有 1 行边框线（frameLine 顶线），内容区从屏幕第 2 行开始。
		if m.sm.Mode() == ModePreview {
			rendered += hideCursor()
		} else if m.sm.Mode() == ModeEdit {
			rendered += hideCursor()
		} else if cursorRow > 0 {
			rendered += cursorGoto(cursorRow+1, 6, m.blinkMode)
		}
	}()

	// 末尾追加滚动区恢复保险：任何情况下都恢复全屏滚动区，防止滚动帧异常残留
	// DECSTBM 导致后续渲染持续触发终端滚动（界面逐步上移/消失）。
	return rendered + "\x1b[r"
}

// fallbackView 在渲染 panic 兜底时返回一个尽量简单、不可能再 panic 的视图，
// 保证终端不会被破坏，用户仍可操作（切换模式/退出）。
func (m *editorModel) fallbackView() string {
	var b strings.Builder
	top := m.scroll
	if m.sm.Mode() == ModePreview || m.sm.Mode() == ModeCommand {
		// Preview/Command 以预览光标行为视口锚点（纯文本兜底，无需视觉行映射）
		top = m.previewCursor
		if top < 0 {
			top = 0
		}
	}
	end := min(m.buf.LineCount(), top+visibleLines(m))
	for i := top; i < end; i++ {
		b.WriteString(string(m.buf.GetLine(i)))
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
func (m *editorModel) fixBottom(body, cmdLine string) string {
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

// frameLine 渲染一条全宽边框线（+-----+），用于把底部状态栏区域与内容区彻底分隔。
// 使用纯 ASCII 的 + 和 -，避免 Unicode 盒线字符在中文 locale 下被按 2 列渲染而撑破行宽。
func (m *editorModel) frameLine(l, r string) string {
	w := m.width
	if w < 3 {
		w = 3
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	return style.Render(l + strings.Repeat("-", w-2) + r)
}

// renderCmdLine 渲染命令/搜索输入框（仿 vim 的命令行，显示在最底部，独占整行）。
// renderPreview 渲染 Preview 模式。
// 采用 block-aware 渲染：普通行逐行富文本（1:1 对应 buffer 行，带行号），
// 块级语法（代码块/表格等）整块 glamour 渲染；块内多行共享首个 buffer 行号。
// 返回值第二项为当前行在可视区内的 1-based 绝对行号（用于把硬件光标定位过去），
// 若当前行不在可视区内则返回 -1。
func (m *editorModel) renderPreview() (string, int) {
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
		if m.sm.Mode() == ModePreview && m.previewRowToBuffer[i] == m.previewCursor && i >= cursorIdx {
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

// ensurePreviewCursorVisible 让 viewport(scroll)跟随 previewCursor：
// 若当前高亮行（蓝底块）在可视区之外，则平移 scroll 使其进入可视区。
// 关键：preview 中一个 buffer 行可能展开成多个 preview 视觉行（代码块/表格/软换行），
// 因此必须基于 preview 视觉行索引判断，否则蓝底光标块会被推到窗口之外。
func (m *editorModel) ensurePreviewCursorVisible() {
	ch := visibleLines(m)
	if ch < 1 {
		ch = 1
	}
	if len(m.previewLines) == 0 {
		return
	}
	m.clampPreviewScroll()
	if m.previewCursor < 0 || m.previewCursor >= m.buf.LineCount() {
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
func (m *editorModel) previewColKeep() {
	m.previewCol = clamp(m.previewCol, 0, m.previewLineDisplayWidth(m.previewCursor))
}

// previewLineDisplayWidth 返回预览中 buffer 行 brow 渲染后的显示宽度
// （不含行号前缀；与 linkTargetAtDispCol 的列口径一致，供 h/l 列移动钳制）。
func (m *editorModel) previewLineDisplayWidth(brow int) int {
	if brow < 0 || brow >= m.buf.LineCount() {
		return 0
	}
	line := m.buf.GetLine(brow)
	if len(line) == 0 {
		return 0
	}
	rendered := m.rend.RenderLine(line, true, m.sm.searchKeyword)
	return fbDisplayWidth(stripANSIForWidth(rendered))
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
func (m *editorModel) injectPreviewColCursor(lineOut string, row int) string {
	if m.previewCol < 0 {
		return lineOut
	}
	if row < 0 || row >= len(m.previewRowToBuffer) {
		return lineOut
	}
	brow := m.previewRowToBuffer[row]
	if brow < 0 || brow >= m.buf.LineCount() || brow >= len(m.bufferToPreview) {
		return lineOut
	}
	width := m.contentWidth()
	if width < 20 {
		width = 20
	}
	rendered := m.rend.RenderLine(m.buf.GetLine(brow), true, m.sm.searchKeyword)
	rlines := wrapText(rendered, width)
	startRow := m.bufferToPreview[brow]
	seg := row - startRow
	if seg < 0 || seg >= len(rlines) {
		return lineOut
	}
	// 该视觉段之前所有段累计的显示宽度 = 软换行偏移；previewCol 减去后即段内列。
	base := 0
	for i := 0; i < seg; i++ {
		base += fbDisplayWidth(stripANSIForWidth(rlines[i]))
	}
	local := m.previewCol - base
	segPlain := stripANSIForWidth(rlines[seg])
	if local < 0 || local >= fbDisplayWidth(segPlain) {
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
		if fbDisplayWidth(string(r)) >= 2 {
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

// rebuildPreview 构建 Preview 渲染缓存（block-aware + 行号前缀 + 映射表）。
//
// 渲染策略：
//   - 代码块（```...```）、表格（| ... |）、数学块（$$...$$）、脚注定义（[^id]:）
//     等跨行块语法整块送 glamour 渲染（RenderBlock），保证表格对齐/代码高亮等正确。
//   - 普通行逐行 RenderLine（标题/列表/引用/段落），并渲染行内语法（粗体/链接/emoji…）。
//   - 每个 buffer 行对应一个预览块；块内多行渲染结果共享该 buffer 行号（行号列
//     仅首行显示，其余留空），维持与编辑态的 1:1 行号对应。
func (m *editorModel) rebuildPreview() {
	// 行号列宽：仅在编辑模式（ModeEdit）下渲染行号；预览模式为沉浸式阅读，
	// 不应渲染行号（否则行号前缀会被 markdown 语法误解析/显示错乱）。
	const editLineNumCol = 5 // 与 renderLineNum 的 "%4d " 列宽（5 列）保持一致
	ln := lnNone
	lineNumCol := 0
	if m.sm.Mode() == ModeEdit {
		ln = m.sm.LineNumMode()
		if ln != lnNone {
			lineNumCol = editLineNumCol
		}
	}
	blankPrefix := strings.Repeat(" ", lineNumCol)
	width := m.contentWidth() - lineNumCol
	if width < 20 {
		width = 20
	}
	// 让 renderer 知道可用宽度：分隔线/标题下划线等占满整行
	m.rend.SetWrap(width)

	// 预览模式光标所在的 buffer 行：若落在代码块/表格范围内，则该块降级为原始文本渲染，
	// 规避块级宽内容（超长代码行/宽表格）被终端硬折行导致的滚动飘逸。仅预览模式生效。
	cursorBuf := -1
	if m.sm.Mode() == ModePreview {
		cursorBuf = m.previewCursor
	}

	lines := m.buf.lines
	var outLines []string
	var rowToBuf []int
	var bufToPrev = make([]int, len(lines))

	i := 0
	for i < len(lines) {
		brow := i
		lineStr := string(lines[i])
		trimmed := strings.TrimSpace(lineStr)
		nextLine := ""
		if i+1 < len(lines) {
			nextLine = string(lines[i+1])
		}

		// 行号前缀（仅块首行显示 buffer 行号）
		prefix := renderLineNum(ln, brow, brow)
		if prefix == "" {
			prefix = blankPrefix
		}

		switch {
		case isFenceStart(trimmed):
			// 代码块：收集到结束 fence（含）
			j := i + 1
			for j < len(lines) && !isFenceEnd(strings.TrimSpace(string(lines[j]))) {
				j++
			}
			if j >= len(lines) {
				j = len(lines) - 1 // 无结束 fence，渲染到文末
			}
			// 预览模式下，若光标（高亮行）落在代码块范围内，则整块降级为“原始文本逐行渲染”，
			// 不触发 chroma 矩形/块级渲染。块级宽内容（超长代码行、表格对齐）是飘逸的根源，
			// 光标悬停时只看源码文本可彻底规避硬折行错位；光标离开后（rebuildPreview 重建）立即恢复渲染。
			if m.sm.Mode() == ModePreview && cursorBuf >= i && cursorBuf <= j {
				for r := i; r <= j; r++ {
					rp := renderLineNum(ln, r, r)
					if rp == "" {
						rp = blankPrefix
					}
					rl := m.rend.RenderLine(lines[r], true, m.sm.searchKeyword)
					rlines := wrapText(rl, width)
					for k, seg := range rlines {
						p := rp
						if k > 0 {
							p = blankPrefix
						}
						outLines = append(outLines, p+seg)
						rowToBuf = append(rowToBuf, r)
					}
					bufToPrev[r] = len(outLines) - len(rlines)
				}
				i = j + 1
				continue
			}
			rawInner := byteLinesToStrings(lines[i+1 : j])
			// 代码块不走高亮由 glamour 渲染：其 dark 风格用"灰色前景空格铺满整行"伪装灰底，
			// 无法裁成自适应宽度矩形。改为 chroma 直接高亮（无背景），再由 renderCodeRect
			// 套真正的灰底矩形，宽度 = 原始代码内容宽度。
			fenceStart := strings.TrimSpace(string(lines[i]))
			lang := strings.TrimPrefix(fenceStart, "```")
			lang = strings.TrimPrefix(lang, "~~~")
			code := strings.Join(rawInner, "\n")
			rlines := highlightCode(lang, code)
			rect := renderCodeRect(rlines, rawInner, prefix, blankPrefix, width-contentWidth(prefix)-1)
			baseOut := len(outLines)
			for _, rl := range rect {
				outLines = append(outLines, rl)
				rowToBuf = append(rowToBuf, brow)
			}
			// 用实际追加行数回溯映射（软换行续行归属同一 buffer 行，取块首行）。
			bufToPrev[brow] = baseOut
			i = j + 1
			continue

		case isTableStart(trimmed, nextLine):
			// 表格：收集连续的含 | 行
			j := i
			for j+1 < len(lines) && strings.Contains(string(lines[j+1]), "|") && strings.TrimSpace(string(lines[j+1])) != "" {
				j++
			}
			// 预览模式下，若光标落在表格范围内，则整块降级为“原始文本逐行渲染”，
			// 不触发 glamour 表格渲染（超宽表格对齐行被终端硬折行是飘逸根源之一）。
			if m.sm.Mode() == ModePreview && cursorBuf >= i && cursorBuf <= j {
				for r := i; r <= j; r++ {
					rp := renderLineNum(ln, r, r)
					if rp == "" {
						rp = blankPrefix
					}
					rl := m.rend.RenderLine(lines[r], true, m.sm.searchKeyword)
					rlines := wrapText(rl, width)
					for k, seg := range rlines {
						p := rp
						if k > 0 {
							p = blankPrefix
						}
						outLines = append(outLines, p+seg)
						rowToBuf = append(rowToBuf, r)
					}
					bufToPrev[r] = len(outLines) - len(rlines)
				}
				i = j + 1
				continue
			}
			block := strings.Join(byteLinesToStrings(lines[i:j+1]), "\n")
			rendered := m.rend.RenderBlock(block, width)
			rlines := strings.Split(rendered, "\n")
			aw := width - contentWidth(prefix)
			if aw < 4 {
				aw = 4
			}
			baseOut := len(outLines)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				// 按可用宽度软换行，避免超宽表格行被终端硬折行（导致滚动偏移错位/飘逸）。
				for _, seg := range wrapText(rl, aw) {
					outLines = append(outLines, p+seg)
					rowToBuf = append(rowToBuf, brow)
				}
			}
			// 用实际追加行数回溯映射（软换行续行也归属同一 buffer 行，故取块首行）。
			bufToPrev[brow] = baseOut
			i = j + 1
			continue

		case isMathBlock(trimmed):
			if isSingleLineMath(trimmed) {
				// 单行显示公式 $$...$$：仅当前行作为块渲染
				block := lineStr
				rendered := m.rend.RenderBlock(block, width)
				rlines := strings.Split(rendered, "\n")
				aw := width - contentWidth(prefix)
				if aw < 4 {
					aw = 4
				}
				baseOut := len(outLines)
				for k, rl := range rlines {
					p := prefix
					if k > 0 {
						p = blankPrefix
					}
					for _, seg := range wrapText(rl, aw) {
						outLines = append(outLines, p+seg)
						rowToBuf = append(rowToBuf, brow)
					}
				}
				bufToPrev[brow] = baseOut
				i++
				continue
			}
			// 多行数学块 $$ ... $$
			j := i + 1
			for j < len(lines) && !isMathBlockDelim(strings.TrimSpace(string(lines[j]))) {
				j++
			}
			if j >= len(lines) {
				j = len(lines) - 1
			}
			block := strings.Join(byteLinesToStrings(lines[i:j+1]), "\n")
			rendered := m.rend.RenderBlock(block, width)
			rlines := strings.Split(rendered, "\n")
			aw := width - contentWidth(prefix)
			if aw < 4 {
				aw = 4
			}
			baseOut := len(outLines)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				for _, seg := range wrapText(rl, aw) {
					outLines = append(outLines, p+seg)
					rowToBuf = append(rowToBuf, brow)
				}
			}
			bufToPrev[brow] = baseOut
			i = j + 1
			continue

		case isDefListLine(trimmed):
			// 定义列表：含前导术语行 + 后续 : 定义行，整块送 glamour 渲染
			start := i
			if start > 0 {
				prev := strings.TrimSpace(string(lines[start-1]))
				if isDefTermLine(prev) {
					start--
				}
			}
			j := i
			for j+1 < len(lines) && isDefListLine(strings.TrimSpace(string(lines[j+1]))) {
				j++
			}
			block := strings.Join(byteLinesToStrings(lines[start:j+1]), "\n")
			rendered := m.rend.RenderBlock(block, width)
			rlines := strings.Split(rendered, "\n")
			aw := width - contentWidth(prefix)
			if aw < 4 {
				aw = 4
			}
			baseOut := len(outLines)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				for _, seg := range wrapText(rl, aw) {
					outLines = append(outLines, p+seg)
					rowToBuf = append(rowToBuf, start)
				}
			}
			bufToPrev[start] = baseOut
			i = j + 1
			continue

		case func() bool {
			_, _, ok := extractBlockImage(trimmed)
			return ok
		}():
			// 独立成行的图片 ![alt](url)：用 🖼 占位符显示（图形预览已关闭，见下方说明）。
			alt, url, _ := extractBlockImage(trimmed)
			note := "🖼 " + alt + " (" + url + ")"
			ph := m.rend.styles.Img.Render(note)
			outLines = append(outLines, prefix+ph)
			rowToBuf = append(rowToBuf, brow)
			bufToPrev[brow] = len(outLines) - 1
			i++
			continue

		default:
			// 普通行：逐行富文本渲染（含行内语法），再软换行到可用宽度
			rendered := m.rend.RenderLine(lines[i], true, m.sm.searchKeyword)
			rlines := wrapText(rendered, width)
			for k, rl := range rlines {
				p := prefix
				if k > 0 {
					p = blankPrefix
				}
				outLines = append(outLines, p+rl)
				rowToBuf = append(rowToBuf, brow)
			}
			bufToPrev[brow] = len(outLines) - len(rlines)
			i++
		}
	}

	m.previewLines = outLines
	m.previewRowToBuffer = rowToBuf
	m.bufferToPreview = bufToPrev

	// 内容/宽度变化后把视口偏移钳制到新边界（位置尽量保留，收缩到合法范围）
	m.clampPreviewScroll()
}

// renderEdit 渲染 Edit 模式（WYSIWYG + 光标精确列对齐）。
//   - 非光标行：与 Preview 一致，富文本渲染（语法行着色）。
//   - 光标行：调用 RenderEditLineWithCursor，仅对语法前缀上色，光标用反显空格标记，
//     确保光标所在显示列与输入字符严格对齐，零输入延迟。
func (m *editorModel) renderEdit() string {
	var b strings.Builder
	lines := m.buf.LineCount()
	// 当前行背景（仿 vim cursorline）：普通态用偏蓝灰，插入态用偏暗的强调色
	cursorBg := lipgloss.Color("236")
	if m.sm.EditSub() == editNormal {
		cursorBg = lipgloss.Color("238")
	}
	cursorLineStyle := lipgloss.NewStyle().Background(cursorBg)
	// 续行/未编辑行的 gutter 占位（与 renderLineNum 的 "%4d " 列宽一致，均为 5 列）。
	// 无行号时必须为空串，否则续行被多塞 5 个空格，窄终端下整行超宽触发硬折行。
	gutterEdit := ""
	if m.sm.LineNumMode() != lnNone {
		gutterEdit = strings.Repeat(" ", 5)
	}
	// 可用宽度：内容区宽度减去行号 gutter（无行号时 gutter 为 0，内容占满整行）
	availWidth := m.contentWidth()
	if m.sm.LineNumMode() != lnNone {
		availWidth -= 5
	}
	if availWidth < 10 {
		availWidth = 10
	}
	// 让 renderer 知道可用宽度：分隔线/标题下划线等占满整行
	m.rend.SetWrap(availWidth)
	ch := visibleLines(m)
	vLines := 0 // 已占用的视觉行数
	for i := m.scroll; i < lines; i++ {
		line := m.buf.GetLine(i)
		// 代码块检测：```` ``` ```` 或 ~~~ 围栏起始行，收集到结束围栏整块送 glamour
		// 渲染（chroma 语法高亮 + 语言标注 + 代码背景），否则编辑模式下代码块内容行
		// 会被逐行当普通文本渲染，完全没有高亮（与 Preview 行为不一致）。
		if isFenceStart(strings.TrimSpace(string(line))) {
			j := i + 1
			for j < lines && !isFenceEnd(strings.TrimSpace(string(m.buf.GetLine(j)))) {
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
						s = m.rend.RenderEditLineWithCursor(m.buf.GetLine(brow), m.cursorCol)
					} else {
						s = string(m.buf.GetLine(brow))
					}
					raw = append(raw, s)
				}
				firstP, lidP := "", gutterEdit
				if ln := m.sm.LineNumMode(); ln != lnNone {
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
				block = append(block, string(m.buf.GetLine(k)))
			}
			rawInner := block[1 : len(block)-1] // 去掉首尾围栏行
			fenceStart := strings.TrimSpace(block[0])
			lang := strings.TrimPrefix(fenceStart, "```")
			lang = strings.TrimPrefix(lang, "~~~")
			code := strings.Join(rawInner, "\n")
			rb := highlightCode(lang, code)
			// 自适应宽度的完整灰底矩形：首行带行号 gutter，其余行用 gutterEdit 对齐，
			// 上/下盖勾出轮廓，右侧按最宽行补齐，形成闭合矩形而非铺满整行。
			firstP, lidP := "", gutterEdit
			if ln := m.sm.LineNumMode(); ln != lnNone {
				firstP = renderLineNum(ln, i, m.cursorRow)
			}
			rect := renderCodeRect(rb, rawInner, firstP, lidP, availWidth-1)
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
		if i+1 < lines && isTableStart(string(line), string(m.buf.GetLine(i+1))) {
			tbl := []string{string(line), string(m.buf.GetLine(i + 1))}
			j := i + 2
			for j < lines {
				r2 := strings.TrimSpace(string(m.buf.GetLine(j)))
				if r2 != "" && strings.Contains(r2, "|") {
					tbl = append(tbl, string(m.buf.GetLine(j)))
					j++
				} else {
					break
				}
			}
			block := strings.Join(tbl, "\n")
			rendered := m.rend.RenderBlock(block, availWidth)
			for k, rl := range strings.Split(rendered, "\n") {
				brow := i + k // 表格块内第 k 行对应的 buffer 行（glamour 输出与 buffer 行 1:1）
				isCursor := brow == m.cursorRow
				numPrefix := ""
				if ln := m.sm.LineNumMode(); ln != lnNone {
					if k == 0 {
						numPrefix = renderLineNum(ln, i, m.cursorRow)
					} else {
						numPrefix = gutterEdit
					}
				}
				var lineOut string
				if isCursor {
					// 光标行：富文本 + 行内标记着色 + 反显光标块，使光标列与输入位置
					// 严格对齐，同时“渲染”出 markdown 语法（不再显示纯原文）。
					lineOut = cursorLineStyle.Render(numPrefix + m.rend.RenderEditLineWithCursorStyled(m.buf.GetLine(brow), m.cursorCol))
				} else {
					lineOut = numPrefix + rl
				}
				// 若加入会越过状态栏且本行不是光标行，则停止（光标行始终完整渲染）
				if vLines+1 > ch && !isCursor {
					break
				}
				b.WriteString(lineOut + "\n")
				vLines++
			}
			// 越界检查（确保不覆盖状态栏/命令行）
			if vLines >= ch {
				break
			}
			i = j - 1 // 跳过块内其余行，下一轮从该块之后开始
			continue
		}
		var rendered string
		if i == m.cursorRow {
			// 光标行：富文本 + 行内标记着色 + 反显光标块（保持行宽、光标列严格对齐），
			// 使进入编辑模式后 markdown 语法立即“渲染”出来，不再呈现纯原文。
			rendered = m.rend.RenderEditLineWithCursorStyled(line, m.cursorCol)
			rendered = cursorLineStyle.Render(rendered)
		} else {
			// 非光标行：与 Preview 一致富文本
			rendered = m.rend.RenderLine(line, true, m.sm.searchKeyword)
		}
		// 软换行到可用宽度，使编辑内容占满终端宽度
		wrapped := wrapText(rendered, availWidth)
		// 若加入本行会越过状态栏所在行，且本行不是光标行，则停止。
		// 光标行始终完整渲染，确保其全部换行都落在状态栏之上，不被覆盖。
		if vLines+len(wrapped) > ch && i != m.cursorRow {
			break
		}
		// 行号前缀（仅首行显示，续行用空白占位，保持标题下划线等与行号列对齐）
		numPrefix := ""
		if ln := m.sm.LineNumMode(); ln != lnNone {
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

// ----------------------------------------------------------
// 工具函数
// ----------------------------------------------------------
// touch 标记缓冲区内容已修改，并使 Preview 渲染缓存失效（下次进入预览时重建）。
func (m *editorModel) touch() {
	m.buf.IsDirty = true
	m.previewDirty = true
	// 内容变化：内容区滚动区缓存失效（下次滚动经连续性校验自动重新同步/重绘）。
	m.invalidateScrollRender()
	m.scrollActive = false
	m.scrollContent = nil
	// 同步标记崩溃恢复后台写盘：任何编辑路径都确保 swap 会在下个周期刷新。
	if m.swap != nil {
		m.swap.MarkDirty()
	}
}

// contentHeight 返回内容区可显示的行数（不含底部边框/状态栏/命令行）。
// 底部布局（仿 vim + 上下分明的边框结构）：
//
//	[ 顶线 1 行 ]                    <- +-----+（与终端顶部边框式分隔）
//	[ 内容区 contentHeight 行 ]
//	[ 分隔线 1 行 ]                  <- +-----+（内容区与状态栏彻底分离）
//	[ 状态栏 1 行 ]                  <- 常驻
//	[ 命令行 1 行（仅命令模式）]      <- 命令/搜索模式下再占 1 行
//	[ 底线 1 行 ]                    <- +-----+
//
// 顶线/分隔线/底线 3 条边框线 + 状态栏 1 行 = 4 行常驻；命令模式再加 1 行。
// 内容区严格缩减，绝不覆盖状态栏（仿 vim）。
func (m *editorModel) contentHeight() int {
	reserved := 4
	if m.sm.Mode() == ModeCommand {
		reserved++
	}
	v := m.height - reserved
	if v < 1 {
		v = 1
	}
	return v
}

// visibleLines 兼容旧调用：返回当前内容区行数（基于命令模式动态预留）。
func visibleLines(m *editorModel) int {
	return m.contentHeight()
}
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// codeBG 是代码块使用的灰色背景 ANSI（48;5;236，柔和暗灰），比终端背景略亮但
// 不刺眼，接近 glow 的代码块背景风格。
const codeBG = "\x1b[48;5;236m"

// codeBorder 是代码块左侧的边框条：亮青蓝竖线 + 透明背景。
// 注意用 ASCII '|' 而非 '│'（U+2502 为 Unicode East Asian Ambiguous，在中文
// locale + kitty 下渲染为 2 列会撑破行宽导致整屏换行滚动）。
const codeBorder = "\x1b[38;5;67m|\x1b[0m"

// codeLidFg 是代码块上/下盖的水平边框色（比内容灰底略亮，勾勒矩形轮廓）。
const codeLidFg = "\x1b[38;5;239m"

// highlightCode 用 chroma 直接高亮代码（不走 glamour）：返回逐行、带 256/truecolor
// 前景色但**无背景**的彩色字符串。glamour 的 dark 风格代码块用"灰色前景空格铺满整行"
// 来伪装灰底，无法干净地裁成自适应宽度矩形；故代码块改由 chroma 上色 + renderCodeRect
// 自行绘制真正的 48;5;236 灰底矩形，宽度由原始代码内容决定。
func highlightCode(lang, code string) []string {
	lexer := lexers.Get(strings.TrimSpace(lang))
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("dracula")
	if style == nil {
		style = styles.Fallback
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		// 高亮失败则退回原始文本（分行、无颜色）
		return strings.Split(code, "\n")
	}
	var b strings.Builder
	f := formatters.TTY16m
	if err := f.Format(&b, style, it); err != nil {
		return strings.Split(code, "\n")
	}
	lines := strings.Split(b.String(), "\n")
	// 去掉因代码末尾换行产生的多余空项（保留代码内部应有的空行）。
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// contentWidth 计算一行去掉 ANSI 后的显示列宽（宽字符按 2 列）。
func contentWidth(s string) int {
	return fbDisplayWidth(stripANSIForWidth(s))
}

// padCodeLine 把单行代码内容补齐到 maxW 列宽，并保持灰底：
// 把内层 "\x1b[0m"/"\x1b[m" 替换为 codeBG（防止背景被重置丢失），再按其显示列宽
// 用带灰底背景的空格补到 maxW，使矩形右边缘对齐成一条竖线。
func padCodeLine(line string, maxW int) string {
	line = strings.ReplaceAll(line, "\x1b[0m", codeBG)
	line = strings.ReplaceAll(line, "\x1b[m", codeBG)
	w := contentWidth(line)
	if w < maxW {
		line += codeBG + strings.Repeat(" ", maxW-w)
	}
	return line
}

// renderCodeRect 把代码块的多行渲染结果包装成一个“自适应宽度的完整矩形”：
// 矩形宽度 = 块内最宽代码行的显示列宽（代码有多宽框就多宽，不会铺满整行），
// 上/下各有一行纯灰底“盖子”勾出矩形轮廓，左侧以青蓝竖线 │ 为边框，
// 每行内容用灰底补齐到统一宽度，形成闭合矩形。
//   - rendered：glamour 高亮后的逐行输出（用于显示内容/前景色）。
//   - widthRef：原始代码内容行（用于测量真实宽度）。glamour 的 chroma 风格会把代码行
//     背景填充到整行终端宽度，若直接用 rendered 测宽会让矩形又变回铺满整行；故宽度必须
//     以原始代码内容为准（围栏 ``` 行较短，不会成为最宽行，可一并传入）。
//   - firstPrefix 仅用于首行（编辑模式下携带行号 gutter），其余行用 lidPrefix 对齐。
//
// renderCodeRect 把代码块的多行渲染结果包装成“自适应宽度的完整矩形”。
//   - rendered：glamour/chroma 高亮后的逐行输出（用于显示内容/前景色）。
//   - widthRef：原始代码内容行（用于测量真实宽度）。
//   - firstPrefix：首行前缀（含 gutter 行号着色），其余行用 lidPrefix 对齐。
//   - availWidth：代码正文可用宽度（已不含前缀与左侧边框，单位：显示列）。
//
// 关键约束：矩形（前缀+边框+正文）必须≤终端宽，否则超宽代码行被终端自动硬折行，
// 使“逻辑视觉行数”≠“真实显示行数”，预览滚动偏移与蓝底光标块随之错位（飘逸）。
// 故矩形宽度钳制到 availWidth，超宽代码行按显示列软换行（wrapText 保留 chroma 着色）。
func renderCodeRect(rendered, widthRef []string, firstPrefix, lidPrefix string, availWidth int) []string {
	if availWidth < 4 {
		availWidth = 4
	}
	maxW := 0
	for _, rl := range widthRef {
		if w := contentWidth(rl); w > maxW {
			maxW = w
		}
	}
	if maxW < 1 {
		maxW = 1
	}
	if maxW > availWidth {
		// 钳制到可用宽度：代码块矩形不会超出终端，杜绝硬折行。
		maxW = availWidth
	}
	var out []string
	// 顶盖：gutter + 边框 + 灰底 + 宽 maxW 的纯色条 + 重置
	out = append(out, lidPrefix+codeBorder+codeBG+codeLidFg+strings.Repeat(" ", maxW)+"\x1b[0m")
	for k, rl := range rendered {
		prefix := firstPrefix
		if k > 0 {
			prefix = lidPrefix
		}
		// 超宽行按显示列软换行（wrapText 正确按显示宽处理 ANSI，保留 chroma 着色），
		// 每段都套灰底矩形，保证每行宽度≤终端宽、逻辑行数==真实显示行数。
		for si, seg := range wrapText(rl, maxW) {
			p := prefix
			if si > 0 {
				p = lidPrefix
			}
			out = append(out, p+codeBorder+codeBG+padCodeLine(seg, maxW)+"\x1b[0m")
		}
	}
	// 底盖
	out = append(out, lidPrefix+codeBorder+codeBG+codeLidFg+strings.Repeat(" ", maxW)+"\x1b[0m")
	return out
}

// stripANSIForWidth 粗略去除 ANSI 转义以估算显示列宽（用于底部栏布局对齐）。
func stripANSIForWidth(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == 0x1b && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func isNumber(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
}
