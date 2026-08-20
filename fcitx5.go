package main

import (
	"github.com/charmbracelet/bubbletea"
)

// ============================================================
// fcitx5.go —— Linux fcitx5 输入法适配层
// ============================================================
//
// 背景：fcitx5 在激活/反激活输入法时，会经由终端向程序发送“焦点报告”
// 转义序列 —— 进入输入法时发送 \x1b[I（Focus In），离开时发送 \x1b[O
// （Focus Out）。bubbletea 默认【不会】解析这两个序列，于是 \x1b[I 中的
// 字母 'I'（以及 \x1b[O 中的 'O'）会被当作普通字符（tea.KeyRunes）插入到
// 缓冲区，表现为“随机插入 i / o 内容”，且预编辑过程中其它 CSI 序列也可能
// 被误判为字符，造成严重乱序。
//
// 本文件提供两套互补的适配手段：
//   1. WithFcitx5()：返回一组 bubbletea 启动选项。关键是启用
//      tea.WithReportFocus()，让框架把 \x1b[I / \x1b[O 正确识别为
//      FocusMsg / BlurMsg，从根本上不再把它们当字符。
//   2. fcitx5InputFilter：一个 tea.Middleware（经 tea.WithFilter 注入）。
//      作为终极兜底，在进入 model.Update 之前清洗 KeyMsg 中可能混入的
//      ANSI/CSI 控制字节（如残留的 \x1b 引导序列、不可打印控制字符），
//      避免任何脏字符被写入文本缓冲。同时把 IME 激活/反激活状态通过
//      fcitx5IMEMsg 转发给 model，供其在输入法激活期做额外规避。
//
// 用法（见 main.go）：
//   p := tea.NewProgram(model, append(
//       []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()},
//       WithFcitx5()...,
//   )...)

// fcitx5IMEMsg 在输入法“进入/离开”时由过滤器转发给 model，
// 便于 UI 在 IME 激活态做规避（如禁用依赖裸字符的快捷键）。
type fcitx5IMEMsg struct{ Active bool }

// WithFcitx5 返回适配 fcitx5 所需的 bubbletea 启动选项集合。
// 关键项：
//   - tea.WithReportFocus()：让框架识别 \x1b[I / \x1b[O 焦点序列，
//     不再把它们当字符插入（这是修复“随机插入 i”的根本手段）。
func WithFcitx5() []tea.ProgramOption {
	return []tea.ProgramOption{
		// 焦点报告：把 fcitx5 的 IME 激活/反激活转义序列解析为 Focus/Blur 事件。
		tea.WithReportFocus(),
		// 输入兜底过滤器：清洗混入的 ANSI 控制字节，并把 IME 状态转发给 model。
		tea.WithFilter(fcitx5InputFilter),
	}
}

// fcitx5InputFilter 是 bubbletea 的输入过滤器（Middleware）。
// 在每条消息进入 model.Update 之前执行：
//   - FocusMsg  → 转发 fcitx5IMEMsg{Active:true}（输入法激活）
//   - BlurMsg   → 转发 fcitx5IMEMsg{Active:false}（输入法反激活）
//   - KeyMsg    → 清洗其中可能混入的 CSI/ANSI 控制字节与不可打印字符，
//     剥离掉脏字符后若仍有可读字符才继续，否则丢弃该消息。
// msgCmd 将一个消息包成 tea.Cmd（用于 tea.Batch 同时转发多条消息）。
func msgCmd(m tea.Msg) tea.Cmd {
	return func() tea.Msg { return m }
}

func fcitx5InputFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch mm := msg.(type) {
	case tea.FocusMsg:
		// 输入法激活：先让框架自身的 Focus 事件通过，再补一条 IME 状态消息。
		return tea.Batch(msgCmd(mm), msgCmd(fcitx5IMEMsg{Active: true}))
	case tea.BlurMsg:
		return tea.Batch(msgCmd(mm), msgCmd(fcitx5IMEMsg{Active: false}))
	case tea.KeyMsg:
		cleaned, ok := sanitizeKeyRunes(mm)
		if !ok {
			// 清洗后无任何可读字符（整条都是脏控制序列）：直接丢弃，
			// 防止 \x1b[I 之类残留污染文本缓冲。
			return nil
		}
		return cleaned
	}
	return msg
}

// sanitizeKeyRunes 清洗 KeyMsg 中可能混入的 fcitx5/终端控制字节：
//   - 丢弃所有 ASCII 控制字符（< 0x20，换行/制表/退格等编辑器专用键已由
//     bubbletea 解析为独立 KeyType，不会以 rune 形式出现，故此处可安全剥离）。
//   - 丢弃独立的 \x1b（ESC，0x1b）及其后的 CSI 引导残留——这些通常是未被
//     框架消费的输入法转义序列，混入文本会表现为乱码/乱序。
//   - 保留正常的可打印 rune（含中文、emoji 等多字节字符）。
//
// 返回 (清洗后的 KeyMsg, 是否保留)。若清洗后无任何可读字符，ok 为 false，
// 表示调用方应丢弃该消息。
func sanitizeKeyRunes(k tea.KeyMsg) (tea.KeyMsg, bool) {
	if k.Type != tea.KeyRunes {
		// 非字符键（方向键、Enter、Esc 等）已由框架正确解析，原样通过。
		return k, true
	}
	clean := make([]rune, 0, len(k.Runes))
	for _, r := range k.Runes {
		if r == 0x1b {
			// 丢弃裸 ESC：通常是未解析的 IME/终端转义序列残留，
			// 不应作为字符写入缓冲区（这正是“随机插入 i”的根因之一）。
			continue
		}
		if r < 0x20 {
			// 丢弃其它不可打印控制字符（NUL/SOH/.../DEL 等）。
			continue
		}
		clean = append(clean, r)
	}
	if len(clean) == 0 {
		return k, false
	}
	k.Runes = clean
	return k, true
}
