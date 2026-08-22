package core

import (
	"charm.land/bubbletea/v2"
	"termd"
)

// Update 处理所有消息（键盘、窗口尺寸、退出等）。
func (m *EditorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case tea.PasteMsg:
		// 粘贴（bracketed paste）：Preview 模式无插入语义，自动切到 Edit 插入态，
		// 避免粘贴内容被 Preview 按键处理完全忽略（表现为“粘贴没有生效”）。
		if m.sm.Mode() == termd.ModePreview {
			if err := m.sm.EnterEdit(); err == nil {
				m.cursorRow = clamp(m.previewCursor, 0, m.Buf.LineCount()-1)
				m.cursorCol = 0
				m.cursWant = 0
				m.ensureCursorVisible()
				m.status = termd.T("已粘贴（Preview 自动进入编辑）")
			}
			return m.handleInsertKey(tea.KeyPressMsg{Text: msg.Content})
		}
		// Edit 模式下直接处理粘贴内容
		return m.handleInsertKey(tea.KeyPressMsg{Text: msg.Content})

	case SwapTickMsg:
		// 后台请求采集快照（编辑线程）：交给 termd.SwapManager 在 UI 线程内安全采集。
		if m.Swap != nil {
			m.Swap.Produce()
		}
		return m, nil

	case termd.Fcitx5IMEMsg:
		// fcitx5 输入法激活/反激活通知（来自 fcitx5.go 过滤器）。
		// 激活时记录状态：编辑模式下跳过单字母快捷键拦截，让候选词字母
		// 直接写入文本，避免输入法选词字符被当作命令吞掉。
		m.imeActive = msg.Active
		return m, nil

	case tea.KeyMsg:
		// 键位帮助视图打开时，仅响应 Esc 关闭（其余按键忽略）
		if m.helpMode {
			ks := ""
			if km, ok := msg.(tea.KeyPressMsg); ok {
				ks = km.Keystroke()
			}
			if ks == "esc" {
				m.helpMode = false
				m.status = termd.T("已关闭键位帮助")
			}
			return m, nil
		}
		// 命令模式帮助视图打开时，仅响应 Esc 关闭（其余按键忽略）
		if m.cmdHelpMode {
			ks := ""
			if km, ok := msg.(tea.KeyPressMsg); ok {
				ks = km.Keystroke()
			}
			if ks == "esc" {
				m.cmdHelpMode = false
				m.status = termd.T("已关闭命令帮助")
			}
			return m, nil
		}
		// Markdown 语法教程视图打开时：Esc 关闭，j/k/上下键/翻页键滚动教程内容
		if m.markdownLangMode {
			ks := ""
			if km, ok := msg.(tea.KeyPressMsg); ok {
				ks = km.Keystroke()
			}
			switch ks {
			case "esc":
				m.markdownLangMode = false
				m.mlScroll = 0
				m.status = termd.T("已关闭 Markdown 语法教程")
			case "j", "down", "pgdown", "space":
				m.mlScroll += max(1, m.contentHeight()-4)
			case "k", "up", "pgup":
				m.mlScroll -= max(1, m.contentHeight()-4)
			case "ctrl+d":
				m.mlScroll += max(4, m.contentHeight()/2)
			case "ctrl+u":
				m.mlScroll -= max(4, m.contentHeight()/2)
			case "g":
				m.mlScroll = 0
			case "G", "end":
				m.mlScroll = 1 << 30 // 渲染时会被 clamp 到底
			}
			return m, nil
		}
		// 文件浏览器打开时，事件优先交给浏览器处理
		if m.fb != nil && m.fb.Opened {
			return m.handleFileBrowserKey(msg)
		}
		// 集中控制硬件光标：编辑模式隐藏光标（用反显块表示位置），
		// 预览/命令模式显示光标并定位到当前行（由 renderPreview 注入定位序列）。
		var cmd tea.Cmd
		switch m.sm.Mode() {
		case termd.ModePreview:
			m2, c := m.handlePreviewKey(msg)
			m = m2.(*EditorModel)
			cmd = c
		case termd.ModeEdit:
			m2, c := m.handleEditKey(msg)
			m = m2.(*EditorModel)
			cmd = c
		case termd.ModeCommand:
			m2, c := m.handleCommandKey(msg)
			m = m2.(*EditorModel)
			cmd = c
		}
		// 光标控制已移至 View() 方法，通过 tea.View.Cursor 字段管理
		return m, cmd

	case tea.QuitMsg:
		m.quitting = true
		return m, tea.Quit

	case termd.ImgReadyMsg:
		// 后台图片加载完成：标记 Preview 需要重建，下一次 View 即可渲染真实图片。
		// 注意：切换回 Edit 模式后无需重建，这里仅在 Preview 时生效（rebuildPreview 自带脏检查）。
		if m.sm.Mode() == termd.ModePreview {
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
