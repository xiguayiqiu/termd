package core

import (
	"github.com/charmbracelet/bubbletea"
	"termd"
)

// ----------------------------------------------------------
// 文件浏览器按键处理
// ----------------------------------------------------------
func (m *EditorModel) handleFileBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fb := m.fb
	switch fb.Mode {
	case termd.FBInput:
		// 输入名称子模式：捕获字符、退格、光标移动、回车提交、Esc 取消
		switch msg.Type {
		case tea.KeyEsc:
			fb.Cancel()
			m.status = termd.T("已取消")
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
	case termd.FBConfirm:
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
		case termd.KeyEsc: // keymap: fb.cancel
			fb.Close()
			m.status = termd.T("已退出文件浏览器")
		case "ctrl+o": // 再次按下关闭文件浏览器（与 Esc 等价，形成开关）
			fb.Close()
			m.status = termd.T("已退出文件浏览器")
		case "tab": // keymap: fb.toggleFocus
			// 焦点在左栏列表 / 右栏预览之间切换
			fb.ToggleFocus()
		case "j", termd.KeyDown: // keymap: fb.down
			if fb.FocusRight {
				fb.ScrollPreview(1) // 右栏：滚动预览
			} else {
				fb.Move(1) // 左栏：移动光标
			}
		case "k", termd.KeyUp: // keymap: fb.up
			if fb.FocusRight {
				fb.ScrollPreview(-1) // 右栏：滚动预览
			} else {
				fb.Move(-1) // 左栏：移动光标
			}
		case termd.KeyPgDown: // keymap: fb.pageDown
			if fb.FocusRight {
				fb.ScrollPreview(visibleLines(m) - 2) // 右栏：半屏/整页下滚
			} else {
				fb.Move(visibleLines(m) - 2)
			}
		case termd.KeyPgUp: // keymap: fb.pageUp
			if fb.FocusRight {
				fb.ScrollPreview(-(visibleLines(m) - 2)) // 右栏：整页上滚
			} else {
				fb.Move(-(visibleLines(m) - 2))
			}
		case termd.KeyEnter: // keymap: fb.confirm
			if path := fb.Confirm(); path != "" {
				// 选定文件：关闭浏览器并加载该文件
				fb.Close()
				if err := m.Buf.LoadFile(path); err != nil {
					m.status = termd.T("打开失败: ") + err.Error()
				} else {
					m.scroll = 0
					m.previewScroll = 0
					m.previewCursor = 0
					m.previewDirty = true // 强制重建 Preview 缓存，否则会显示旧文件内容
					m.status = termd.T("已打开 ") + path
				}
			}
		case "d": // keymap: fb.mkdir
			// 创建文件夹（输入名称后回车确认）
			fb.StartCreate("dir")
			m.status = termd.T("输入文件夹名后回车确认")
		case "t": // keymap: fb.mkfile
			// 创建文件（输入名称后回车确认）
			fb.StartCreate("file")
			m.status = termd.T("输入文件名后回车确认")
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
				m.status = termd.T("输入新名称后回车确认")
			}
		}
		return m, nil
	}
}
