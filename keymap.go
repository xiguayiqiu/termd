package main

import (
	"slices"
	"strings"
)

// ============================================================
// keymap —— 键盘操作集中注册管理
// ============================================================
//
// 本文件是 termd 所有键盘操作的“单一事实来源”（single source of truth）。
// 每个键位都注册为一个 Binding，包含：适用模式、按键字符串、动作名、
// 人类可读描述、所属分组（用于帮助视图分组展示）。
//
// 设计要点：
//   - Keys 中的字符串与 bubbletea 的 msg.String() 返回值保持一致，
//     便于将来从“分散的 switch-case”平滑迁移到“查表分发”。
//   - 凡在 handler 中需要匹配按键字符串，都应引用本文件导出的 Key* 常量，
//     避免散落在各处的魔法字符串。
//   - AllBindings() 返回完整注册表；GroupedBindings() 按分组聚合；
//     RenderKeyMapHelp() 生成帮助视图文本。

// EditorMode 标记键位适用的编辑器模式（与 StateMachine 的模式对应）。
type EditorMode string

const (
	ModeNamePreview     EditorMode = "PREVIEW"     // 预览/浏览态
	ModeNameEdit        EditorMode = "EDIT"        // 编辑态（插入+普通）
	ModeNameCommand     EditorMode = "COMMAND"     // 命令模式（底部输入）
	ModeNameFileBrowser EditorMode = "FILEBROWSER" // :ex 文件浏览器
)

// Binding 描述一条键位注册项。
type Binding struct {
	Modes       []EditorMode // 适用模式（空表示全局/未限定）
	Keys        []string     // 匹配的 msg.String() 按键串（任一命中即可）
	Action      string       // 动作标识（语义名，便于未来查表分发）
	Description string       // 人类可读描述（用于帮助视图）
	Group       string       // 分组（导航/编辑/命令/文件浏览器…）
}

// ---- 按键字符串常量（与 bubbletea msg.String() 返回值一致，供 handler 引用）----
const (
	KeyEsc       = "esc"
	KeyEnter     = "enter"
	KeySpace     = "space"
	KeyTab       = "tab"
	KeyShiftTab  = "shift+tab"
	KeyBackspace = "backspace"
	KeyDelete    = "delete"
	KeyUp        = "up"
	KeyDown      = "down"
	KeyLeft      = "left"
	KeyRight     = "right"
	KeyHome      = "home"
	KeyEnd       = "end"
	KeyPgUp      = "pgup"
	KeyPgDown    = "pgdown"
	KeyCtrlC     = "ctrl+c"
	KeyCtrlD     = "ctrl+d"
	KeyCtrlU     = "ctrl+u"
	KeyCtrlF     = "ctrl+f"
	KeyCtrlB     = "ctrl+b"
	KeyCtrlT     = "ctrl+t"
	KeyColon     = ":"
	KeySlash     = "/"
)

