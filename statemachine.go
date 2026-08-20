package main

import "errors"

// ============================================================
// StateMachine —— 三模式状态机
// ============================================================
//
// 严格遵循以下模式切换逻辑：
//
//   Preview Mode（预览模式，默认，仿 vim Normal 的浏览态）
//     │
//     │ 按 'i' / 'e' / 'a'           ┌──────────────┐
//     └──────────────────────────►  │  Edit Mode   │
//                                     │  (editInsert │ 直接键入文本
//                                     │   / editNormal│ 仿 vim Normal 导航)
//                                     │            │
//                                      │ Esc        │
//                                      └───────────┘
//     │ 按 ':' 进入命令模式（底部输入框）
//     ▼
//   Command Mode（命令模式）
//     - 输入 :q / :w / :wq / :34 / /key / :u / :ex / :set nu 等
//     - 回车执行；Esc 取消，回到 Preview
//
// 注：Edit 与 Command 互不直连，二者都以 Preview 为枢纽，避免状态爆炸。
//     Edit 模式内部再分 editInsert（插入）/ editNormal（普通导航）两态，
//     形成完整的 vim 式 插入↔普通 循环（仿 vim 的 INSERT/NORMAL）。

// Mode 表示编辑器当前所处模式。
type Mode int

const (
	// ModePreview 预览模式（默认）
	ModePreview Mode = iota
	// ModeEdit 编辑模式
	ModeEdit
	// ModeCommand 命令模式
	ModeCommand
)

// modeNames 用于状态栏显示。
var modeNames = map[Mode]string{
	ModePreview: "PREVIEW",
	ModeEdit:    "EDIT",
	ModeCommand: "COMMAND",
}

// editSub 表示 Edit 模式内部的子状态（仿 vim 的 INSERT / NORMAL 切换）。
type editSub int

const (
	// editInsert 插入态：键入的字符直接写入缓冲区（仿 vim INSERT）。
	editInsert editSub = iota
	// editNormal 普通导航态：字母键用于移动/编辑命令，不直接写入文本（仿 vim NORMAL）。
	editNormal
)

// StateMachine 维护当前模式及模式相关临时数据。
type StateMachine struct {
	mode Mode
	// editSub 仅在 mode==ModeEdit 时有效，区分插入态与普通导航态。
	editSub editSub
	// cmdInput 命令模式下的输入缓冲（含前导冒号或斜杠）
	cmdInput string
	// searchKeyword 当前搜索关键字（/ 进入），空表示未搜索
	searchKeyword string
	// lineNumMode 控制行号显示方式：none=关闭，abs=绝对行号，rel=相对行号
	lineNumMode lineNumMode
}

// lineNumMode 行号显示模式。
type lineNumMode int

const (
	// lnNone 不显示行号
	lnNone lineNumMode = iota
	// lnAbs 绝对行号（:set nu）
	lnAbs
	// lnRel 相对行号（:set rnu），当前行显示绝对行号
	lnRel
)

// lineNumModeName 返回行号模式的可读名称，用于状态栏展示。
func (m lineNumMode) name() string {
	switch m {
	case lnAbs:
		return "nu"
	case lnRel:
		return "rnu"
	default:
		return "nonu"
	}
}

// NewStateMachine 创建默认处于 Preview 模式的实例。
func NewStateMachine() *StateMachine {
	return &StateMachine{mode: ModePreview}
}

// Mode 返回当前模式。
func (s *StateMachine) Mode() Mode { return s.mode }

// EnterEdit 从 Preview 进入 Edit 模式（默认进入插入态，仅当当前为 Preview 时合法）。
func (s *StateMachine) EnterEdit() error {
	if s.mode != ModePreview {
		return errors.New("只能在 Preview 模式按 i/e 进入编辑")
	}
	s.mode = ModeEdit
	s.editSub = editInsert
	return nil
}

// EnterEditNormal 从 Preview 进入 Edit 模式并停在普通导航态（仿 vim 的 Normal）。
func (s *StateMachine) EnterEditNormal() error {
	if s.mode != ModePreview {
		return errors.New("只能在 Preview 模式进入编辑")
	}
	s.mode = ModeEdit
	s.editSub = editNormal
	return nil
}

// SetEditSub 设置 Edit 内部子状态（insert/normal 切换）。
func (s *StateMachine) SetEditSub(sub editSub) {
	if s.mode == ModeEdit {
		s.editSub = sub
	}
}

// EditSub 返回当前 Edit 子状态（非 Edit 模式时恒为 editInsert）。
func (s *StateMachine) EditSub() editSub { return s.editSub }

// EnterCommand 从 Preview 进入 Command 模式。
func (s *StateMachine) EnterCommand() error {
	if s.mode != ModePreview {
		return errors.New("只能在 Preview 模式按 : 进入命令")
	}
	s.mode = ModeCommand
	s.cmdInput = ":" // 预置冒号提示符
	return nil
}

// EnterSearch 从 Preview 进入搜索输入（复用 Command 模式的输入通道，以 / 开头）。
func (s *StateMachine) EnterSearch() error {
	if s.mode != ModePreview {
		return errors.New("只能在 Preview 模式按 / 搜索")
	}
	s.mode = ModeCommand
	s.cmdInput = "/" // 以斜杠作为搜索前缀
	return nil
}

// ExitToPreview 退回到 Preview 模式，并清空临时输入缓冲与编辑子状态。
func (s *StateMachine) ExitToPreview() {
	s.mode = ModePreview
	s.editSub = editInsert
	s.cmdInput = ""
}

// editSubName 返回编辑子状态的可读名称，用于状态栏展示。
func (s *StateMachine) editSubName() string {
	if s.mode != ModeEdit {
		return ""
	}
	if s.editSub == editInsert {
		return "INSERT"
	}
	return "NORMAL"
}

// AppendCmd 向命令缓冲区追加字符（命令模式输入时使用）。
func (s *StateMachine) AppendCmd(r rune) {
	s.cmdInput += string(r)
}

// BackspaceCmd 删除命令缓冲区最后一个字符。
func (s *StateMachine) BackspaceCmd() {
	if len(s.cmdInput) <= 1 {
		// 仅剩前缀符时退格 => 取消命令模式
		s.ExitToPreview()
		return
	}
	s.cmdInput = s.cmdInput[:len(s.cmdInput)-1]
}

// SetLineNum 设置行号显示模式。
func (s *StateMachine) SetLineNum(m lineNumMode) {
	s.lineNumMode = m
}

// LineNumMode 返回当前行号显示模式。
func (s *StateMachine) LineNumMode() lineNumMode {
	return s.lineNumMode
}
