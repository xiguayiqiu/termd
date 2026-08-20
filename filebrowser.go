package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// ============================================================
// FileBrowser —— :ex 极简文件浏览器（ranger 风格列表视图）
// ============================================================
//
// 在 Command 模式下输入 :ex 打开。提供：
//   - 当前目录文件/文件夹列表
//   - 上下移动光标
//   - 回车进入目录 / 选择文件（返回该路径供主程序加载）
//   - Esc 退出浏览器
//   - d：在当前路径下创建文件夹（输入名称后回车）
//   - t：在当前路径下创建文件（输入名称后回车）
//   - r：删除光标选中的文件/文件夹（弹确认框，y 确认 / 其它取消）

// fbMode 为文件浏览器的子模式，用于区分「列表浏览 / 输入名称 / 确认删除」。
type fbMode int

const (
	fbList    fbMode = iota // 正常列表浏览
	fbInput                 // 输入名称（创建文件夹/文件）
	fbConfirm               // 确认删除弹窗
)

// fbUseIcons 控制是否渲染 Nerd Font 文件图标。
// 仅当终端使用 Nerd Font（如 0xproto Nerd Font、FiraCode Nerd Font 等）时才应
// 开启；普通终端无这些字形，开启会显示空白方块/乱码。默认关闭——用户若使用
// Nerd Font，可在 .termdrc 写 `fileicons`，或运行时 `:set fileicons` 开启；
// 关闭用 `nofileicons` / `:set nofileicons`。
var fbUseIcons = false

// Nerd Font 图标（仅在 fbUseIcons=true 时按后缀拼接在名称前）。
// 这些字形来自 Nerd Font 内嵌的 devicon / Font Awesome / Material 集合，
// 普通字体无此映射。
const (
	iconFolder = "\uf413 " //   nf-fa-folder
	iconExec   = "\uf489 " // 󰂉  nf-fa-file_code（可执行/脚本）
	iconFile   = "\uf15b " // 󰅛  nf-fa-file（通用文件兜底）
)

// fbIconByExt 按文件后缀映射 Nerd Font 图标（仅 fbUseIcons=true 时生效）。
// 覆盖常见文本/代码/图片/音视频/压缩包/配置类型；未列出的后缀回退到通用文件图标。
var fbIconByExt = map[string]string{
	// 文档 / 标记语言
	".md": "\uf48a ", ".markdown": "\uf48a ", ".txt": "\uf15c ", ".pdf": "\uf1f5 ",
	".doc": "\uf1f5 ", ".docx": "\uf1f5 ", ".rtf": "\uf1f5 ", ".epub": "\uf02d ",
	".tex": "\ue63b ", ".rst": "\uf48a ",
	// 代码 / 数据
	".go": "\ue627 ", ".py": "\ue606 ", ".rb": "\ue21e ", ".pl": "\ue769 ",
	".js": "\ue74e ", ".jsx": "\ue7ba ", ".ts": "\ue628 ", ".tsx": "\ue7ba ",
	".mjs": "\ue74e ", ".cjs": "\ue74e ", ".lua": "\ue620 ", ".php": "\ue73d ",
	".java": "\ue256 ", ".kt": "\ue634 ", ".kts": "\ue634 ", ".scala": "\ue737 ",
	".rs": "\ue7a8 ", ".c": "\ue61e ", ".h": "\uf0fd ", ".cpp": "\ue61d ",
	".cc": "\ue61d ", ".hpp": "\uf0fd ", ".cs": "\uf81a ", ".swift": "\ue755 ",
	".sh": "\uf489 ", ".bash": "\uf489 ", ".zsh": "\uf489 ", ".fish": "\uf489 ",
	".json": "\ue60b ", ".yaml": "\uf481 ", ".yml": "\uf481 ", ".toml": "\ue6b2 ",
	".xml": "\uf72d ", ".csv": "\uf1c3 ", ".sql": "\uf1c0 ", ".db": "\uf1c0 ",
	".html": "\ue736 ", ".htm": "\ue736 ", ".css": "\ue749 ", ".scss": "\ue749 ",
	".vim": "\ue62b ", ".vimrc": "\ue62b ",
	// 图片
	".png": "\uf1c5 ", ".jpg": "\uf1c5 ", ".jpeg": "\uf1c5 ", ".gif": "\uf1c5 ",
	".svg": "\uf1c5 ", ".webp": "\uf1c5 ", ".bmp": "\uf1c5 ", ".ico": "\uf1c5 ",
	".tiff": "\uf1c5 ",
	// 音视频
	".mp3": "\uf001 ", ".wav": "\uf001 ", ".flac": "\uf001 ", ".ogg": "\uf001 ",
	".mp4": "\uf03d ", ".mkv": "\uf03d ", ".mov": "\uf03d ", ".avi": "\uf03d ",
	".webm": "\uf03d ",
	// 压缩包 / 归档
	".zip": "\uf410 ", ".tar": "\uf410 ", ".gz": "\uf410 ", ".bz2": "\uf410 ",
	".xz": "\uf410 ", ".7z": "\uf410 ", ".rar": "\uf410 ",
	// 其它常见
	".git": "\ue702 ", ".lock": "\uf023 ", ".env": "\uf023 ",
}

