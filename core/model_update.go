package core

import (
	"github.com/charmbracelet/bubbletea"
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
			if msg.String() == "esc" {
				m.helpMode = false
				m.status = termd.T("已关闭键位帮助")
			}
			return m, nil
		}
		// 命令模式帮助视图打开时，仅响应 Esc 关闭（其余按键忽略）
		if m.cmdHelpMode {
			if msg.String() == "esc" {
				m.cmdHelpMode = false
				m.status = termd.T("已关闭命令帮助")
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
		if m.sm.Mode() == termd.ModePreview {
			// 预览模式隐藏硬件光标（View 亦输出 ?25l 兜底），靠蓝底块指示当前行；
			// 状态栏常驻显示，但硬件光标不落在状态栏位置。
			cmd = tea.Batch(cmd, tea.HideCursor)
		} else if m.sm.Mode() == termd.ModeEdit {
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
