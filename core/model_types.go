package core

import (
	"github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"termd"
)

// TabSize 是编辑模式下 Tab 键插入的缩进宽度（空格数），与渲染嵌套层级（每 2 空格一级）保持一致。
const TabSize = 2

// EditorModel 是 bubbletea 的核心 Model，整合 termd.Buffer / termd.Renderer / termd.StateMachine。
type EditorModel struct {
	Buf    *termd.Buffer
	Rend   *termd.Renderer
	sm     *termd.StateMachine
	fb     *termd.FileBrowser
	width  int
	height int
	// 编辑态光标（termd.Buffer 行列，基于字节偏移换算为 rune 列）
	cursorRow int
	cursorCol int
	// 虚拟列：垂直移动（j/k）时记住的"目标列"，使得短行换行后列位置稳定（仿 vim curswant）
	cursWant int
	// 计数前缀：编辑模式下输入数字键累积的 count（如 "3" 在 "3j" 中）
	pendingCount int
	// 待组合的 'g' 前缀（用于 gj/gk 视觉行移动）
	pendingG bool
	// 滚动偏移（Edit 模式 viewport 顶部的 termd.Buffer 行，仿 vim topline）
	scroll int
	// 预览模式下“当前高亮行”（termd.Buffer 行）。与 viewport 解耦：用户可在预览中任意
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
	// termd.Fcitx5IMEMsg 转发）。用于规避输入法激活期间的快捷键冲突。
	imeActive bool
	// swap：崩溃恢复用的 termd.SwapManager（swap.go）。管理本文件 .swp 的后台写盘。
	// 为 nil 表示未启用（未命名缓冲区或无文件路径）。
	Swap *termd.SwapManager
	// sendMsg 向 bubbletea 主循环注入消息（后台图片加载完成时触发重绘）。
	// 由 main.go 在创建 Program 后注入 *tea.Program.Send。
	SendMsg func(tea.Msg)
}

// NewModel 构造编辑器模型。
func NewModel(path string) (*EditorModel, error) {
	buf := termd.NewBuffer()
	if err := buf.LoadFile(path); err != nil {
		return nil, err
	}
	rend := termd.NewRenderer()
	rend.SetProfile(termenv.ColorProfile()) // 实际能力在 main 中按终端设置
	return &EditorModel{
		Buf:          buf,
		Rend:         rend,
		sm:           termd.NewStateMachine(),
		fb:           nil,
		status:       "就绪",
		previewDirty: true,
		blinkMode:    true,             // 光标闪烁默认开启
		smoothScroll: true,             // vim 式行滚动默认开启
		SendMsg:      func(tea.Msg) {}, // 占位；main.go 会注入真实 *Program.Send
	}, nil
}

// Init 实现 bubbletea.Model（无异步启动任务）。
func (m *EditorModel) Init() tea.Cmd { return tea.ShowCursor }

// runeColToByte 将 rune 列转换为字节列，确保光标落在正确字节起点。
func runeColToByte(line []byte, col int) int {
	bc := 0
	rc := 0
	for bc < len(line) && rc < col {
		_, size := termd.DecodeRune(line[bc:])
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
		_, size := termd.DecodeRune(line[i:])
		i += size
		rc++
	}
	return rc
}

// SwapTickMsg 是 termd.SwapManager 后台线程投递给 UI 线程的“需要采集快照”的信号。
// 由 model.Update 处理：在编辑线程（UI）内安全采集快照并交给后台写盘，
// 从而保证采集快照与 termd.Buffer 编辑发生在同一 goroutine，无数据竞争。
type SwapTickMsg struct{}