// 文件浏览器配色（ANSI 256 调色板，所有终端均支持）：
//
//	蓝色 = 文件夹；绿色 = 可执行程序或脚本；默认色 = 普通文件。
var (
	fbDirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))  // bright blue
	fbExecStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))  // bright green
	fbFileStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")) // 浅灰
)

// 常见脚本/解释型语言扩展名：命中即按“可执行/脚本”着色（绿色）。
var fbScriptExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".ksh": true, ".fish": true,
	".py": true, ".pyw": true, ".pl": true, ".pm": true, ".rb": true, ".rake": true,
	".awk": true, ".sed": true, ".php": true, ".js": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".lua": true, ".tcl": true, ".groovy": true, ".scala": true,
	".coffee": true, ".ps1": true, ".bat": true, ".cmd": true, ".vbs": true, ".cgi": true,
	".r": true, ".jl": true,
}

// entryIcon 返回某条目的 Nerd Font 图标前缀（含尾随空格）。
// 仅在 fbUseIcons=true 时返回非空图标；否则返回空串（不渲染图标）。
// 优先级：目录→文件夹；文件→按后缀查表，未命中回退通用文件图标。
func (fb *FileBrowser) entryIcon(name string) string {
	if !fbUseIcons {
		return ""
	}
	if name == "../" || name == ".." || strings.HasSuffix(name, "/") {
		return iconFolder
	}
	if ic, ok := fbIconByExt[strings.ToLower(filepath.Ext(name))]; ok {
		return ic
	}
	return iconFile
}

// entryStyle 根据条目名称返回（着色样式, 图标前缀）。
// 规则：目录→蓝+文件夹图标；普通文件有可执行位或脚本扩展名→绿+代码图标；
// 其余普通文件→默认色+对应后缀图标。图标仅在 fbUseIcons=true 时渲染。
func (fb *FileBrowser) entryStyle(name string) (lipgloss.Style, string) {
	icon := fb.entryIcon(name)
	// 目录（含 "../" 上级目录）
	if name == "../" || name == ".." || strings.HasSuffix(name, "/") {
		return fbDirStyle, icon
	}
	// 普通文件：检查可执行位或脚本扩展名
	full := filepath.Join(fb.dir, name)
	if info, err := os.Stat(full); err == nil {
		mode := info.Mode()
		executable := mode.IsRegular() && mode.Perm()&0111 != 0
		script := fbScriptExts[strings.ToLower(filepath.Ext(name))]
		if executable || script {
			return fbExecStyle, icon
		}
	}
	return fbFileStyle, icon
}

// FileBrowser 极简文件浏览器状态。
type FileBrowser struct {
	open     bool
	dir      string
	entries  []string // 目录项（含相对路径）
	cursor   int
	selected string // 用户最终选择的文件路径；空表示取消

	// 增强功能状态
	mode          fbMode // 当前子模式（列表/输入/确认）
	inputLabel    string // 输入提示："dirname:" / "textname:"
	inputBuf      string // 当前输入的文件/文件夹名称
	createKind    string // "dir" 创建文件夹 / "file" 创建文件
	pendingDelete string // 待删除项的相对名（如 "foo/" 或 "bar.md"）
	pendingRename string // 待重命名项的相对名（如 "foo/" 或 "bar.md"）

	// 两栏布局：右侧文本预览
	previewLines  []string // 当前光标选中文本文件的内容（按行）
	previewScroll int      // 右侧预览区垂直滚动偏移
	focusRight    bool     // 焦点是否在右栏预览区（false=左栏列表）

	// 左栏列表滚动偏移：持久化在状态里，使光标移动时列表“按行平滑滚动”
	// （光标越出可视区才卷动一行），而非每步重新居中到半屏中央——后者会让
	// 选择文件时列表剧烈跳变（看起来“跳得很远”）。
	listTop int

	// 输入子模式（创建/重命名）的可移动光标位置（以 rune 计，0=行首）
	inputPos int
}