// DefaultKeyMap 是完整的键位注册表（单一事实来源）。
// 顺序即展示顺序；同分组内聚合后展示。
var DefaultKeyMap = []Binding{
	// ---------------- Preview 模式 ----------------
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{"i", "e", "a"}, Action: "preview.enterEdit",
		Description: "进入编辑模式（i/e 行首插入，a 行尾插入）", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{"o", "O"}, Action: "preview.openBelow",
		Description: "在光标行下方/上方新建空行并进入插入模式", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyColon}, Action: "preview.command",
		Description: "进入命令模式（底部输入 : 命令）", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeySlash}, Action: "preview.search",
		Description: "进入搜索模式", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyUp, "k"}, Action: "preview.up",
		Description: "上移高亮行", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyDown, "j"}, Action: "preview.down",
		Description: "下移高亮行", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyLeft, "h"}, Action: "preview.left",
		Description: "左移列光标", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyRight, "l"}, Action: "preview.right",
		Description: "右移列光标", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyEnter}, Action: "preview.openLink",
		Description: "回车：光标停在超链接上则跳转（URL / 本地 md 文件），否则下移", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyPgUp}, Action: "preview.pageUp",
		Description: "向上翻页", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyPgDown}, Action: "preview.pageDown",
		Description: "向下翻页", Group: "预览模式"},
	{Modes: []EditorMode{ModeNamePreview}, Keys: []string{KeyCtrlT}, Action: "preview.toggleOutline",
		Description: "开关大纲目录（标题跳转）", Group: "预览模式"},

	// ---------------- Edit / 插入态 ----------------
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyEsc}, Action: "insert.toNormal",
		Description: "退回普通导航态（INSERT→NORMAL）", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"<rune>"}, Action: "insert.rune",
		Description: "输入字符直接写入缓冲区", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeySpace}, Action: "insert.space",
		Description: "插入空格", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyTab}, Action: "insert.indent",
		Description: "缩进（插入 TabSize 个空格）", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyShiftTab}, Action: "insert.outdent",
		Description: "反缩进（向左删除最多 TabSize 个空格）", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyEnter}, Action: "insert.newline",
		Description: "换行（在下方新建一行）", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyBackspace, KeyDelete}, Action: "insert.backspace",
		Description: "删除（行首退格合并上一行）", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyLeft}, Action: "insert.left",
		Description: "光标左移", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyRight}, Action: "insert.right",
		Description: "光标右移", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyUp}, Action: "insert.up",
		Description: "光标上移", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyDown}, Action: "insert.down",
		Description: "光标下移", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyHome}, Action: "insert.home",
		Description: "跳到行首", Group: "插入态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyEnd}, Action: "insert.end",
		Description: "跳到行尾", Group: "插入态"},

	// ---------------- Edit / 普通导航态 ----------------
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"0"}, Action: "normal.lineStart",
		Description: "跳到行首（0，不计入数前缀）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyEsc}, Action: "normal.toPreview",
		Description: "返回预览模式", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"i"}, Action: "normal.insert",
		Description: "在光标处插入", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"a"}, Action: "normal.append",
		Description: "在光标后插入", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"I"}, Action: "normal.insertBOL",
		Description: "在行首非空白后插入", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"A"}, Action: "normal.appendEOL",
		Description: "在行尾插入", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"o"}, Action: "normal.openBelow",
		Description: "在下方新建一行并插入", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"O"}, Action: "normal.openAbove",
		Description: "在上方新建一行并插入", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"x", KeyDelete, "dl"}, Action: "normal.deleteFwd",
		Description: "删除光标后字符（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"X", "dh"}, Action: "normal.deleteBack",
		Description: "删除光标前字符（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"dw"}, Action: "normal.deleteWord",
		Description: "删除一个词（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"dd"}, Action: "normal.deleteLine",
		Description: "删除整行（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"u"}, Action: "normal.undo",
		Description: "撤销", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"h", KeyLeft}, Action: "normal.left",
		Description: "光标左移（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"l", KeyRight, KeySpace}, Action: "normal.right",
		Description: "光标右移（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"k", KeyUp}, Action: "normal.up",
		Description: "光标上移（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"j", KeyDown}, Action: "normal.down",
		Description: "光标下移（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyEnter}, Action: "normal.openLink",
		Description: "回车：光标停在超链接上则跳转（URL / 本地 md 文件），否则下移", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"w", "W"}, Action: "normal.wordFwd",
		Description: "向后移动一个词（W 忽略标点）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"b", "B"}, Action: "normal.wordBack",
		Description: "向前移动一个词（B 忽略标点）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"e", "E"}, Action: "normal.wordEnd",
		Description: "移动到词尾（E 忽略标点）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"^"}, Action: "normal.firstNonBlank",
		Description: "跳到行首第一个非空白", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"$"}, Action: "normal.lineEnd",
		Description: "跳到行尾", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"g"}, Action: "normal.waitG",
		Description: "g 组合等待（gj/gk 视觉行移动）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"gg"}, Action: "normal.fileTop",
		Description: "跳到文件首行（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"G"}, Action: "normal.fileEnd",
		Description: "跳到文件末行", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"{"}, Action: "normal.paragraphUp",
		Description: "上移一段（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"}"}, Action: "normal.paragraphDown",
		Description: "下移一段（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"("}, Action: "normal.sentenceUp",
		Description: "上移一句（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{")"}, Action: "normal.sentenceDown",
		Description: "下移一句（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyCtrlD}, Action: "normal.halfDown",
		Description: "向下翻半页（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyCtrlU}, Action: "normal.halfUp",
		Description: "向上翻半页（可计数）", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyCtrlF}, Action: "normal.pageDown",
		Description: "向下翻一页", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyCtrlB}, Action: "normal.pageUp",
		Description: "向上翻一页", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"zz"}, Action: "normal.center",
		Description: "光标居中", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"zt"}, Action: "normal.top",
		Description: "光标置顶", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{"zb"}, Action: "normal.bottom",
		Description: "光标置底", Group: "普通导航态"},
	{Modes: []EditorMode{ModeNameEdit}, Keys: []string{KeyCtrlT}, Action: "edit.toggleOutline",
		Description: "开关大纲目录（标题跳转）", Group: "普通导航态"},

	// ---------------- Command 模式 ----------------
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{KeyEsc}, Action: "command.cancel",
		Description: "取消命令，回到预览", Group: "命令模式"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{KeyEnter}, Action: "command.execute",
		Description: "执行命令", Group: "命令模式"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{KeyBackspace, KeyDelete}, Action: "command.backspace",
		Description: "删除命令行一个字符", Group: "命令模式"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{KeySpace}, Action: "command.space",
		Description: "输入空格", Group: "命令模式"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{"<rune>"}, Action: "command.rune",
		Description: "输入字符追加到命令行", Group: "命令模式"},
	// 命令模式支持的命令（作为“动作”登记，便于查阅）
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":q"}, Action: "cmd.quit",
		Description: ":q 退出（有改动需 :q!）", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":q!"}, Action: "cmd.quitForce",
		Description: ":q! 强制退出", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":w"}, Action: "cmd.write",
		Description: ":w 保存（:w 文件名 另存为）", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":wq"}, Action: "cmd.writeQuit",
		Description: ":wq 保存并退出", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":u"}, Action: "cmd.undo",
		Description: ":u 撤销", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set nu"}, Action: "cmd.setnu",
		Description: ":set nu / :set number 绝对行号", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set rnu"}, Action: "cmd.setrnu",
		Description: ":set rnu 相对行号", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set nonu"}, Action: "cmd.setnonu",
		Description: ":set nonu 关闭行号", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set cursorblink"}, Action: "cmd.setcursorblink",
		Description: ":set cursorblink 开启光标闪烁", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set nocursorblink"}, Action: "cmd.setnocursorblink",
		Description: ":set nocursorblink 关闭光标闪烁", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set smoothscroll"}, Action: "cmd.setsmoothscroll",
		Description: ":set smoothscroll 开启 vim 式平滑滚动（按视觉行滚动）", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":set nosmoothscroll"}, Action: "cmd.setnosmoothscroll",
		Description: ":set nosmoothscroll 关闭平滑滚动（老终端兼容，改为按 buffer 行滚动）", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":ex"}, Action: "cmd.ex",
		Description: ":ex 打开文件浏览器", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":e"}, Action: "cmd.edit",
		Description: ":e [文件] / :e! 重新加载文件", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":d"}, Action: "cmd.deleteLine",
		Description: ":d / :dd 删除当前行", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":%d"}, Action: "cmd.deleteAll",
		Description: ":%d 清空当前文件所有内容", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":dk"}, Action: "cmd.deleteUpTo",
		Description: ":dk 行号 从当前行向上删除到指定行", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":dj"}, Action: "cmd.deleteDownTo",
		Description: ":dj 行号 从当前行向下删除到指定行", Group: "命令模式命令"},
	{Modes: []EditorMode{ModeNameCommand}, Keys: []string{":help"}, Action: "cmd.help",
		Description: ":help 查看命令模式命令帮助 / :keymap 查看键位表", Group: "命令模式命令"},

	// ---------------- 文件浏览器 ----------------
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{KeyEsc}, Action: "fb.cancel",
		Description: "退出文件浏览器", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"j", KeyDown}, Action: "fb.down",
		Description: "下移光标", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"k", KeyUp}, Action: "fb.up",
		Description: "上移光标", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{KeyEnter}, Action: "fb.confirm",
		Description: "进入目录 / 选择文件", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"d"}, Action: "fb.mkdir",
		Description: "创建文件夹", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"t"}, Action: "fb.mkfile",
		Description: "创建文件", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"r"}, Action: "fb.delete",
		Description: "删除选中项（弹确认）", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"m"}, Action: "fb.rename",
		Description: "重命名选中项（预填原名）", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{KeyTab}, Action: "fb.toggleFocus",
		Description: "切换焦点：左栏列表 / 右栏预览", Group: "文件浏览器"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"y", "Y", KeyEnter}, Action: "fb.confirmDelete",
		Description: "确认删除", Group: "文件浏览器(确认)"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{"n", "N", KeyEsc}, Action: "fb.cancelDelete",
		Description: "取消删除", Group: "文件浏览器(确认)"},
	{Modes: []EditorMode{ModeNameFileBrowser}, Keys: []string{KeyBackspace, KeyDelete}, Action: "fb.backspace",
		Description: "输入名称时退格", Group: "文件浏览器(输入)"},
}

