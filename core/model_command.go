package core

import (
	"fmt"
	"strconv"
	"strings"
	"termd"

	"charm.land/bubbletea/v2"
)

// expandPercent 将命令行参数中的 `%` 展开为当前文件名，与 Vim 的 cmdline 变量语义保持一致：
//   - `%`           展开为当前缓冲区文件名（Vim 的 curbuf->b_fname）。
//   - `\%`          反斜杠转义，保留字面 `%`（Vim 中同样用反斜杠取消特殊含义）。
//
// loadFile 加载文件到当前缓冲区并重置光标/视口（供 :e 重新加载与
// 光标回车跳转本地 md 文件复用）。
func (m *EditorModel) loadFile(target string) error {
	if err := m.Buf.LoadFile(target); err != nil {
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
func (m *EditorModel) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	switch key.Code {
	case tea.KeyEsc: // keymap: command.cancel
		m.sm.ExitToPreview()
		m.status = termd.T("已取消命令")
		return m, nil

	case tea.KeyEnter: // keymap: command.execute
		return m, m.executeCommand(m.sm.CmdInput)

	case tea.KeyBackspace, tea.KeyDelete: // keymap: command.backspace
		m.sm.BackspaceCmd()

	case tea.KeySpace: // keymap: command.space
		m.sm.AppendCmd(' ')

	default:
		// 字符输入：key.Text 包含可打印字符
		if key.Text != "" {
			for _, r := range key.Text {
				m.sm.AppendCmd(r)
			}
		}
	}
	return m, nil
}

// executeCommand 解析并执行命令模式输入（返回可选的 quit 命令）。
func (m *EditorModel) executeCommand(input string) tea.Cmd {
	cmd := strings.TrimSpace(input)
	m.sm.ExitToPreview()

	switch {
	case cmd == ":q":
		if m.Buf.IsDirty {
			m.status = termd.T("有未保存改动，使用 :q! 强制退出")
		} else {
			m.quitting = true
			return tea.Quit
		}
	case cmd == ":q!":
		m.quitting = true
		return tea.Quit

	case cmd == ":w":
		// 保存到当前文件：若启动时未指定文件名，提示用 :w 文件名 创建
		if err := m.Buf.Save(false); err != nil {
			m.status = termd.T("保存失败: ") + err.Error()
		} else {
			m.status = termd.T("已保存 ") + m.Buf.FilePath()
		}
	case cmd == ":w!" || cmd == ":wq!" || cmd == ":wq":
		// 强制保存（含退出）：文件不存在时自动创建
		if err := m.Buf.Save(true); err != nil {
			m.status = termd.T("保存失败: ") + err.Error()
		} else {
			m.status = termd.T("已保存 ") + m.Buf.FilePath()
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
		fname := expandPercent(rest, m.Buf.FilePath())
		if rest == "%" && m.Buf.FilePath() == "" {
			m.status = termd.T("无文件名（缓冲区未命名，使用 :w 文件名 指定）")
			break
		}
		if fname == "" {
			m.status = termd.T("用法: :w 文件名（创建/另存为）")
			break
		}
		if err := m.Buf.SaveAs(fname); err != nil {
			m.status = termd.T("保存失败: ") + err.Error()
		} else {
			m.status = termd.T("已保存为 ") + fname
			// :wq / :wq! 带文件名 => 保存后退出
			if strings.HasPrefix(cmd, ":wq") {
				m.quitting = true
				return tea.Quit
			}
		}

	case cmd == ":u":
		if m.Buf.Undo() {
			m.status = termd.T("已撤销")
			// 撤销也属于内容变更：使 Preview 渲染缓存与滚动缓存失效并同步 swap，
			// 否则 Preview 模式画面不更新（:u "无效"的根因）。
			m.touch()
			// 命令执行后已回到 Preview 模式：撤销可能减少行数，先钳制预览光标，
			// 下次 renderPreview 重建缓存后会经 ensurePreviewCursorVisible 对齐视口。
			m.previewCursor = clamp(m.previewCursor, 0, m.Buf.LineCount()-1)
			m.previewColKeep()
			// Edit 光标同步钳制，防止下次切回 Edit 模式时越界（仿 vim check_cursor）。
			m.clampCursor()
			m.cursWant = m.cursorCol
			m.ensureCursorVisible()
		} else {
			m.status = termd.T("无可撤销操作")
		}

	case cmd == ":set nu", cmd == ":set number":
		m.sm.SetLineNum(termd.LNAbs)
		m.status = termd.T("行号显示: 绝对行号 (nu)")
	case cmd == ":set rnu", cmd == ":set relativenumber":
		m.sm.SetLineNum(termd.LNRel)
		m.status = termd.T("行号显示: 相对行号 (rnu)")
	case cmd == ":set nonu", cmd == ":set nonumber", cmd == ":set norelativenumber":
		m.sm.SetLineNum(termd.LNNone)
		m.status = termd.T("行号显示: 关闭 (nonu)")
	case cmd == ":set cursorblink":
		m.blinkMode = true
		m.status = termd.T("光标闪烁: 开启")
	case cmd == ":set nocursorblink":
		m.blinkMode = false
		m.status = termd.T("光标闪烁: 关闭")
	case cmd == ":set cursor block":
		m.cursorShape = 0
		m.status = termd.T("光标形状: 块")
	case cmd == ":set cursor bar":
		m.cursorShape = 2
		m.status = termd.T("光标形状: 竖线")
	case cmd == ":set cursor underline":
		m.cursorShape = 1
		m.status = termd.T("光标形状: 下划线")
	case cmd == ":set fileicons":
		termd.FBUseIcons = true
		m.status = termd.T("文件浏览器图标: 开启（需 Nerd Font 终端）")
	case cmd == ":set nofileicons":
		termd.FBUseIcons = false
		m.status = termd.T("文件浏览器图标: 关闭")
	case cmd == ":set smoothscroll":
		m.smoothScroll = true
		m.status = termd.T("平滑行滚动: 开启（vim 式行刷新）")
	case cmd == ":set nosmoothscroll":
		m.smoothScroll = false
		m.invalidateScrollRender() // 停用平滑滚动并清滚动缓存
		m.status = termd.T("平滑行滚动: 关闭（老终端兼容，改为全量重绘）")

	case cmd == ":ex":
		// 打开极简文件浏览器
		m.fb = termd.NewFileBrowser(".")
		m.fb.Open()
		m.status = termd.T("文件浏览器已打开")

	case cmd == ":help":
		// 打开命令模式命令帮助视图（termd.RenderCommandHelp），Esc 关闭
		m.cmdHelpMode = true
		m.status = termd.T("命令模式帮助（Esc 关闭）")
	case cmd == ":keymap":
		// 打开键位帮助视图（集中注册于 keymap.go），Esc 关闭
		m.helpMode = true
		m.status = termd.T("键位帮助（Esc 关闭）")
	case cmd == ":ml":
		// 打开 Markdown 语法教程视图（MarkdownLanguage.go），Esc 关闭，j/k 或滚轮翻页
		m.markdownLangMode = true
		m.mlScroll = 0
		m.status = termd.T("Markdown 语法教程（Esc 关闭，j/k 或滚轮翻页）")

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
			target = m.Buf.FilePath()
		} else {
			target = expandPercent(arg, m.Buf.FilePath())
		}
		if target == "" {
			m.status = termd.T("无文件名（缓冲区未命名，使用 :e 文件名 指定）")
			break
		}
		if !m.Buf.IsDirty || strings.HasPrefix(cmd, ":e!") {
			if err := m.loadFile(target); err != nil {
				m.status = termd.T("加载失败: ") + err.Error()
			} else {
				m.status = termd.T("已重新加载 ") + target
			}
		} else {
			m.status = termd.T("未保存的修改，使用 :e! 强制重载")
		}

	case strings.HasPrefix(cmd, ":") && isNumber(cmd[1:]):
		// :数字 跳转行
		n, _ := strconv.Atoi(cmd[1:])
		n-- // 1-based -> 0-based
		if n < 0 {
			n = 0
		}
		if n >= m.Buf.LineCount() {
			n = m.Buf.LineCount() - 1
		}
		m.previewCursor = n
		// 视口居中定位到目标行（仿 vim zz / glow 的 EnsureVisible）
		m.centerPreviewOnCursor()
		m.status = termd.Tf("跳转到第 %d 行", n+1)

	// ---- 删除命令（当前行以 Preview 高亮行为准，命令模式只能从 Preview 进入）----
	case cmd == ":%d":
		// 清空当前文件所有内容（保留一行空行）。
		last := m.Buf.LineCount() - 1
		for row := last; row >= 0; row-- {
			m.Buf.DeleteLine(row)
		}
		m.previewCursor = 0
		m.cursorRow = 0
		m.scroll = 0
		m.previewScroll = 0
		m.previewDirty = true
		m.status = termd.T("已清空当前文件所有内容")

	case strings.HasPrefix(cmd, ":dk"), strings.HasPrefix(cmd, ":dj"):
		// :dk 行号 / :dj 行号（行号 1-based）：从当前行向上(k)/向下(j)删除到指定行（含两端）。
		// 也接受无空格写法，如 :dk12 / :dj2。
		dir := cmd[2:3] // 'k' 或 'j'
		rest := strings.TrimSpace(cmd[3:])
		var target int
		lineCount := m.Buf.LineCount()
		if rest == "" {
			// 无行号：k 删到文件首行，j 删到文件末行。
			if dir == "k" {
				target = 0
			} else {
				target = lineCount - 1
			}
		} else if _, err := fmt.Sscanf(rest, "%d", &target); err != nil || target < 1 {
			m.status = termd.T("用法: :dk 行号 / :dj 行号（行号为 1-based）")
			break
		} else {
			target-- // 1-based -> 0-based
		}
		// 修正：previewCursor 是 buffer 行号（0-based），确保 target 在合法范围内
		if target < 0 {
			target = 0
		}
		if target >= lineCount {
			target = lineCount - 1
		}
		cur := m.previewCursor
		if cur < 0 {
			cur = 0
		}
		if cur >= lineCount {
			cur = lineCount - 1
		}
		from := min(cur, target)
		to := max(cur, target)
		// 至少保留 1 行
		if to >= lineCount {
			to = lineCount - 1
		}
		if from < 0 {
			from = 0
		}
		for row := to; row >= from; row-- {
			m.Buf.DeleteLine(row)
		}
		// 删除后同步游标：至少保留 1 行
		newCount := m.Buf.LineCount()
		if newCount == 0 {
			newCount = 1
		}
		m.previewCursor = clamp(from, 0, newCount-1)
		m.cursorRow = m.previewCursor
		m.scroll = 0
		m.previewScroll = 0
		m.previewDirty = true
		m.status = termd.Tf("已删除第 %d-%d 行", from+1, to+1)

	case cmd == ":d", cmd == ":dd":
		// 删除整行（当前行）。:d 与 :dd 等价，均删除当前高亮行。
		cur := m.previewCursor
		m.Buf.DeleteLine(cur)
		m.previewCursor = clamp(cur, 0, m.Buf.LineCount()-1)
		m.cursorRow = m.previewCursor
		m.previewDirty = true
		m.status = termd.Tf("已删除第 %d 行", cur+1)

	case cmd == ":%":
		// 单独输入 `:%` 时，与 Vim 中 `%`=当前文件名的语义一致：显示当前文件名。
		// （Vim 中 `%` 也作行范围 1,$ 使用，但本编辑器无带地址命令，此处以「当前文件」语义呈现。）
		if m.Buf.FilePath() == "" {
			m.status = termd.T("当前文件: （未命名缓冲区）")
		} else {
			m.status = termd.T("当前文件: ") + m.Buf.FilePath()
		}

	case strings.HasPrefix(cmd, "/"):
		// /关键字 搜索：记录关键字并跳到首个匹配 termd.Buffer 行（previewLines 与 buffer 1:1）
		kw := strings.TrimSpace(cmd[1:])
		if kw == "" {
			m.status = termd.T("搜索关键字为空")
			break
		}
		m.sm.SearchKeyword = kw
		if row := m.findFirstMatch(kw); row >= 0 {
			m.previewCursor = row
			// 视口居中定位到匹配行（仿 vim zz）
			m.centerPreviewOnCursor()
			m.status = termd.Tf("匹配 '%s' 于第 %d 行", kw, row+1)
		} else {
			m.status = termd.Tf("未找到 '%s'", kw)
		}

	default:
		m.status = termd.T("未知命令: ") + cmd
	}
	// 打开极简文件浏览器属于从 Preview 全屏切换到另一种全屏视图，
	// 必须先清除 Preview 模式遗留的终端物理滚动区（DECSTBM），
	// 否则该滚动区的 diff 缓存会与新两栏内容叠加，造成「预览残留与文件列表混合」。
	if m.fb != nil && m.fb.Opened {
		m.invalidateScrollRender()
		return m.clearScrollRegionCmd()
	}
	return nil
}

// findFirstMatch 从当前光标位置向后查找首个包含关键字的行。
func (m *EditorModel) findFirstMatch(kw string) int {
	kw = strings.ToLower(kw)
	start := m.previewCursor
	if start < 0 {
		start = 0
	}
	for i := start; i < m.Buf.LineCount(); i++ {
		if strings.Contains(strings.ToLower(string(m.Buf.GetLine(i))), kw) {
			return i
		}
	}
	// 环形回绕：从头再找一次
	for i := 0; i < start; i++ {
		if strings.Contains(strings.ToLower(string(m.Buf.GetLine(i))), kw) {
			return i
		}
	}
	return -1
}

// (findPreviewMatch 已删除：previewLines 现已与 buffer 行 1:1，复用 findFirstMatch 即可)