// NewFileBrowser 构造文件浏览器，定位到 dir。
func NewFileBrowser(dir string) *FileBrowser {
	fb := &FileBrowser{dir: dir, cursor: 0, mode: fbList}
	fb.reload()
	return fb
}

// Open 打开浏览器。
func (fb *FileBrowser) Open() { fb.open = true; fb.reload() }

// Close 关闭浏览器并清空选择。
func (fb *FileBrowser) Close() {
	fb.open = false
	fb.selected = ""
	fb.mode = fbList
	fb.inputBuf = ""
	fb.pendingDelete = ""
	fb.pendingRename = ""
	fb.inputPos = 0
}

// reload 重新读取目录内容，目录在前、文件在后，按名称排序。
func (fb *FileBrowser) reload() {
	entries, err := os.ReadDir(fb.dir)
	if err != nil {
		fb.entries = []string{".. (读取失败)"}
		return
	}
	dirs := []string{}
	files := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // 隐藏文件不显示，保持简洁
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name()+"/")
		} else {
			files = append(files, e.Name())
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	fb.entries = append([]string{"../"}, append(dirs, files...)...)
	if fb.cursor >= len(fb.entries) {
		fb.cursor = len(fb.entries) - 1
	}
	if fb.cursor < 0 {
		fb.cursor = 0
	}
	fb.listTop = 0 // 目录内容变更后列表回到顶部（与 cursor=0 同步）
	fb.refreshPreview()
}

// Move 上下移动光标（d: +1 / -1）。仅在列表模式有效。
func (fb *FileBrowser) Move(d int) {
	fb.cursor += d
	if fb.cursor < 0 {
		fb.cursor = 0
	}
	if fb.cursor >= len(fb.entries) {
		fb.cursor = len(fb.entries) - 1
	}
	fb.refreshPreview()
}

// Confirm 在当前光标项上回车：若为目录则进入，若为文件则选定并返回路径。
// 返回选定路径；若进入目录则路径为空（仅改变状态）。
func (fb *FileBrowser) Confirm() string {
	if fb.cursor < 0 || fb.cursor >= len(fb.entries) {
		return ""
	}
	name := fb.entries[fb.cursor]
	switch {
	case name == "../" || name == "..":
		parent := filepath.Dir(fb.dir)
		if parent != fb.dir {
			fb.dir = parent
			fb.cursor = 0
			fb.reload()
		}
		return ""
	case strings.HasSuffix(name, "/"):
		fb.dir = filepath.Join(fb.dir, strings.TrimSuffix(name, "/"))
		fb.cursor = 0
		fb.reload()
		return ""
	default:
		fb.selected = filepath.Join(fb.dir, name)
		return fb.selected
	}
}

// ---- 两栏布局：右侧文本预览 ----

// isTextFile 依据扩展名粗判是否为可预览的文本文件（目录/二进制返回 false）。
func (fb *FileBrowser) isTextFile(name string) bool {
	if name == "../" || name == ".." || strings.HasSuffix(name, "/") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico", ".tiff",
		".mp3", ".wav", ".flac", ".ogg", ".mp4", ".mkv", ".mov", ".avi", ".webm",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".pdf", ".ttf", ".otf", ".woff", ".woff2", ".eot":
		return false
	}
	return true
}