// Matches 判断给定按键串是否命中本绑定（任一 Keys 命中即可）。
func (b Binding) Matches(key string) bool {
	return slices.Contains(b.Keys, key)
}

// LookupBindings 返回指定模式下注册的所有键位（含全局项）。
func LookupBindings(mode EditorMode) []Binding {
	out := []Binding{}
	for _, b := range DefaultKeyMap {
		if slices.Contains(b.Modes, mode) {
			out = append(out, b)
		}
	}
	return out
}

// GroupedBindings 按 Group 聚合所有键位，保持注册表首次出现的分组顺序。
func GroupedBindings() map[string][]Binding {
	order := []string{}
	m := map[string][]Binding{}
	for _, b := range DefaultKeyMap {
		if _, ok := m[b.Group]; !ok {
			m[b.Group] = []Binding{}
			order = append(order, b.Group)
		}
		m[b.Group] = append(m[b.Group], b)
	}
	res := map[string][]Binding{}
	for _, g := range order {
		res[g] = m[g]
	}
	return res
}

// formatKeys 把按键列表渲染为可读串，如 "j / ↓ / enter"。
func formatKeys(keys []string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k)
	}
	return joinHuman(parts)
}

// joinHuman 用 " / " 连接，空列表返回 "-"。
func joinHuman(parts []string) string {
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " / ")
}

