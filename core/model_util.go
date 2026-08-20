package core

import (
	"strconv"
	"termd"
)

// ----------------------------------------------------------
// 工具函数
// ----------------------------------------------------------
// touch 标记缓冲区内容已修改，并使 Preview 渲染缓存失效（下次进入预览时重建）。
func (m *EditorModel) touch() {
	m.Buf.IsDirty = true
	m.previewDirty = true
	// 内容变化：内容区滚动区缓存失效（下次滚动经连续性校验自动重新同步/重绘）。
	m.invalidateScrollRender()
	m.scrollActive = false
	m.scrollContent = nil
	// 同步标记崩溃恢复后台写盘：任何编辑路径都确保 swap 会在下个周期刷新。
	if m.Swap != nil {
		m.Swap.MarkDirty()
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
func (m *EditorModel) contentHeight() int {
	reserved := 4
	if m.sm.Mode() == termd.ModeCommand {
		reserved++
	}
	v := m.height - reserved
	if v < 1 {
		v = 1
	}
	return v
}

// visibleLines 兼容旧调用：返回当前内容区行数（基于命令模式动态预留）。
func visibleLines(m *EditorModel) int {
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