// refreshPreview 读取光标选中项的内容以填充右栏预览：
// 文本文件→按行填充内容；目录/二进制→属性面板（绝不以二进制内容当文本渲染，避免乱码）。
// 切换文件时滚动归零。
func (fb *FileBrowser) refreshPreview() {
	fb.previewScroll = 0
	if fb.cursor < 0 || fb.cursor >= len(fb.entries) {
		fb.previewLines = []string{T("（无可预览项）")}
		return
	}
	name := fb.entries[fb.cursor]
	if name == "../" || name == ".." {
		fb.previewLines = []string{T("（上级目录）")}
		return
	}
	if strings.HasSuffix(name, "/") {
		fb.previewLines = []string{T("（目录，无法预览）")}
		return
	}
	// 先用扩展名粗筛，再实际嗅探内容，双重判断是否为二进制。
	// 仅扩展名不可靠（如 termd 可执行文件、无扩展名脚本等会被误判为文本）。
	if !fb.isTextFile(name) {
		fb.previewLines = fb.binaryPreviewLines(name)
		return
	}
	path := filepath.Join(fb.dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		fb.previewLines = []string{T("读取失败: ") + err.Error()}
		return
	}
	// 大小限制：超过 256KB 只预览前 256KB，避免一次性读入超大文件卡顿
	const maxBytes = 256 * 1024
	if len(data) > maxBytes {
		data = data[:maxBytes]
	}
	// 内容嗅探：含 NUL 字节或不可打印字符占比过高 → 视为二进制，避免乱码。
	if looksBinary(data) {
		fb.previewLines = fb.binaryPreviewLines(name)
		return
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	fb.previewLines = lines
}

// looksBinary 嗅探字节内容是否像二进制：命中 NUL 字节、非法 UTF-8，或不可打印
// 控制字符占比超阈值，即判为二进制，避免把二进制读成字符串渲染出乱码。
func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true // 含 NUL 字节：几乎肯定是二进制
	}
	// 非法 UTF-8（如含 0xff、孤立续字节等）几乎肯定是二进制。
	// 文本文件（含中文/emoji）均为合法 UTF-8，不会被误判。
	if !utf8.Valid(data) {
		return true
	}
	const sample = 8000 // 只检查前若干字节，足够判断
	n := len(data)
	if n > sample {
		n = sample
	}
	nonPrint := 0
	total := 0
	for i := 0; i < n; i++ {
		b := data[i]
		// 跳过常见可打印/空白字符，统计真正不可打印的控制字符（\t \n \r 之外）
		switch b {
		case '\t', '\n', '\r', '\v', '\f':
			continue
		}
		total++
		if b < 0x20 || b == 0x7f {
			nonPrint++
		}
	}
	if total == 0 {
		return false
	}
	return float64(nonPrint)/float64(total) > 0.05 // 不可打印字符 > 5% 视为二进制
}

// binaryPreviewLines 为非文本（二进制）文件生成属性面板，取代原先的单一提示。
// 展示：名称、类型/扩展名、字节大小（带 KB/MB 单位）、权限位、最后修改时间。
func (fb *FileBrowser) binaryPreviewLines(name string) []string {
	path := filepath.Join(fb.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return []string{T("读取失败: ") + err.Error()}
	}
	ext := strings.ToLower(filepath.Ext(name))
	// 依据扩展名给出可读的类型描述（中英文各一套，走 T 翻译）
	cat := ""
	switch {
	case strings.HasPrefix(ext, ".png"), strings.HasPrefix(ext, ".jpg"),
		strings.HasPrefix(ext, ".jpeg"), strings.HasPrefix(ext, ".gif"),
		strings.HasPrefix(ext, ".svg"), strings.HasPrefix(ext, ".webp"),
		strings.HasPrefix(ext, ".bmp"), strings.HasPrefix(ext, ".ico"),
		strings.HasPrefix(ext, ".tiff"):
		cat = T("图片")
	case strings.HasPrefix(ext, ".mp3"), strings.HasPrefix(ext, ".wav"),
		strings.HasPrefix(ext, ".flac"), strings.HasPrefix(ext, ".ogg"):
		cat = T("音频")
	case strings.HasPrefix(ext, ".mp4"), strings.HasPrefix(ext, ".mkv"),
		strings.HasPrefix(ext, ".mov"), strings.HasPrefix(ext, ".avi"),
		strings.HasPrefix(ext, ".webm"):
		cat = T("视频")
	case strings.HasPrefix(ext, ".zip"), strings.HasPrefix(ext, ".tar"),
		strings.HasPrefix(ext, ".gz"), strings.HasPrefix(ext, ".bz2"),
		strings.HasPrefix(ext, ".xz"), strings.HasPrefix(ext, ".7z"),
		strings.HasPrefix(ext, ".rar"):
		cat = T("压缩包")
	case strings.HasPrefix(ext, ".pdf"):
		cat = T("PDF 文档")
	case strings.HasPrefix(ext, ".ttf"), strings.HasPrefix(ext, ".otf"),
		strings.HasPrefix(ext, ".woff"), strings.HasPrefix(ext, ".woff2"),
		strings.HasPrefix(ext, ".eot"):
		cat = T("字体")
	default:
		cat = T("二进制文件")
	}
	// 可读大小
	size := info.Size()
	var sizeStr string
	switch {
	case size < 1024:
		sizeStr = fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
	case size < 1024*1024*1024:
		sizeStr = fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	default:
		sizeStr = fmt.Sprintf("%.1f GB", float64(size)/(1024*1024*1024))
	}
	mod := info.ModTime().Format("2006-01-02 15:04:05")

	var lines []string
	lines = append(lines, T("二进制文件")+": "+name)
	lines = append(lines, "")
	lines = append(lines, T("类型")+":  "+cat+"  ("+ext+")")
	lines = append(lines, T("大小")+":  "+sizeStr+"  ("+fmt.Sprintf("%d", size)+" B)")
	lines = append(lines, T("权限")+":  "+info.Mode().String())
	lines = append(lines, T("修改时间")+": "+mod)
	return lines
}

