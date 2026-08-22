package termd

import (
	"charm.land/bubbletea/v2"
)

// ============================================================
// fcitx5.go —— Linux fcitx5 输入法适配层 (bubbletea v2)
// ============================================================
//
// 背景：fcitx5 在激活/反激活输入法时，会经由终端向程序发送“焦点报告”
// 转义序列 —— 进入输入法时发送 \x1b[I（Focus In），离开时发送 \x1b[O
// （Focus Out）。bubbletea v2 使用 ultraviolet 库处理输入，默认支持
// 焦点报告事件 (FocusMsg/BlurMsg)。
//
// 本文件提供两套互补的适配手段：
//   1. WithFcitx5()：返回一组 bubbletea 启动选项。包含输入过滤器。
//   2. fcitx5InputFilter：一个 tea.Middleware（经 tea.WithFilter 注入）。
//      作为终极兜底，在进入 model.Update 之前清洗 KeyMsg 中可能混入的
//      ANSI/CSI 控制字节（如残留的 \x1b 引导序列、不可打印控制字符），
//      避免任何脏字符被写入文本缓冲。同时把 IME 激活/反激活状态通过
//      Fcitx5IMEMsg 转发给 model，供其在输入法激活期做额外规避。
//
// 用法（见 main.go）：
//   p := tea.NewProgram(model, append(
//       []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()},
//       WithFcitx5()...,
//   )...)

// Fcitx5IMEMsg 在输入法“进入/离开”时由过滤器转发给 model，
// 便于 UI 在 IME 激活态做规避（如禁用依赖裸字符的快捷键）。
type Fcitx5IMEMsg struct{ Active bool }

// WithFcitx5 返回适配 fcitx5 所需的 bubbletea 启动选项集合。
// 关键项：
//   - tea.WithFilter(fcitx5InputFilter)：输入兜底过滤器，清洗混入的
//     ANSI 控制字节，并把 IME 状态转发给 model。
func WithFcitx5() []tea.ProgramOption {
	return []tea.ProgramOption{
		// 输入兜底过滤器：清洗混入的 ANSI 控制字节，并把 IME 状态转发给 model。
		tea.WithFilter(fcitx5InputFilter),
	}
}

// fcitx5InputFilter 是 bubbletea 的输入过滤器（Middleware）。
// 在每条消息进入 model.Update 之前执行：
//   - FocusMsg  → 转发 Fcitx5IMEMsg{Active:true}（输入法激活）
//   - BlurMsg   → 转发 Fcitx5IMEMsg{Active:false}（输入法反激活）
//   - KeyMsg    → 清洗其中可能混入的 CSI/ANSI 控制字节与不可打印字符，
//     剥离掉脏字符后若仍有可读字符才继续，否则丢弃该消息。
//
// msgCmd 将一个消息包成 tea.Cmd（用于 tea.Batch 同时转发多条消息）。
func msgCmd(m tea.Msg) tea.Cmd {
	return func() tea.Msg { return m }
}

func fcitx5InputFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch mm := msg.(type) {
	case tea.FocusMsg:
		// 输入法激活：先让框架自身的 Focus 事件通过，再补一条 IME 状态消息。
		return tea.Batch(msgCmd(mm), msgCmd(Fcitx5IMEMsg{Active: true}))
	case tea.BlurMsg:
		return tea.Batch(msgCmd(mm), msgCmd(Fcitx5IMEMsg{Active: false}))
	case tea.KeyPressMsg:
		cleaned, ok := sanitizeKeyPress(mm)
		if !ok {
			// 清洗后无任何可读字符（整条都是脏控制序列）：直接丢弃，
			// 防止 \x1b[I 之类残留污染文本缓冲。
			return nil
		}
		return cleaned
	case tea.KeyReleaseMsg:
		// KeyReleaseMsg 通常不包含文本内容，直接通过
		return mm
	}
	return msg
}

// sanitizeKeyPress 清洗 KeyPressMsg 中可能混入的 fcitx5/终端控制字节：
//   - 丢弃独立的 ESC（0x1b）——通常是未被框架消费的输入法转义序列
//     残留，混入文本会表现为乱码/乱序（这正是“随机插入 i”的根因之一）。
//   - 丢弃其它 ASCII 控制字符（< 0x20）：NUL/SOH/.../DEL 等。
//   - 保留正常的可打印字符（含中文、emoji 等多字节字符）。
//   - 粘贴文本通过 PasteMsg 单独传递，不在此处处理。
//
// 返回 (清洗后的 KeyPressMsg, 是否保留)。若清洗后无任何可读字符，ok 为 false，
// 表示调用方应丢弃该消息。
func sanitizeKeyPress(k tea.KeyPressMsg) (tea.KeyPressMsg, bool) {
	key := k.Key()
	// 非字符键（方向键、Enter、Esc 等）已由框架正确解析，原样通过。
	// 字符键的特征：Text 非空，且 Code 为 0 或等于 Text 的第一个字符
	if key.Text == "" {
		return k, true
	}

	clean := make([]rune, 0, len(key.Text))
	for _, r := range key.Text {
		if r == 0x1b {
			// 丢弃裸 ESC：通常是未解析的 IME/终端转义序列残留，
			// 不应作为字符写入缓冲区（这正是“随机插入 i”的根因之一）。
			continue
		}
		if r < 0x20 {
			// 丢弃其它不可打印控制字符（NUL/SOH/.../DEL 等）。
			// 注意：\t (0x09)、\n (0x0a)、\r (0x0d) 在正常输入中不会以 KeyPressMsg 形式出现，
			// 它们会被解析为 KeyTab、KeyEnter 等特殊键。
			// 粘贴内容通过 PasteMsg 传递，保留其中的换行/制表符。
			continue
		}
		clean = append(clean, r)
	}
	if len(clean) == 0 {
		return k, false
	}
	// 创建新的 KeyPressMsg，保留原有的修饰键等信息
	newKey := key
	newKey.Text = string(clean)
	newKey.Code = 0 // 字符键的 Code 通常为 0
	return tea.KeyPressMsg(newKey), true
}