// RenderKeyMapHelp 生成完整的键位帮助文本（供 :help / :keymap 视图使用）。
// 分组名与每条描述均经 T() 国际化（zh 原文为 key，en 命中译文）。
func RenderKeyMapHelp() string {
	var b strings.Builder
	b.WriteString(T("termd 键位一览（Esc 关闭本视图）"))
	b.WriteString("\n")
	b.WriteString("══════════════════════════════════════\n\n")
	for group, bindings := range GroupedBindings() {
		b.WriteString("【")
		b.WriteString(T(group))
		b.WriteString("】\n")
		for _, bd := range bindings {
			b.WriteString("  ")
			b.WriteString(padRight(formatKeys(bd.Keys), 22))
			b.WriteString(T(bd.Description))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// padRight 右补齐到 width，中文等非 ASCII 字符按 rune 计数粗略处理。
func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(r))
}

// commandDoc 描述一条命令模式命令的语法与说明（用于 :help 帮助视图）。
type commandDoc struct {
	syntax string // 命令语法（纯 ASCII，不翻译）
	usage  string // 人类可读用法说明（经 i18n）
}

// commandDocs 是命令模式全部命令的「单一事实来源」（供 :help 渲染）。
// 顺序即展示顺序。语法串保持 ASCII 不变，说明串走 T() 国际化。
var commandDocs = []commandDoc{
	{":q", "退出；若有未保存改动，提示改用 :q! 强制退出"},
	{":q!", "强制退出，丢弃所有未保存改动"},
	{":w", "保存当前文件（需在启动时指定文件名，或先 :w 文件名 命名）"},
	{":w 文件名", "另存为 / 创建指定文件（% 展开为当前文件名）"},
	{":w!", "强制保存：文件不存在时自动创建，覆盖只读文件"},
	{":wq / :wq!", "保存并退出（:wq! 强制保存后退出）"},
	{":wq 文件名", "保存到指定文件后退出"},
	{":u", "撤销上一次编辑操作"},
	{":e [文件]", "重新加载文件；无参等价于 :e %（重开当前文件）"},
	{":e! [文件]", "强制重新加载，丢弃未保存改动"},
	{":set nu", "显示绝对行号"},
	{":set number", ":set nu 的等价写法（绝对行号）"},
	{":set rnu", "显示相对行号（以当前行为基准）"},
	{":set relativenumber", ":set rnu 的等价写法"},
	{":set nonu", "关闭行号显示"},
	{":set nonumber", ":set nonu 的等价写法（关闭行号）"},
	{":set norelativenumber", "关闭相对行号（回到关闭行号）"},
	{":set cursorblink", "开启光标闪烁（预览/命令模式的光标会闪烁）"},
	{":set nocursorblink", "关闭光标闪烁（光标常亮不闪烁）"},
	{":set smoothscroll", "开启 vim 式平滑滚动（按视觉行滚动，滚轮/翻页逐屏幕行移动）"},
	{":set nosmoothscroll", "关闭平滑行滚动（老终端兼容，改为全量重绘）"},
	{":ex", "打开文件浏览器（可新建 / 删除文件与目录）"},
	{":d / :dd", "删除当前高亮行（两者等价）"},
	{":%d", "清空当前文件全部内容（保留一行空行）"},
	{":dk 行号", "从当前行向上删除到指定行（含两端，行号 1-based）；无行号删到首行"},
	{":dj 行号", "从当前行向下删除到指定行（含两端，行号 1-based）；无行号删到末行"},
	{":数字", "跳转到指定行（如 :12 跳到第 12 行，1-based）"},
	{":%", "显示当前文件名（未命名缓冲区显示「未命名」）"},
	{":help", "查看本命令模式命令帮助（Esc 关闭）"},
	{":keymap", "查看键位帮助一览（Esc 关闭）"},
	{":toc", "开关大纲目录侧边栏（等价快捷键 ctrl+t，用于标题跳转）"},
}

// RenderCommandHelp 生成命令模式命令帮助文本（供 :help 视图使用）。
// 标题与每条说明均经 T() 国际化；语法串保持 ASCII 原样。
func RenderCommandHelp() string {
	var b strings.Builder
	b.WriteString(T("termd 命令模式命令帮助（Esc 关闭本视图）"))
	b.WriteString("\n")
	b.WriteString("════════════════════════════════════════════\n\n")
	b.WriteString(T("进入方式：在预览模式按 : 进入命令模式，输入命令后回车执行。"))
	b.WriteString("\n\n")
	for _, c := range commandDocs {
		b.WriteString("  ")
		b.WriteString(padRight(c.syntax, 22))
		b.WriteString(T(c.usage))
		b.WriteString("\n")
	}
	return b.String()
}