// ScrollPreview 滚动右栏预览（d>0 下滚，d<0 上滚）；钳制在有效范围内。
func (fb *FileBrowser) ScrollPreview(d int) {
	max := len(fb.previewLines) - 1
	if max < 0 {
		max = 0
	}
	fb.previewScroll += d
	if fb.previewScroll < 0 {
		fb.previewScroll = 0
	}
	if fb.previewScroll > max {
		fb.previewScroll = max
	}
}

// ToggleFocus 在左栏列表 / 右栏预览之间切换输入焦点（Tab 键）。
func (fb *FileBrowser) ToggleFocus() {
	fb.focusRight = !fb.focusRight
}

// ---- 增强功能：创建文件夹 / 创建文件 / 删除 ----

// StartCreate 进入「输入名称」子模式，准备创建文件夹(kind="dir")或文件(kind="file")。
func (fb *FileBrowser) StartCreate(kind string) {
	fb.mode = fbInput
	fb.createKind = kind
	fb.inputBuf = ""
	fb.inputPos = 0
	if kind == "dir" {
		fb.inputLabel = T("dirname: ")
	} else {
		fb.inputLabel = T("filename: ")
	}
}

// StartRename 进入「输入名称」子模式，预填光标选中项的当前名称，准备重命名。
// 选中 "../" 上级目录时拒绝并返回提示（不进入输入）。
func (fb *FileBrowser) StartRename() string {
	if fb.cursor < 0 || fb.cursor >= len(fb.entries) {
		return ""
	}
	name := fb.entries[fb.cursor]
	if name == "../" || name == ".." {
		return T("不能重命名上级目录")
	}
	fb.pendingRename = name
	fb.mode = fbInput
	fb.inputBuf = strings.TrimSuffix(name, "/") // 预填原名（去尾部斜杠），便于直接修改
	fb.inputPos = len([]rune(fb.inputBuf))      // 光标置于末尾，可向左移动修改
	fb.inputLabel = T("重命名: ")
	return ""
}

// AppendInput 在光标位置插入字符（按 rune 处理，兼容中文/emoji），随后光标右移。
func (fb *FileBrowser) AppendInput(runes []rune) {
	r := []rune(fb.inputBuf)
	if fb.inputPos > len(r) {
		fb.inputPos = len(r)
	}
	head := r[:fb.inputPos]
	tail := r[fb.inputPos:]
	merged := append(head, runes...)
	merged = append(merged, tail...)
	fb.inputBuf = string(merged)
	fb.inputPos += len(runes)
}

// BackspaceInput 删除光标前的一个字符（按 rune），光标左移；行首不动作。
func (fb *FileBrowser) BackspaceInput() {
	r := []rune(fb.inputBuf)
	if fb.inputPos <= 0 || len(r) == 0 {
		return
	}
	head := r[:fb.inputPos-1]
	tail := r[fb.inputPos:]
	fb.inputBuf = string(append(head, tail...))
	fb.inputPos--
}

// DeleteForward 删除光标后的一个字符（按 rune），光标不动；行尾不动作。
func (fb *FileBrowser) DeleteForward() {
	r := []rune(fb.inputBuf)
	if fb.inputPos >= len(r) || len(r) == 0 {
		return
	}
	head := r[:fb.inputPos]
	tail := r[fb.inputPos+1:]
	fb.inputBuf = string(append(head, tail...))
}

// MoveInput 移动输入光标（d>0 右移 / d<0 左移），钳制在 [0, len(runes)] 范围内。
func (fb *FileBrowser) MoveInput(d int) {
	r := []rune(fb.inputBuf)
	fb.inputPos += d
	if fb.inputPos < 0 {
		fb.inputPos = 0
	}
	if fb.inputPos > len(r) {
		fb.inputPos = len(r)
	}
}

// InputJump 跳到输入行首(d<0)或行尾(d>0)。
func (fb *FileBrowser) InputJump(end bool) {
	r := []rune(fb.inputBuf)
	if end {
		fb.inputPos = len(r)
	} else {
		fb.inputPos = 0
	}
}

// SubmitInput 提交「输入名称」：根据上下文执行重命名或创建文件夹/文件，返回状态消息。
// 校验：名称非空、不含路径分隔符、不以点开头（避免越界/隐藏项）。
func (fb *FileBrowser) SubmitInput() string {
	name := strings.TrimSpace(fb.inputBuf)
	fb.mode = fbList
	fb.inputBuf = ""
	if fb.pendingRename != "" {
		return fb.submitRename(name)
	}
	if name == "" || name == ".." || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return T("名称无效（不能为空/含 / /以 . 开头）")
	}
	target := filepath.Join(fb.dir, name)
	// 已存在则拒绝，避免误覆盖
	if _, err := os.Stat(target); err == nil {
		return T("已存在，未创建: ") + name
	}
	var err error
	if fb.createKind == "dir" {
		err = os.Mkdir(target, 0o755)
	} else {
		var f *os.File
		f, err = os.Create(target)
		if f != nil {
			f.Close()
		}
	}
	if err != nil {
		return T("创建失败: ") + err.Error()
	}
	fb.cursor = 0
	fb.reload()
	if fb.createKind == "dir" {
		return T("已创建文件夹: ") + name
	}
	return T("已创建文件: ") + name
}

// submitRename 执行重命名：把 pendingRename 重命名为 name，保持目录/文件类型不变。
func (fb *FileBrowser) submitRename(name string) string {
	old := fb.pendingRename
	fb.pendingRename = ""
	if name == "" || name == ".." || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return T("名称无效（不能为空/含 / /以 . 开头）")
	}
	oldBase := strings.TrimSuffix(old, "/")
	oldTarget := filepath.Join(fb.dir, oldBase)
	newTarget := filepath.Join(fb.dir, name)
	// 目标已存在（且非自身）则拒绝，避免误覆盖
	if info, err := os.Stat(newTarget); err == nil && info.Name() != oldBase {
		return T("已存在，未重命名: ") + name
	}
	if err := os.Rename(oldTarget, newTarget); err != nil {
		return T("重命名失败: ") + err.Error()
	}
	fb.cursor = 0
	fb.reload()
	return T("已重命名: ") + oldBase + " → " + name
}

// StartDelete 进入「确认删除」子模式，记录光标选中的待删除项。
// 选中 "../" 上级目录时拒绝并返回提示（不进入确认）。
func (fb *FileBrowser) StartDelete() string {
	if fb.cursor < 0 || fb.cursor >= len(fb.entries) {
		return ""
	}
	name := fb.entries[fb.cursor]
	if name == "../" || name == ".." {
		return T("不能删除上级目录")
	}
	fb.pendingDelete = name
	fb.mode = fbConfirm
	return ""
}

// ConfirmDelete 处理「确认删除」结果：yes=true 执行删除，否则取消。
func (fb *FileBrowser) ConfirmDelete(yes bool) string {
	pending := fb.pendingDelete
	fb.mode = fbList
	fb.pendingDelete = ""
	fb.inputBuf = ""
	if !yes {
		return T("已取消删除")
	}
	if pending == "" {
		return ""
	}
	target := filepath.Join(fb.dir, strings.TrimSuffix(pending, "/"))
	if err := os.RemoveAll(target); err != nil {
		return T("删除失败: ") + err.Error()
	}
	fb.cursor = 0
	fb.reload()
	return T("已删除: ") + pending
}

// Cancel 取消当前子模式（输入/确认），回到列表浏览。
func (fb *FileBrowser) Cancel() {
	fb.mode = fbList
	fb.inputBuf = ""
	fb.pendingDelete = ""
	fb.pendingRename = ""
	fb.inputPos = 0
}

// Render 渲染文件浏览器视图字符串（两栏：左=文件列表，右=文本预览）。
// 参数 height 为窗口【总】高度（由调用方传入 m.height）。Render 内部自行预留
// 底部状态栏 1 行（availH = height-1），标题占 1 行，余下 rows 行给两栏内容。
// 这样 body 严格产出 availH 行，与 fixBottom 的状态栏（1 行）拼成完整窗口高度，
// 且完全不依赖 contentHeight()（避免受 Preview/Command 模式的高度计算干扰）。
func (fb *FileBrowser) Render(width, height int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	// 强隔离：自行从总高度推导可用高度，不调用任何外部高度函数
	availH := height - 1 // 预留底部状态栏 1 行
	if availH < 2 {
		availH = 2
	}

	// 两栏宽度：左栏取 40%（至少 20 列），右栏取剩余；中间留 1 列间隔
	leftW := width * 2 / 5
	if leftW < 20 {
		leftW = 20
	}
	if leftW > width-25 {
		leftW = width - 25
	}
	if leftW < 10 {
		leftW = 10
	}
	rightW := width - leftW - 1
	if rightW < 10 {
		rightW = 10
	}

	// 标题栏（占 1 行）：左栏标题 + 右栏标题
	titleLeft := T("文件 (j/k 移动, Enter 打开, Tab 切换) ")
	titleRight := T("预览 (Tab 切换焦点) ")
	titleLeft = lipgloss.NewStyle().
		Background(lipgloss.Color("63")).Foreground(lipgloss.Color("255")).
		Width(leftW).Render(fbFit(titleLeft, leftW))
	titleRight = lipgloss.NewStyle().
		Background(lipgloss.Color("63")).Foreground(lipgloss.Color("255")).
		Width(rightW).Render(fbFit(titleRight, rightW))
	title := lipgloss.JoinHorizontal(lipgloss.Top, titleLeft, " ", titleRight)

	// 两栏内容可用行数 = 标题栏之外、状态栏之上
	rows := availH - 1
	if rows < 1 {
		rows = 1
	}

	// 左栏：文件列表（按光标滚动，仅可见行）
	leftLines := fb.renderListPane(leftW, rows)
	// 右栏：文本预览（按 previewScroll 滚动，仅可见行）
	rightLines := fb.renderPreviewPane(rightW, rows)

	// 两栏都裁剪到可见高度，不足补空；不再用全部条目数，避免长目录把两栏拉远
	if rows < 1 {
		rows = 1
	}
	for len(leftLines) < rows {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < rows {
		rightLines = append(rightLines, "")
	}
	// 两栏对齐要点：lipgloss.JoinHorizontal 按每个 block 的「实际最大行宽」拼接各行。
	// 若行宽随文件名长短变化（如仅用 MaxWidth 只截断不补宽），右栏 x 起始列会逐行漂移，
	// 短文件名行右栏预览紧贴其后、长文件名行右栏靠后，两栏内容看起来「混合」。
	// 不能直接改用 Width() 固定列宽：Width 内部会先 cellbuf.Wrap 软换行，
	// 一旦 fbFit 宽度统计与实际终端显示不一致（宽字符/图标），超宽行被撑成 2+ 行 → 列表稀疏。
	// 因此双保险：先用 MaxWidth 硬截断（ANSI-aware，超宽只截断不拆行），
	// 再按显示列宽手动补空格到 leftW/rightW，使每行宽度固定 → 两栏严格对齐。
	mid := strings.Builder{}
	for r := 0; r < rows; r++ {
		left := lipgloss.NewStyle().MaxWidth(leftW).Render(leftLines[r])
		left += strings.Repeat(" ", max(0, leftW-lipgloss.Width(left)))
		right := lipgloss.NewStyle().MaxWidth(rightW).Render(rightLines[r])
		right += strings.Repeat(" ", max(0, rightW-lipgloss.Width(right)))
		mid.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
		if r < rows-1 {
			mid.WriteString("\n")
		}
	}

	// 主体仅含「标题栏 + 两栏内容」，严格占满 contentHeight 行（标题 1 行 + 内容 rows 行）。
	// 位置/输入提示等由调用方（model.View）写入底部状态栏，避免与 fixBottom 的状态栏叠加导致行数错乱。
	return title + "\n" + mid.String()
}

// fbDisplayWidth 返回字符串的【显示列宽】：(宽字符)东亚 CJK / 全角标点 / emoji 等
// 占 2 列，其余（含半角、西文、窄 emoji）占 1 列。不能用 len([]rune) 近似，否则中文
// 等宽字符会被少算，导致 fbFit 截断后实际显示宽度超出列宽，被 lipgloss 软换行撑高整行。
func fbDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x1100 &&
			(r <= 0x115f || r == 0x2329 || r == 0x232a ||
				(r >= 0x2e80 && r <= 0x303e) ||
				(r >= 0x3041 && r <= 0x33ff) ||
				(r >= 0x3400 && r <= 0x4dbf) ||
				(r >= 0x4e00 && r <= 0x9fff) ||
				(r >= 0xa000 && r <= 0xa4cf) ||
				(r >= 0xac00 && r <= 0xd7a3) ||
				(r >= 0xf900 && r <= 0xfaff) ||
				(r >= 0xfe10 && r <= 0xfe19) ||
				(r >= 0xfe30 && r <= 0xfe6f) ||
				(r >= 0xff00 && r <= 0xff60) ||
				(r >= 0xffe0 && r <= 0xffe6) ||
				(r >= 0x1f300 && r <= 0x1faff) ||
				(r >= 0x20000 && r <= 0x3fffd)) {
			w += 2
		} else {
			w += 1
		}
	}
	return w
}

// fbFit 裁剪/截断字符串到 w 【显示列宽】。超出则按列宽截断并加 "…"，保证渲染后
// 不会因中文/宽字符导致实际宽度超过列宽而被 lipgloss 软换行（否则整行被撑高，
// 左右栏行号对齐错位，左栏文件列表看起来“隔得很远”）。
func fbFit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if fbDisplayWidth(s) <= w {
		return s
	}
	// 逐 rune 累加显示宽度，截断到 w-1 列，末尾补 "…"（占 1 列）
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := 1
		if r >= 0x1100 { // 复用宽字符判定（粗略：>=0x1100 区间多为宽字符）
			if r <= 0x115f || r == 0x2329 || r == 0x232a ||
				(r >= 0x2e80 && r <= 0x303e) ||
				(r >= 0x3041 && r <= 0x33ff) ||
				(r >= 0x3400 && r <= 0x4dbf) ||
				(r >= 0x4e00 && r <= 0x9fff) ||
				(r >= 0xa000 && r <= 0xa4cf) ||
				(r >= 0xac00 && r <= 0xd7a3) ||
				(r >= 0xf900 && r <= 0xfaff) ||
				(r >= 0xfe10 && r <= 0xfe19) ||
				(r >= 0xfe30 && r <= 0xfe6f) ||
				(r >= 0xff00 && r <= 0xff60) ||
				(r >= 0xffe0 && r <= 0xffe6) ||
				(r >= 0x1f300 && r <= 0x1faff) ||
				(r >= 0x20000 && r <= 0x3fffd) {
				rw = 2
			}
		}
		if cur+rw > w-1 {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String() + "…"
}

// renderListPane 渲染左栏文件列表（多行）。列表根据光标位置滚动，只输出
// 可见的 h 行，使光标始终在可视区内，避免长目录把两栏「拉得很远」。
// 始终返回恰好 h 行（条目不足时以空行补齐），与右栏行数保持一致，避免两栏错位。
func (fb *FileBrowser) renderListPane(w, h int) []string {
	if h < 1 {
		h = 1
	}
	// 稳定滚动：基于持久化的 listTop，仅当光标越出可视区时才卷动一行，
	// 避免“光标每次移动都重新居中到半屏中央”导致的列表剧烈跳变。
	top := fb.listTop
	if top < 0 {
		top = 0
	}
	maxTop := len(fb.entries) - h
	if maxTop < 0 {
		maxTop = 0
	}
	if top > maxTop {
		top = maxTop
	}
	// 光标滚到可视区上方：top 跟随到光标行（列表向上卷一行）
	if fb.cursor < top {
		top = fb.cursor
	}
	// 光标滚到可视区下方：top 跟随到“光标行 - h + 1”（列表向下卷一行）
	if fb.cursor >= top+h {
		top = fb.cursor - h + 1
	}
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}
	fb.listTop = top // 回写，供下次平滑滚动
	lines := []string{}
	for i := top; i < top+h && i < len(fb.entries); i++ {
		e := fb.entries[i]
		prefix := "  "
		if i == fb.cursor {
			prefix = "> "
		}
		style, icon := fb.entryStyle(e)
		cursor := i == fb.cursor && !fb.focusRight
		label := fbFit(icon+e, w-len([]rune(prefix)))
		var rendered string
		if cursor {
			rendered = lipgloss.NewStyle().Reverse(true).Render(label)
		} else {
			rendered = style.Render(label)
		}
		lines = append(lines, prefix+rendered)
	}
	// 不足 h 行时以空行补齐，确保左右栏行数对齐、列表项连续显示（不稀疏错位）。
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// renderPreviewPane 渲染右栏文本预览（多行），按 previewScroll 滚动。
func (fb *FileBrowser) renderPreviewPane(w, h int) []string {
	out := []string{}
	if h < 1 {
		h = 1
	}
	start := fb.previewScroll
	if start > len(fb.previewLines) {
		start = len(fb.previewLines)
	}
	for idx := start; idx < len(fb.previewLines) && len(out) < h; idx++ {
		line := strings.ReplaceAll(fb.previewLines[idx], "\r", "")
		// 超宽截断（按显示列宽），避免右栏溢出被软换行撑高整行
		line = fbFit(line, w)
		// 焦点在右栏时，光标行（当前预览首行）反显突出
		if fb.focusRight && idx == start {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		out = append(out, line)
	}
	return out
}
